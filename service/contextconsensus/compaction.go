package contextconsensus

import (
	"fmt"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/types"
)

type SummarySourceRange struct {
	StartSequence int    `json:"start_sequence"`
	EndSequence   int    `json:"end_sequence"`
	SourceDigest  string `json:"source_digest"`
}

type CompactionPlan struct {
	Protocol            types.RelayFormat    `json:"protocol"`
	SourceDigest        string               `json:"source_digest"`
	CoveredSegments     []ContextSegment     `json:"covered_segments"`
	CoveredRanges       []SummarySourceRange `json:"covered_ranges"`
	PreservedSegments   []ContextSegment     `json:"preserved_segments"`
	ImmutableSegments   []ContextSegment     `json:"immutable_segments"`
	OpenToolSegments    []ContextSegment     `json:"open_tool_segments,omitempty"`
	MediaSegments       []ContextSegment     `json:"media_segments,omitempty"`
	TargetInputTokens   int                  `json:"target_input_tokens"`
	MaxSummaryTokens    int                  `json:"max_summary_tokens"`
	PolicyVersion       string               `json:"policy_version"`
	SummaryInsertBefore int                  `json:"summary_insert_before"`
	// These fields make plans in-process capabilities rather than forgeable JSON payloads.
	preservedRecentTurns int
	integrityDigest      string
}

type CompactionPlanRequest struct {
	Protocol types.RelayFormat
	Body     []byte
	Envelope *ContextEnvelope
	Policy   CompactionPolicySnapshot
}

type textHistorySegment struct {
	ContextSegment
	Role string
}

func BuildCompactionPlan(request CompactionPlanRequest) (CompactionPlan, error) {
	if request.Envelope == nil {
		return CompactionPlan{}, fmt.Errorf("context envelope is required")
	}
	authorization := EvaluateCompactionAuthorization(request.Policy)
	if !authorization.Allowed {
		return CompactionPlan{}, fmt.Errorf("compaction is not authorized: %s", strings.Join(authorization.ReasonCodes, ","))
	}
	if strings.TrimSpace(request.Policy.PolicyVersion) == "" {
		return CompactionPlan{}, fmt.Errorf("compaction policy version is required")
	}
	if request.Policy.PreservedRecentTurns < 1 {
		return CompactionPlan{}, fmt.Errorf("preserved recent turns must be at least one")
	}
	if request.Policy.TargetInputTokens <= 0 {
		return CompactionPlan{}, fmt.Errorf("target input tokens must be greater than zero")
	}
	if request.Policy.MaxSummaryTokens <= 0 {
		return CompactionPlan{}, fmt.Errorf("max summary tokens must be greater than zero")
	}
	if request.Protocol != request.Envelope.Protocol {
		return CompactionPlan{}, fmt.Errorf("compaction protocol does not match context envelope")
	}
	if digestBytes(request.Body) != request.Envelope.SourceDigest {
		return CompactionPlan{}, fmt.Errorf("compaction source digest does not match request body")
	}
	if request.Envelope.ProviderBinding.Required() {
		return CompactionPlan{}, fmt.Errorf("provider-bound context cannot be compacted")
	}
	if len(request.Envelope.ToolState.Exchanges) > 0 || request.Envelope.ToolState.SchemaDigest != "" {
		return CompactionPlan{}, fmt.Errorf("tool context cannot be compacted")
	}
	if request.Envelope.MediaState.TotalCount() > 0 || request.Envelope.MediaState.InlineCount > 0 {
		return CompactionPlan{}, fmt.Errorf("media context cannot be compacted")
	}

	historySegments, err := extractTextHistory(request.Protocol, request.Body)
	if err != nil {
		return CompactionPlan{}, err
	}
	if len(historySegments) == 0 || historySegments[len(historySegments)-1].Role != "user" {
		return CompactionPlan{}, fmt.Errorf("current user turn must be the final history segment")
	}
	completedHistory := historySegments[:len(historySegments)-1]
	if len(completedHistory)%2 != 0 {
		return CompactionPlan{}, fmt.Errorf("history does not contain complete user and assistant turns")
	}
	for index := 0; index < len(completedHistory); index += 2 {
		if completedHistory[index].Role != "user" || completedHistory[index+1].Role != "assistant" {
			return CompactionPlan{}, fmt.Errorf("history turn at sequence %d is not a complete user and assistant turn", completedHistory[index].Sequence)
		}
		if index > 0 && completedHistory[index-1].Sequence+1 != completedHistory[index].Sequence {
			return CompactionPlan{}, fmt.Errorf("compressible history turns are not contiguous")
		}
		if completedHistory[index].Sequence+1 != completedHistory[index+1].Sequence {
			return CompactionPlan{}, fmt.Errorf("history turn at sequence %d is not contiguous", completedHistory[index].Sequence)
		}
	}

	completedTurnCount := len(completedHistory) / 2
	coveredTurnCount := completedTurnCount - request.Policy.PreservedRecentTurns
	if coveredTurnCount <= 0 {
		return CompactionPlan{}, fmt.Errorf("no safe early complete turns are available for compaction")
	}
	coveredHistory := completedHistory[:coveredTurnCount*2]
	coveredSegments := make([]ContextSegment, 0, len(coveredHistory))
	coveredRanges := make([]SummarySourceRange, 0, coveredTurnCount)
	for index := 0; index < len(coveredHistory); index += 2 {
		turnSegments := []ContextSegment{coveredHistory[index].ContextSegment, coveredHistory[index+1].ContextSegment}
		coveredSegments = append(coveredSegments, turnSegments...)
		coveredRanges = append(coveredRanges, summarySourceRange(turnSegments))
	}

	coveredPaths := make(map[string]struct{}, len(coveredSegments))
	for _, segment := range coveredSegments {
		coveredPaths[segment.Path] = struct{}{}
	}
	preservedSegments := make([]ContextSegment, 0, len(request.Envelope.PreservedSegments)+len(historySegments))
	for _, segment := range request.Envelope.PreservedSegments {
		if _, covered := coveredPaths[segment.Path]; !covered {
			preservedSegments = appendUniqueSegment(preservedSegments, segment)
		}
	}
	for _, historySegment := range historySegments {
		if _, covered := coveredPaths[historySegment.Path]; !covered {
			preservedSegments = appendUniqueSegment(preservedSegments, historySegment.ContextSegment)
		}
	}

	plan := CompactionPlan{
		Protocol:             request.Protocol,
		SourceDigest:         request.Envelope.SourceDigest,
		CoveredSegments:      coveredSegments,
		CoveredRanges:        coveredRanges,
		PreservedSegments:    preservedSegments,
		ImmutableSegments:    append([]ContextSegment(nil), request.Envelope.ImmutableInstructions...),
		TargetInputTokens:    request.Policy.TargetInputTokens,
		MaxSummaryTokens:     request.Policy.MaxSummaryTokens,
		PolicyVersion:        request.Policy.PolicyVersion,
		SummaryInsertBefore:  coveredSegments[0].Sequence,
		preservedRecentTurns: request.Policy.PreservedRecentTurns,
	}
	plan.integrityDigest = digestValue(plan)
	return plan, nil
}

