package contextconsensus

import (
	"errors"
	"fmt"
	"sort"
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

type chatCompactionToolGroup struct {
	resultContents []any
	finalSequence  int
}

const (
	maximumToolCompactionProjectionBytes = 16 * 1024
	maximumToolCompactionFacts           = 32
)

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
	assessment := AssessToolCompactionGroup(request.Envelope)
	if !assessment.ReadyForSanitization || assessment.Evidence == nil {
		return CompactionPlan{}, fmt.Errorf("tool context is not eligible for compaction: %s", strings.Join(assessment.ReasonCodes, ","))
	}
	var chatRequest dto.GeneralOpenAIRequest
	if err := common.Unmarshal(request.Body, &chatRequest); err != nil {
		return CompactionPlan{}, fmt.Errorf("decode tool compaction request: %w", err)
	}
	turns, currentUser, toolTurnIndex, toolGroup, err := extractChatCompactionGroupTurns(chatRequest.Messages, *assessment.Evidence)
	if err != nil {
		return CompactionPlan{}, err
	}
	coveredTurnCount := len(turns) - request.Policy.PreservedRecentTurns
	if coveredTurnCount <= 0 {
		return CompactionPlan{}, fmt.Errorf("no safe early complete turns are available for compaction")
	}

	summaryVersion := ConsensusSummaryVersion
	var projections []ToolResultSanitizationOutput
	if toolTurnIndex < coveredTurnCount {
		if request.PolicyProvider == nil {
			coveredTurnCount = toolTurnIndex
		} else {
			projections = make([]ToolResultSanitizationOutput, 0, len(assessment.Evidence.Exchanges))
			projectionBytes := 0
			factCount := 0
			missingPolicy := false
			for exchangeIndex, evidence := range assessment.Evidence.Exchanges {
				sanitized, sanitizeErr := request.PolicyProvider.Sanitize(evidence, toolGroup.resultContents[exchangeIndex])
				if errors.Is(sanitizeErr, ErrToolSanitizationPolicyNotFound) {
					missingPolicy = true
					break
				}
				if sanitizeErr != nil {
					return CompactionPlan{}, sanitizeErr
				}
				if validateErr := sanitized.Validate(); validateErr != nil {
					return CompactionPlan{}, validateErr
				}
				encodedProjection, encodeErr := common.Marshal(sanitized)
				if encodeErr != nil {
					return CompactionPlan{}, encodeErr
				}
				projectionBytes += len(encodedProjection)
				factCount += len(sanitized.Fields())
				if projectionBytes > maximumToolCompactionProjectionBytes || factCount > maximumToolCompactionFacts {
					return CompactionPlan{}, ErrToolSanitizationLimitExceeded
				}
				projections = append(projections, sanitized)
			}
			if missingPolicy {
				coveredTurnCount = toolTurnIndex
				projections = nil
			} else {
				summaryVersion = ConsensusSummaryVersionV2
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
	if len(projections) > 0 {
		toolSegments := turns[toolTurnIndex].segments
		toolRange := summarySourceRange(toolSegments)
		plan.ToolAtomicRange = &toolRange
		plan.ToolHiddenSequences = append([]int{assessment.Evidence.CallSequence}, assessment.Evidence.ResultSequences...)
		sort.Ints(plan.ToolHiddenSequences[1:])
		plan.ToolHiddenSequences = append(plan.ToolHiddenSequences, toolGroup.finalSequence)
		plan.ToolProjectionDigests = make([]string, len(projections))
		plan.toolProjections = append([]ToolResultSanitizationOutput(nil), projections...)
		for index := range projections {
			plan.ToolProjectionDigests[index] = projections[index].ProjectionDigest()
		}
		plan.ToolCallSequence = assessment.Evidence.CallSequence
		plan.ToolFinalSequence = toolGroup.finalSequence
		if len(projections) == 1 {
			plan.ToolResultSequence = assessment.Evidence.ResultSequences[0]
			plan.ToolProjectionDigest = projections[0].ProjectionDigest()
			projection := projections[0]
			plan.toolProjection = &projection
		}
	}
	plan.integrityDigest = digestValue(plan)
	return plan, nil
}

func extractChatCompactionTurns(messages []dto.Message, evidence ToolCompactionStructuralEvidence) ([]chatCompactionTurn, ContextSegment, int, error) {
	groupEvidence := ToolCompactionGroupEvidence{
		Protocol: evidence.Protocol, GroupIndex: 0, CallSequence: evidence.CallSequence,
		ResultSequences: []int{evidence.ResultSequence}, Status: evidence.Status,
		Exchanges: []ToolCompactionStructuralEvidence{evidence},
	}
	groupEvidence.integrityDigest = digestValue(groupEvidence)
	turns, currentUser, toolTurnIndex, _, err := extractChatCompactionGroupTurns(messages, groupEvidence)
	return turns, currentUser, toolTurnIndex, err
}

func extractChatCompactionGroupTurns(messages []dto.Message, evidence ToolCompactionGroupEvidence) ([]chatCompactionTurn, ContextSegment, int, chatCompactionToolGroup, error) {
	if err := evidence.Validate(); err != nil {
		return nil, ContextSegment{}, -1, chatCompactionToolGroup{}, err
	}
	turns := make([]chatCompactionTurn, 0, len(messages)/2)
	toolTurnIndex := -1
	toolGroup := chatCompactionToolGroup{}
	for sequence := 0; sequence < len(messages); {
		message := messages[sequence]
		role := normalizedHistoryRole(message.Role)
		if role == "instruction" {
			sequence++
			continue
		}
		if role != "user" || !isPureTextContent(message.Content) || rawJSONPresent(message.ToolCalls) {
			return nil, ContextSegment{}, -1, chatCompactionToolGroup{}, fmt.Errorf("messages[%d] is not a supported user turn", sequence)
		}
		userSegment := contextSegment(sequence, fmt.Sprintf("messages[%d]", sequence), "user", SegmentKindMessage, message, true, false)
		if sequence == len(messages)-1 {
			if toolTurnIndex < 0 {
				return nil, ContextSegment{}, -1, chatCompactionToolGroup{}, fmt.Errorf("tool compaction requires one completed tool turn")
			}
			return turns, userSegment, toolTurnIndex, toolGroup, nil
		}
		if sequence+1 >= len(messages) {
			return nil, ContextSegment{}, -1, chatCompactionToolGroup{}, fmt.Errorf("history contains an incomplete user turn")
		}

		if sequence+1 == evidence.CallSequence {
			resultCount := len(evidence.Exchanges)
			finalSequence := sequence + resultCount + 2
			if toolTurnIndex >= 0 || finalSequence >= len(messages) {
				return nil, ContextSegment{}, -1, chatCompactionToolGroup{}, fmt.Errorf("tool turn is not one contiguous tool group")
			}
			callMessage := messages[sequence+1]
			finalMessage := messages[finalSequence]
			callContent, callContentIsString := callMessage.Content.(string)
			callContentPresent := callMessage.Content != nil && (!callContentIsString || callContent != "")
			toolCalls := callMessage.ParseToolCalls()
			if normalizedHistoryRole(callMessage.Role) != "assistant" || len(toolCalls) != resultCount ||
				callContentPresent || hasUnsupportedToolTurnMessageMetadata(callMessage, true, false) ||
				normalizedHistoryRole(finalMessage.Role) != "assistant" || !isPureTextContent(finalMessage.Content) ||
				rawJSONPresent(finalMessage.ToolCalls) || hasUnsupportedToolTurnMessageMetadata(finalMessage, false, false) {
				return nil, ContextSegment{}, -1, chatCompactionToolGroup{}, fmt.Errorf("tool turn does not match the required user/call/result/final shape")
			}
			resultContentsByIdentity := make(map[string]any, resultCount)
			resultSegments := make([]ContextSegment, 0, resultCount)
			for resultSequence := sequence + 2; resultSequence < finalSequence; resultSequence++ {
				resultMessage := messages[resultSequence]
				identityDigest := digestString(resultMessage.ToolCallId)
				if strings.ToLower(strings.TrimSpace(resultMessage.Role)) != "tool" || resultMessage.ToolCallId == "" ||
					hasUnsupportedToolTurnMessageMetadata(resultMessage, false, true) {
					return nil, ContextSegment{}, -1, chatCompactionToolGroup{}, fmt.Errorf("tool turn does not match the required user/call/result/final shape")
				}
				if _, duplicate := resultContentsByIdentity[identityDigest]; duplicate {
					return nil, ContextSegment{}, -1, chatCompactionToolGroup{}, fmt.Errorf("tool result identity is duplicated")
				}
				resultContentsByIdentity[identityDigest] = resultMessage.Content
				resultSegments = append(resultSegments, contextSegment(resultSequence, fmt.Sprintf("messages[%d]", resultSequence), "tool", SegmentKindToolResult, resultMessage, false, false))
			}
			resultContents := make([]any, resultCount)
			for callIndex, toolCall := range toolCalls {
				exchange := evidence.Exchanges[callIndex]
				if toolCall.Type != "function" || len(toolCall.Custom) > 0 || toolCall.Function.Description != "" || toolCall.Function.Parameters != nil ||
					digestString(toolCall.ID) != exchange.CallIdentityDigest || digestString(toolCall.Function.Name) != exchange.ToolIdentityDigest ||
					digestString(toolCall.Function.Arguments) != exchange.ArgumentsDigest {
					return nil, ContextSegment{}, -1, chatCompactionToolGroup{}, fmt.Errorf("tool call does not match sealed structural evidence")
				}
				resultContent, found := resultContentsByIdentity[exchange.CallIdentityDigest]
				if !found || digestValue(resultContent) != exchange.ResultDigest {
					return nil, ContextSegment{}, -1, chatCompactionToolGroup{}, fmt.Errorf("tool result does not match sealed structural evidence")
				}
				resultContents[callIndex] = resultContent
			}
			toolTurnIndex = len(turns)
			segments := []ContextSegment{
				userSegment,
				contextSegment(sequence+1, fmt.Sprintf("messages[%d]", sequence+1), "assistant", SegmentKindToolCall, callMessage, false, false),
			}
			segments = append(segments, resultSegments...)
			segments = append(segments, contextSegment(finalSequence, fmt.Sprintf("messages[%d]", finalSequence), "assistant", SegmentKindMessage, finalMessage, true, false))
			turns = append(turns, chatCompactionTurn{segments: segments})
			toolGroup = chatCompactionToolGroup{resultContents: resultContents, finalSequence: finalSequence}
			sequence = finalSequence + 1
			continue
		}

		assistantMessage := messages[sequence+1]
		if normalizedHistoryRole(assistantMessage.Role) != "assistant" || !isPureTextContent(assistantMessage.Content) || rawJSONPresent(assistantMessage.ToolCalls) {
			return nil, ContextSegment{}, -1, chatCompactionToolGroup{}, fmt.Errorf("history turn at sequence %d is not a complete pure text turn", sequence)
		}
		turns = append(turns, chatCompactionTurn{segments: []ContextSegment{
			userSegment,
			contextSegment(sequence+1, fmt.Sprintf("messages[%d]", sequence+1), "assistant", SegmentKindMessage, assistantMessage, true, false),
		}})
		sequence += 2
	}
	return nil, ContextSegment{}, -1, chatCompactionToolGroup{}, fmt.Errorf("current user turn must be the final history segment")
}

func hasUnsupportedToolTurnMessageMetadata(message dto.Message, allowToolCalls, allowToolCallID bool) bool {
	return message.Name != nil || message.Prefix != nil || message.ReasoningContent != nil || message.Reasoning != nil ||
		(!allowToolCalls && rawJSONPresent(message.ToolCalls)) || (!allowToolCallID && message.ToolCallId != "")
}
