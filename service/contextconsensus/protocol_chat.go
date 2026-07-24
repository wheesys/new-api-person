package contextconsensus

import (
	"fmt"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/types"
)

func extractChatCompletions(extractionRequest ExtractionRequest) (*ContextEnvelope, error) {
	var request dto.GeneralOpenAIRequest
	if err := common.Unmarshal(extractionRequest.Body, &request); err != nil {
		return nil, fmt.Errorf("decode chat completions request: %w", err)
	}
	envelope := &ContextEnvelope{
		Protocol:           types.RelayFormatOpenAI,
		OriginalModel:      request.Model,
		RequestedMaxOutput: request.MaxTokens,
		ProviderBinding: ProtocolBinding{
			BindingLevel: BindingLevelNone,
			RelayFormat:  types.RelayFormatOpenAI,
		},
	}
	if request.MaxCompletionTokens != nil {
		envelope.RequestedMaxOutput = request.MaxCompletionTokens
	}

	toolEvents := make([]ToolEvent, 0)
	for messageIndex, message := range request.Messages {
		path := fmt.Sprintf("messages[%d]", messageIndex)
		role := strings.ToLower(strings.TrimSpace(message.Role))
		toolCalls := make([]dto.ToolCallRequest, 0)
		if rawJSONPresent(message.ToolCalls) {
			if err := common.Unmarshal(message.ToolCalls, &toolCalls); err != nil {
				return envelope, fmt.Errorf("decode %s.tool_calls: %w", path, err)
			}
		}
		for _, toolCall := range toolCalls {
			argumentsDigest := digestString(toolCall.Function.Arguments)
			if len(toolCall.Custom) > 0 {
				argumentsDigest = digestBytes(toolCall.Custom)
			}
			toolEvents = append(toolEvents, ToolEvent{
				Kind:          ToolEventCall,
				Protocol:      types.RelayFormatOpenAI,
				Sequence:      messageIndex,
				ParallelGroup: messageIndex,
				CallID:        toolCall.ID,
				FunctionName:  toolCall.Function.Name,
				PayloadDigest: argumentsDigest,
			})
		}
		if role == "tool" {
			toolEvents = append(toolEvents, ToolEvent{
				Kind:          ToolEventResult,
				Protocol:      types.RelayFormatOpenAI,
				Sequence:      messageIndex,
				ParallelGroup: messageIndex,
				CallID:        message.ToolCallId,
				PayloadDigest: digestValue(message.Content),
			})
		}

		hasMedia, providerBoundMedia := inspectGenericMedia(message.Content, &envelope.MediaState)
		if providerBoundMedia {
			requireBinding(&envelope.ProviderBinding, BindingLevelCredential, "provider_file_reference", "")
		}
		immutable := role == "system" || role == "developer"
		preserved := messageIndex == len(request.Messages)-1 || len(toolCalls) > 0 || role == "tool" || hasMedia
		kind := SegmentKindMessage
		if immutable {
			kind = SegmentKindInstruction
		} else if len(toolCalls) > 0 {
			kind = SegmentKindToolCall
		} else if role == "tool" {
			kind = SegmentKindToolResult
		} else if hasMedia {
			kind = SegmentKindMedia
		}
		appendSegment(envelope, contextSegment(messageIndex, path, role, kind, message, !immutable && !preserved, providerBoundMedia), immutable, preserved)
	}

	graph, issues := ValidateToolGraph(toolEvents)
	if len(request.Tools) > 0 {
		graph.SchemaDigest = digestValue(request.Tools)
	}
	envelope.ToolState = graph
	if request.ResponseFormat != nil && (request.ResponseFormat.Type == "json_schema" || request.ResponseFormat.Type == "json_object") {
		strict := false
		if rawJSONPresent(request.ResponseFormat.JsonSchema) {
			var schemaConfiguration any
			if err := common.Unmarshal(request.ResponseFormat.JsonSchema, &schemaConfiguration); err == nil {
				_, strict = containsJSONSchema(schemaConfiguration)
				if schemaMap, ok := schemaConfiguration.(map[string]any); ok {
					if strictValue, ok := schemaMap["strict"].(bool); ok {
						strict = strictValue
					}
				}
			}
		}
		envelope.SchemaState = SchemaState{
			Present: true,
			Strict:  strict,
			Digest:  digestValue(request.ResponseFormat),
		}
		envelope.PreservedSegments = append(envelope.PreservedSegments, contextSegment(len(request.Messages), "response_format", "", SegmentKindSchema, request.ResponseFormat, false, false))
	}
	return envelope, validationError(issues)
}
