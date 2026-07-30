package contextconsensus

import (
	"errors"
	"fmt"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/relaykit/types"
)

type ToolCompactionPlanRequest struct {
	CompactionPlanRequest
	PolicyProvider ToolSanitizationPolicyProvider
}

type chatCompactionTurn struct {
	segments []ContextSegment
}

func BuildToolCompactionPlanV2(request ToolCompactionPlanRequest) (CompactionPlan, error) {
	if err := validateCompactionPlanRequest(request.CompactionPlanRequest); err != nil {
		return CompactionPlan{}, err
	}
	if request.Protocol != types.RelayFormatOpenAI {
		return CompactionPlan{}, fmt.Errorf("tool compaction v2 only supports OpenAI Chat Completions")
	}
	if !request.Policy.AllowToolResultCompaction {
		return CompactionPlan{}, fmt.Errorf("tool result compaction is disabled")
	}
	if request.Envelope.MediaState.TotalCount() > 0 || request.Envelope.MediaState.InlineCount > 0 {
		return CompactionPlan{}, fmt.Errorf("media context cannot be compacted")
	}
	assessment := AssessSingleSerialToolCompaction(request.Envelope)
	if !assessment.ReadyForSanitization || assessment.Evidence == nil {
		return CompactionPlan{}, fmt.Errorf("tool context is not eligible for compaction: %s", strings.Join(assessment.ReasonCodes, ","))
	}
	var chatRequest dto.GeneralOpenAIRequest
	if err := common.Unmarshal(request.Body, &chatRequest); err != nil {
		return CompactionPlan{}, fmt.Errorf("decode tool compaction request: %w", err)
	}
	turns, currentUser, toolTurnIndex, err := extractChatCompactionTurns(chatRequest.Messages, *assessment.Evidence)
	if err != nil {
		return CompactionPlan{}, err
	}
	coveredTurnCount := len(turns) - request.Policy.PreservedRecentTurns
	if coveredTurnCount <= 0 {
		return CompactionPlan{}, fmt.Errorf("no safe early complete turns are available for compaction")
	}

	summaryVersion := ConsensusSummaryVersion
	var projection *ToolResultSanitizationOutput
	if toolTurnIndex < coveredTurnCount {
		if request.PolicyProvider == nil {
			coveredTurnCount = toolTurnIndex
		} else {
			sanitized, sanitizeErr := request.PolicyProvider.Sanitize(*assessment.Evidence, chatRequest.Messages[assessment.Evidence.ResultSequence].Content)
			if errors.Is(sanitizeErr, ErrToolSanitizationPolicyNotFound) {
				coveredTurnCount = toolTurnIndex
			} else if sanitizeErr != nil {
				return CompactionPlan{}, sanitizeErr
			} else if validateErr := sanitized.Validate(); validateErr != nil {
				return CompactionPlan{}, validateErr
			} else {
				summaryVersion = ConsensusSummaryVersionV2
				projection = &sanitized
			}
		}
	}
	if coveredTurnCount <= 0 {
		return CompactionPlan{}, fmt.Errorf("no safe complete turns precede the unregistered tool turn")
	}

	coveredSegments := make([]ContextSegment, 0, coveredTurnCount*2+2)
	coveredRanges := make([]SummarySourceRange, 0, coveredTurnCount)
	allHistorySegments := make([]ContextSegment, 0, len(chatRequest.Messages))
	for _, turn := range turns {
		allHistorySegments = append(allHistorySegments, turn.segments...)
	}
	allHistorySegments = append(allHistorySegments, currentUser)
	for _, turn := range turns[:coveredTurnCount] {
		coveredSegments = append(coveredSegments, turn.segments...)
		coveredRanges = append(coveredRanges, summarySourceRange(turn.segments))
	}

	coveredPaths := make(map[string]struct{}, len(coveredSegments))
	for _, segment := range coveredSegments {
		coveredPaths[segment.Path] = struct{}{}
	}
	preservedSegments := make([]ContextSegment, 0, len(request.Envelope.PreservedSegments)+len(allHistorySegments))
	for _, segment := range request.Envelope.PreservedSegments {
		if _, covered := coveredPaths[segment.Path]; !covered {
			preservedSegments = appendUniqueSegment(preservedSegments, segment)
		}
	}
	for _, segment := range allHistorySegments {
		if _, covered := coveredPaths[segment.Path]; !covered {
			preservedSegments = appendUniqueSegment(preservedSegments, segment)
		}
	}

	plan := CompactionPlan{
		SummaryVersion:       summaryVersion,
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
		ToolContextPresent:   true,
		preservedRecentTurns: request.Policy.PreservedRecentTurns,
		toolPolicyProvider:   request.PolicyProvider,
	}
	if projection != nil {
		toolSegments := turns[toolTurnIndex].segments
		toolRange := summarySourceRange(toolSegments)
		plan.ToolAtomicRange = &toolRange
		plan.ToolCallSequence = assessment.Evidence.CallSequence
		plan.ToolResultSequence = assessment.Evidence.ResultSequence
		plan.ToolFinalSequence = assessment.Evidence.ResultSequence + 1
		plan.ToolProjectionDigest = projection.ProjectionDigest()
		plan.toolProjection = projection
	}
	plan.integrityDigest = digestValue(plan)
	return plan, nil
}