func NewSummarySourceRange(plan CompactionPlan, startSequence, endSequence int) (SummarySourceRange, error) {
	segments := make([]ContextSegment, 0, endSequence-startSequence+1)
	for _, segment := range plan.CoveredSegments {
		if segment.Sequence >= startSequence && segment.Sequence <= endSequence {
			segments = append(segments, segment)
		}
	}
	if len(segments) == 0 || segments[0].Sequence != startSequence || segments[len(segments)-1].Sequence != endSequence {
		return SummarySourceRange{}, fmt.Errorf("source range is not fully covered by the compaction plan")
	}
	for index := 1; index < len(segments); index++ {
		if segments[index-1].Sequence+1 != segments[index].Sequence {
			return SummarySourceRange{}, fmt.Errorf("source range is not contiguous")
		}
	}
	return summarySourceRange(segments), nil
}

func summarySourceRange(segments []ContextSegment) SummarySourceRange {
	digests := make([]string, 0, len(segments))
	for _, segment := range segments {
		digests = append(digests, segment.Digest)
	}
	return SummarySourceRange{
		StartSequence: segments[0].Sequence,
		EndSequence:   segments[len(segments)-1].Sequence,
		SourceDigest:  digestValue(digests),
	}
}

func appendUniqueSegment(segments []ContextSegment, candidate ContextSegment) []ContextSegment {
	for _, segment := range segments {
		if segment.Path == candidate.Path {
			return segments
		}
	}
	return append(segments, candidate)
}

