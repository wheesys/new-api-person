package contextconsensus

import (
	"fmt"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/relaykit/types"
)

func extractOpenAIResponses(extractionRequest ExtractionRequest) (*ContextEnvelope, error) {
	var request dto.OpenAIResponsesRequest
	if err := common.Unmarshal(extractionRequest.Body, &request); err != nil {
		return nil, fmt.Errorf("decode OpenAI Responses request: %w", err)
	}
	envelope := &ContextEnvelope{
		Protocol:           types.RelayFormatOpenAIResponses,
		OriginalModel:      request.Model,
		RequestedMaxOutput: request.MaxOutputTokens,
		ProviderBinding: ProtocolBinding{
			BindingLevel: BindingLevelNone,
			RelayFormat:  types.RelayFormatOpenAIResponses,
		},
	}
	if rawJSONPresent(request.Instructions) {
		envelope.ImmutableInstructions = append(envelope.ImmutableInstructions, rawContextSegment(0, "instructions", "developer", SegmentKindInstruction, request.Instructions, false, false))
	}
	if strings.TrimSpace(request.PreviousResponseID) != "" {
		requireBinding(&envelope.ProviderBinding, BindingLevelCredential, "responses_previous_response_id", request.PreviousResponseID)
	}
	if rawJSONPresent(request.Conversation) {
		requireBinding(&envelope.ProviderBinding, BindingLevelCredential, "responses_conversation", rawJSONString(request.Conversation))
	}
	if rawJSONPresent(request.ContextManagement) {
		requireBinding(&envelope.ProviderBinding, BindingLevelCredential, "responses_context_management", rawJSONString(request.ContextManagement))
	}
	if rawJSONPresent(request.Prompt) {
		requireBinding(&envelope.ProviderBinding, BindingLevelCredential, "responses_prompt", rawJSONString(request.Prompt))
	}

	toolEvents := make([]ToolEvent, 0)
	inputItems := make([]map[string]any, 0)
	if rawJSONPresent(request.Input) {
		switch common.GetJsonType(request.Input) {
		case "string":
			envelope.PreservedSegments = append(envelope.PreservedSegments, rawContextSegment(0, "input", "user", SegmentKindMessage, request.Input, false, false))
		case "array":
			if err := common.Unmarshal(request.Input, &inputItems); err != nil {
				return envelope, fmt.Errorf("decode Responses input: %w", err)
			}
		default:
			return envelope, fmt.Errorf("unsupported Responses input type %q", common.GetJsonType(request.Input))
		}
	}
	lastUserIndex := -1
	for index, item := range inputItems {
		if strings.EqualFold(strings.TrimSpace(fmt.Sprint(item["role"])), "user") {
			lastUserIndex = index
		}
	}
	for itemIndex, item := range inputItems {
		path := fmt.Sprintf("input[%d]", itemIndex)
		itemType := strings.ToLower(strings.TrimSpace(fmt.Sprint(item["type"])))
		role := strings.ToLower(strings.TrimSpace(fmt.Sprint(item["role"])))
		isTool := false
		switch itemType {
		case "function_call", "custom_tool_call":
			isTool = true
			arguments := item["arguments"]
			if itemType == "custom_tool_call" {
				arguments = item["input"]
			}
			callID, _ := item["call_id"].(string)
			functionName, _ := item["name"].(string)
			toolEvents = append(toolEvents, ToolEvent{
				Kind:     ToolEventCall,
				Protocol: types.RelayFormatOpenAIResponses,
				Sequence: itemIndex,
				// Responses has no call container spanning adjacent input items.
				ParallelGroup: itemIndex,
				CallID:        callID,
				FunctionName:  functionName,
				PayloadDigest: digestValue(arguments),
			})
		case "function_call_output", "custom_tool_call_output":
			isTool = true
			callID, _ := item["call_id"].(string)
			toolEvents = append(toolEvents, ToolEvent{
				Kind:          ToolEventResult,
				Protocol:      types.RelayFormatOpenAIResponses,
				Sequence:      itemIndex,
				ParallelGroup: itemIndex,
				CallID:        callID,
				PayloadDigest: digestValue(item["output"]),
			})
		}
		if strings.HasPrefix(itemType, "mcp_") || strings.HasPrefix(itemType, "computer_") || strings.HasPrefix(itemType, "web_search_") || strings.HasPrefix(itemType, "file_search_") {
			requireBinding(&envelope.ProviderBinding, BindingLevelCredential, "responses_hosted_tool_state", "")
		}
		hasMedia, providerBoundMedia := inspectGenericMedia(item, &envelope.MediaState)
		if providerBoundMedia {
			requireBinding(&envelope.ProviderBinding, BindingLevelCredential, "provider_file_reference", "")
		}
		immutable := role == "system" || role == "developer"
		preserved := itemIndex == lastUserIndex || isTool || hasMedia || itemType == "reasoning"
		kind := SegmentKindMessage
		if immutable {
			kind = SegmentKindInstruction
		} else if strings.HasSuffix(itemType, "_output") {
			kind = SegmentKindToolResult
		} else if isTool {
			kind = SegmentKindToolCall
		} else if hasMedia {
			kind = SegmentKindMedia
		}
		appendSegment(envelope, contextSegment(itemIndex, path, role, kind, item, !immutable && !preserved, providerBoundMedia), immutable, preserved)
	}

	graph, issues := ValidateToolGraph(toolEvents)
	if rawJSONPresent(request.Tools) {
		graph.SchemaDigest = digestBytes(request.Tools)
		var tools []map[string]any
		if err := common.Unmarshal(request.Tools, &tools); err == nil {
			for _, tool := range tools {
				toolType := strings.ToLower(strings.TrimSpace(fmt.Sprint(tool["type"])))
				if toolType != "" && toolType != "function" && toolType != "custom" {
					requireBinding(&envelope.ProviderBinding, BindingLevelProvider, "responses_hosted_tool_definition", "")
				}
			}
		}
	}
	envelope.ToolState = graph
	if rawJSONPresent(request.Text) {
		var textConfiguration any
		if err := common.Unmarshal(request.Text, &textConfiguration); err == nil {
			present, strict := containsJSONSchema(textConfiguration)
			if present {
				envelope.SchemaState = SchemaState{Present: true, Strict: strict, Digest: digestBytes(request.Text)}
				envelope.PreservedSegments = append(envelope.PreservedSegments, rawContextSegment(len(inputItems)+1, "text", "", SegmentKindSchema, request.Text, false, false))
			}
		}
	}
	return envelope, validationError(issues)
}
