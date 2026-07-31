package contextconsensus

import (
	"fmt"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/relaykit/types"
)

func extractGemini(extractionRequest ExtractionRequest) (*ContextEnvelope, error) {
	var request dto.GeminiChatRequest
	if err := common.Unmarshal(extractionRequest.Body, &request); err != nil {
		return nil, fmt.Errorf("decode Gemini request: %w", err)
	}
	envelope := &ContextEnvelope{
		Protocol:           types.RelayFormatGemini,
		OriginalModel:      extractionRequest.OriginalModel,
		RequestedMaxOutput: request.GenerationConfig.MaxOutputTokens,
		ProviderBinding: ProtocolBinding{
			BindingLevel: BindingLevelNone,
			RelayFormat:  types.RelayFormatGemini,
		},
	}
	providerFileState, err := ExtractProviderFileState(extractionRequest)
	if err != nil {
		return envelope, fmt.Errorf("extract Gemini provider files: %w", err)
	}
	envelope.ProviderFileState = providerFileState
	applyProviderFileState(envelope)
	if request.SystemInstructions != nil {
		envelope.ImmutableInstructions = append(envelope.ImmutableInstructions, contextSegment(0, "systemInstruction", "system", SegmentKindInstruction, request.SystemInstructions, false, false))
	}
	if strings.TrimSpace(request.CachedContent) != "" {
		requireBinding(&envelope.ProviderBinding, BindingLevelCredential, "gemini_cached_content", request.CachedContent)
	}

	toolEvents := make([]ToolEvent, 0)
	for contentIndex, content := range request.Contents {
		path := fmt.Sprintf("contents[%d]", contentIndex)
		hasToolCall := false
		hasToolResult := false
		hasMedia := false
		providerBoundContent := false
		for _, part := range content.Parts {
			sequence := len(toolEvents)
			if part.FunctionCall != nil {
				hasToolCall = true
				toolEvents = append(toolEvents, ToolEvent{
					Kind:               ToolEventCall,
					Protocol:           types.RelayFormatGemini,
					Sequence:           sequence,
					ParallelGroup:      contentIndex,
					FunctionName:       part.FunctionCall.FunctionName,
					PayloadDigest:      digestValue(part.FunctionCall.Arguments),
					OpaqueStatePresent: rawJSONPresent(part.ThoughtSignature),
					MatchByName:        true,
				})
			}
			if part.FunctionResponse != nil {
				hasToolResult = true
				toolEvents = append(toolEvents, ToolEvent{
					Kind:               ToolEventResult,
					Protocol:           types.RelayFormatGemini,
					Sequence:           sequence,
					ParallelGroup:      contentIndex,
					FunctionName:       part.FunctionResponse.Name,
					PayloadDigest:      digestValue(part.FunctionResponse.Response),
					OpaqueStatePresent: rawJSONPresent(part.ThoughtSignature),
					MatchByName:        true,
				})
			}
			if rawJSONPresent(part.ThoughtSignature) {
				providerBoundContent = true
				requireBinding(&envelope.ProviderBinding, BindingLevelCredential, "gemini_thought_signature", rawJSONString(part.ThoughtSignature))
			}
			if part.InlineData != nil {
				hasMedia = true
				incrementGeminiMedia(part.InlineData.MimeType, &envelope.MediaState)
			}
			if part.FileData != nil {
				hasMedia = true
				providerBoundContent = true
				envelope.MediaState.FileCount++
			}
		}
		providerBoundContent = providerBoundContent || envelope.ProviderFileState.BindingLevelAtSequence(contentIndex) != BindingLevelNone
		preserved := contentIndex == len(request.Contents)-1 || hasToolCall || hasToolResult || hasMedia || providerBoundContent
		kind := SegmentKindMessage
		if hasToolCall {
			kind = SegmentKindToolCall
		} else if hasToolResult {
			kind = SegmentKindToolResult
		} else if hasMedia {
			kind = SegmentKindMedia
		}
		appendSegment(envelope, contextSegment(contentIndex, path, content.Role, kind, content, !preserved, providerBoundContent), false, preserved)
	}

	graph, issues := ValidateToolGraph(toolEvents)
	if rawJSONPresent(request.Tools) {
		graph.SchemaDigest = digestBytes(request.Tools)
	}
	if len(graph.AmbiguousFunctionNames) > 0 {
		requireBinding(&envelope.ProviderBinding, BindingLevelProvider, "gemini_ambiguous_parallel_function_calls", "")
	}
	envelope.ToolState = graph
	if request.GenerationConfig.ResponseSchema != nil || rawJSONPresent(request.GenerationConfig.ResponseJsonSchema) {
		schemaValue := request.GenerationConfig.ResponseSchema
		schemaDigest := digestValue(schemaValue)
		if rawJSONPresent(request.GenerationConfig.ResponseJsonSchema) {
			schemaValue = request.GenerationConfig.ResponseJsonSchema
			schemaDigest = digestBytes(request.GenerationConfig.ResponseJsonSchema)
		}
		envelope.SchemaState = SchemaState{Present: true, Digest: schemaDigest}
		envelope.PreservedSegments = append(envelope.PreservedSegments, contextSegment(len(request.Contents)+1, "generationConfig.responseSchema", "", SegmentKindSchema, schemaValue, false, false))
	}
	return envelope, validationError(issues)
}

func incrementGeminiMedia(mimeType string, state *MediaState) {
	switch {
	case strings.HasPrefix(mimeType, "image/"):
		state.ImageCount++
	case strings.HasPrefix(mimeType, "audio/"):
		state.AudioCount++
	case strings.HasPrefix(mimeType, "video/"):
		state.VideoCount++
	default:
		state.FileCount++
	}
}