func extractChatCompactionTurns(messages []dto.Message, evidence ToolCompactionStructuralEvidence) ([]chatCompactionTurn, ContextSegment, int, error) {
	turns := make([]chatCompactionTurn, 0, len(messages)/2)
	toolTurnIndex := -1
	for sequence := 0; sequence < len(messages); {
		message := messages[sequence]
		role := normalizedHistoryRole(message.Role)
		if role == "instruction" {
			sequence++
			continue
		}
		if role != "user" || !isPureTextContent(message.Content) || rawJSONPresent(message.ToolCalls) {
			return nil, ContextSegment{}, -1, fmt.Errorf("messages[%d] is not a supported user turn", sequence)
		}
		userSegment := contextSegment(sequence, fmt.Sprintf("messages[%d]", sequence), "user", SegmentKindMessage, message, true, false)
		if sequence == len(messages)-1 {
			if toolTurnIndex < 0 {
				return nil, ContextSegment{}, -1, fmt.Errorf("tool compaction requires one completed tool turn")
			}
			return turns, userSegment, toolTurnIndex, nil
		}
		if sequence+1 >= len(messages) {
			return nil, ContextSegment{}, -1, fmt.Errorf("history contains an incomplete user turn")
		}

		if sequence+1 == evidence.CallSequence {
			if toolTurnIndex >= 0 || evidence.ResultSequence != sequence+2 || sequence+3 >= len(messages) {
				return nil, ContextSegment{}, -1, fmt.Errorf("tool turn is not one contiguous serial exchange")
			}
			callMessage := messages[sequence+1]
			resultMessage := messages[sequence+2]
			finalMessage := messages[sequence+3]
			callContent, callContentIsString := callMessage.Content.(string)
			callContentPresent := callMessage.Content != nil && (!callContentIsString || callContent != "")
			if normalizedHistoryRole(callMessage.Role) != "assistant" || len(callMessage.ParseToolCalls()) != 1 ||
				callContentPresent || hasUnsupportedToolTurnMessageMetadata(callMessage, true, false) ||
				strings.ToLower(strings.TrimSpace(resultMessage.Role)) != "tool" || resultMessage.ToolCallId == "" ||
				hasUnsupportedToolTurnMessageMetadata(resultMessage, false, true) ||
				normalizedHistoryRole(finalMessage.Role) != "assistant" || !isPureTextContent(finalMessage.Content) ||
				rawJSONPresent(finalMessage.ToolCalls) || hasUnsupportedToolTurnMessageMetadata(finalMessage, false, false) {
				return nil, ContextSegment{}, -1, fmt.Errorf("tool turn does not match the required user/call/result/final shape")
			}
			toolTurnIndex = len(turns)
			turns = append(turns, chatCompactionTurn{segments: []ContextSegment{
				userSegment,
				contextSegment(sequence+1, fmt.Sprintf("messages[%d]", sequence+1), "assistant", SegmentKindToolCall, callMessage, false, false),
				contextSegment(sequence+2, fmt.Sprintf("messages[%d]", sequence+2), "tool", SegmentKindToolResult, resultMessage, false, false),
				contextSegment(sequence+3, fmt.Sprintf("messages[%d]", sequence+3), "assistant", SegmentKindMessage, finalMessage, true, false),
			}})
			sequence += 4
			continue
		}

		assistantMessage := messages[sequence+1]
		if normalizedHistoryRole(assistantMessage.Role) != "assistant" || !isPureTextContent(assistantMessage.Content) || rawJSONPresent(assistantMessage.ToolCalls) {
			return nil, ContextSegment{}, -1, fmt.Errorf("history turn at sequence %d is not a complete pure text turn", sequence)
		}
		turns = append(turns, chatCompactionTurn{segments: []ContextSegment{
			userSegment,
			contextSegment(sequence+1, fmt.Sprintf("messages[%d]", sequence+1), "assistant", SegmentKindMessage, assistantMessage, true, false),
		}})
		sequence += 2
	}
	return nil, ContextSegment{}, -1, fmt.Errorf("current user turn must be the final history segment")
}

func hasUnsupportedToolTurnMessageMetadata(message dto.Message, allowToolCalls, allowToolCallID bool) bool {
	return message.Name != nil || message.Prefix != nil || message.ReasoningContent != nil || message.Reasoning != nil ||
		(!allowToolCalls && rawJSONPresent(message.ToolCalls)) || (!allowToolCallID && message.ToolCallId != "")
}