func extractTextHistory(protocol types.RelayFormat, body []byte) ([]textHistorySegment, error) {
	switch protocol {
	case types.RelayFormatOpenAI:
		var request dto.GeneralOpenAIRequest
		if err := common.Unmarshal(body, &request); err != nil {
			return nil, fmt.Errorf("decode chat completions request: %w", err)
		}
		segments := make([]textHistorySegment, 0, len(request.Messages))
		for index, message := range request.Messages {
			role := normalizedHistoryRole(message.Role)
			if role == "instruction" {
				continue
			}
			if role == "" || !isPureTextContent(message.Content) {
				return nil, fmt.Errorf("messages[%d] is not a supported pure text history segment", index)
			}
			segments = append(segments, textHistorySegment{
				ContextSegment: contextSegment(index, fmt.Sprintf("messages[%d]", index), strings.ToLower(message.Role), SegmentKindMessage, message, true, false),
				Role:           role,
			})
		}
		return segments, nil

	case types.RelayFormatOpenAIResponses:
		var request dto.OpenAIResponsesRequest
		if err := common.Unmarshal(body, &request); err != nil {
			return nil, fmt.Errorf("decode Responses request: %w", err)
		}
		if common.GetJsonType(request.Input) != "array" {
			return nil, fmt.Errorf("Responses compaction requires an input item array")
		}
		var items []map[string]any
		if err := common.Unmarshal(request.Input, &items); err != nil {
			return nil, fmt.Errorf("decode Responses input: %w", err)
		}
		segments := make([]textHistorySegment, 0, len(items))
		for index, item := range items {
			role := normalizedHistoryRole(fmt.Sprint(item["role"]))
			if role == "instruction" {
				continue
			}
			if role == "" || !isPureTextContent(item["content"]) {
				return nil, fmt.Errorf("input[%d] is not a supported pure text history segment", index)
			}
			segments = append(segments, textHistorySegment{
				ContextSegment: contextSegment(index, fmt.Sprintf("input[%d]", index), strings.ToLower(fmt.Sprint(item["role"])), SegmentKindMessage, item, true, false),
				Role:           role,
			})
		}
		return segments, nil

	case types.RelayFormatClaude:
		var request dto.ClaudeRequest
		if err := common.Unmarshal(body, &request); err != nil {
			return nil, fmt.Errorf("decode Claude request: %w", err)
		}
		segments := make([]textHistorySegment, 0, len(request.Messages))
		for index, message := range request.Messages {
			role := normalizedHistoryRole(message.Role)
			if role == "" || !isPureTextContent(message.Content) {
				return nil, fmt.Errorf("messages[%d] is not a supported pure text history segment", index)
			}
			segments = append(segments, textHistorySegment{
				ContextSegment: contextSegment(index, fmt.Sprintf("messages[%d]", index), strings.ToLower(message.Role), SegmentKindMessage, message, true, false),
				Role:           role,
			})
		}
		return segments, nil

	case types.RelayFormatGemini:
		var request dto.GeminiChatRequest
		if err := common.Unmarshal(body, &request); err != nil {
			return nil, fmt.Errorf("decode Gemini request: %w", err)
		}
		segments := make([]textHistorySegment, 0, len(request.Contents))
		for index, content := range request.Contents {
			role := normalizedHistoryRole(content.Role)
			if role == "" || !isPureGeminiTextContent(content) {
				return nil, fmt.Errorf("contents[%d] is not a supported pure text history segment", index)
			}
			segments = append(segments, textHistorySegment{
				ContextSegment: contextSegment(index, fmt.Sprintf("contents[%d]", index), strings.ToLower(content.Role), SegmentKindMessage, content, true, false),
				Role:           role,
			})
		}
		return segments, nil
	default:
		return nil, fmt.Errorf("unsupported compaction protocol %q", protocol)
	}
}

func normalizedHistoryRole(role string) string {
	switch strings.ToLower(strings.TrimSpace(role)) {
	case "system", "developer":
		return "instruction"
	case "user":
		return "user"
	case "assistant", "model":
		return "assistant"
	default:
		return ""
	}
}

func isPureTextContent(content any) bool {
	if _, isString := content.(string); isString {
		return true
	}
	parts, ok := content.([]any)
	if !ok || len(parts) == 0 {
		return false
	}
	for _, part := range parts {
		partMap, ok := part.(map[string]any)
		if !ok {
			return false
		}
		partType := strings.ToLower(strings.TrimSpace(fmt.Sprint(partMap["type"])))
		if partType != "text" && partType != "input_text" && partType != "output_text" {
			return false
		}
		if _, textPresent := partMap["text"].(string); !textPresent {
			return false
		}
	}
	return true
}

func isPureGeminiTextContent(content dto.GeminiChatContent) bool {
	if len(content.Parts) == 0 {
		return false
	}
	for _, part := range content.Parts {
		if part.Text == "" || part.Thought || part.InlineData != nil || part.FileData != nil || part.FunctionCall != nil || part.FunctionResponse != nil || rawJSONPresent(part.ThoughtSignature) || part.ExecutableCode != nil || part.CodeExecutionResult != nil {
			return false
		}
	}
	return true
}
