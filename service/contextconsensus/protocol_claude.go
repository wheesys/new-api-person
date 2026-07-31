package contextconsensus

import (
	"fmt"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/relaykit/types"
)

func extractClaudeMessages(extractionRequest ExtractionRequest) (*ContextEnvelope, error) {
	var request dto.ClaudeRequest
	if err := common.Unmarshal(extractionRequest.Body, &request); err != nil {
		return nil, fmt.Errorf("decode Claude Messages request: %w", err)
	}
	envelope := &ContextEnvelope{
		Protocol:           types.RelayFormatClaude,
		OriginalModel:      request.Model,
		RequestedMaxOutput: request.MaxTokens,
		ProviderBinding: ProtocolBinding{
			BindingLevel: BindingLevelNone,
			RelayFormat:  types.RelayFormatClaude,
		},
	}
	if request.MaxTokensToSample != nil {
		envelope.RequestedMaxOutput = request.MaxTokensToSample
	}
	if request.System != nil {
		envelope.ImmutableInstructions = append(envelope.ImmutableInstructions, contextSegment(0, "system", "system", SegmentKindInstruction, request.System, false, false))
	}
	if rawJSONPresent(request.Container) {
		requireBinding(&envelope.ProviderBinding, BindingLevelCredential, "claude_container", rawJSONString(request.Container))
	}
	if rawJSONPresent(request.ContextManagement) {
		requireBinding(&envelope.ProviderBinding, BindingLevelCredential, "claude_context_management", rawJSONString(request.ContextManagement))
	}
	if rawJSONPresent(request.McpServers) {
		requireBinding(&envelope.ProviderBinding, BindingLevelCredential, "claude_mcp_servers", "")
	}

	toolEvents := make([]ToolEvent, 0)
	for messageIndex, message := range request.Messages {
		path := fmt.Sprintf("messages[%d]", messageIndex)
		role := strings.ToLower(strings.TrimSpace(message.Role))
		parts := make([]dto.ClaudeMediaMessage, 0)
		if _, isString := message.Content.(string); !isString {
			parsedParts, err := common.Any2Type[[]dto.ClaudeMediaMessage](message.Content)
			if err != nil {
				return envelope, fmt.Errorf("decode %s.content: %w", path, err)
			}
			parts = parsedParts
		}
		hasToolCall := false
		hasToolResult := false
		hasMedia := false
		providerBoundMessage := false
		for _, part := range parts {
			sequence := len(toolEvents)
			partType := strings.ToLower(strings.TrimSpace(part.Type))
			if strings.Contains(partType, "server_tool") || strings.Contains(partType, "mcp") || strings.Contains(partType, "web_search") || strings.Contains(partType, "code_execution") || strings.Contains(partType, "computer") {
				providerBoundMessage = true
				requireBinding(&envelope.ProviderBinding, BindingLevelCredential, "claude_server_tool_state", "")
			}
			switch part.Type {
			case "tool_use":
				hasToolCall = true
				toolEvents = append(toolEvents, ToolEvent{
					Kind:               ToolEventCall,
					Protocol:           types.RelayFormatClaude,
					Sequence:           sequence,
					ParallelGroup:      messageIndex,
					CallID:             part.Id,
					FunctionName:       part.Name,
					PayloadDigest:      digestValue(part.Input),
					OpaqueStatePresent: part.Signature != "",
				})
			case "tool_result":
				hasToolResult = true
				toolEvents = append(toolEvents, ToolEvent{
					Kind:               ToolEventResult,
					Protocol:           types.RelayFormatClaude,
					Sequence:           sequence,
					ParallelGroup:      messageIndex,
					CallID:             part.ToolUseId,
					PayloadDigest:      digestValue(part.Content),
					OpaqueStatePresent: part.Signature != "",
				})
			case "image", "document":
				hasMedia = true
				if part.Type == "image" {
					envelope.MediaState.ImageCount++
				} else {
					envelope.MediaState.FileCount++
				}
				if part.Source != nil && part.Source.Type != "base64" && part.Source.Type != "url" {
					providerBoundMessage = true
					envelope.MediaState.ProviderBoundCount++
				}
			}
			if part.Signature != "" {
				providerBoundMessage = true
				requireBinding(&envelope.ProviderBinding, BindingLevelCredential, "claude_thinking_signature", part.Signature)
			}
		}
		if providerBoundMessage {
			requireBinding(&envelope.ProviderBinding, BindingLevelProvider, "claude_provider_bound_message", "")
		}
		preserved := messageIndex == len(request.Messages)-1 || hasToolCall || hasToolResult || hasMedia || providerBoundMessage
		kind := SegmentKindMessage
		if hasToolCall {
			kind = SegmentKindToolCall
		} else if hasToolResult {
			kind = SegmentKindToolResult
		} else if hasMedia {
			kind = SegmentKindMedia
		}
		appendSegment(envelope, contextSegment(messageIndex, path, role, kind, message, !preserved, providerBoundMessage), false, preserved)
	}

	graph, issues := ValidateToolGraph(toolEvents)
	if request.Tools != nil {
		graph.SchemaDigest = digestValue(request.Tools)
		tools, err := common.Any2Type[[]map[string]any](request.Tools)
		if err == nil {
			for _, tool := range tools {
				toolType := strings.ToLower(strings.TrimSpace(fmt.Sprint(tool["type"])))
				if toolType != "" && toolType != "custom" {
					requireBinding(&envelope.ProviderBinding, BindingLevelProvider, "claude_server_tool_definition", "")
				}
			}
		}
	}
	envelope.ToolState = graph
	outputSchema := request.OutputFormat
	outputSchemaPath := "output_format"
	outputSchemaStrict := false
	if rawJSONPresent(outputSchema) {
		var outputFormatValue any
		if err := common.Unmarshal(outputSchema, &outputFormatValue); err == nil {
			_, outputSchemaStrict = containsJSONSchema(outputFormatValue)
		}
	} else if rawJSONPresent(request.OutputConfig) {
		var outputConfigValue any
		if err := common.Unmarshal(request.OutputConfig, &outputConfigValue); err == nil {
			if present, strict := containsJSONSchema(outputConfigValue); present {
				outputSchema = request.OutputConfig
				outputSchemaPath = "output_config"
				outputSchemaStrict = strict
			}
		}
	}
	if rawJSONPresent(outputSchema) {
		envelope.SchemaState = SchemaState{Present: true, Strict: outputSchemaStrict, Digest: digestBytes(outputSchema)}
		envelope.PreservedSegments = append(envelope.PreservedSegments, rawContextSegment(len(request.Messages)+1, outputSchemaPath, "", SegmentKindSchema, outputSchema, false, false))
	}
	return envelope, validationError(issues)
}
