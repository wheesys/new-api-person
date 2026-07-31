package contextconsensus

import (
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestValidateToolGraphRejectsDuplicateCallsAndOrphanResults(t *testing.T) {
	graph, issues := ValidateToolGraph([]ToolEvent{
		{Kind: ToolEventCall, Protocol: types.RelayFormatOpenAI, Sequence: 0, CallID: "call-1", FunctionName: "lookup"},
		{Kind: ToolEventCall, Protocol: types.RelayFormatOpenAI, Sequence: 1, CallID: "call-1", FunctionName: "lookup"},
		{Kind: ToolEventResult, Protocol: types.RelayFormatOpenAI, Sequence: 2, CallID: "missing"},
	})

	assert.Len(t, graph.Exchanges, 1)
	assert.Equal(t, ToolExchangePending, graph.Exchanges[0].Status)
	issueCodes := make([]string, 0, len(issues))
	for _, issue := range issues {
		issueCodes = append(issueCodes, issue.Code)
	}
	assert.ElementsMatch(t, []string{"duplicate_tool_call_id", "orphan_tool_result", "missing_tool_result"}, issueCodes)
}

func TestValidateToolGraphMatchesDistinctGeminiFunctionsByName(t *testing.T) {
	graph, issues := ValidateToolGraph([]ToolEvent{
		{Kind: ToolEventCall, Protocol: types.RelayFormatGemini, Sequence: 0, FunctionName: "first", MatchByName: true},
		{Kind: ToolEventCall, Protocol: types.RelayFormatGemini, Sequence: 1, FunctionName: "second", MatchByName: true},
		{Kind: ToolEventResult, Protocol: types.RelayFormatGemini, Sequence: 2, FunctionName: "second", MatchByName: true},
		{Kind: ToolEventResult, Protocol: types.RelayFormatGemini, Sequence: 3, FunctionName: "first", MatchByName: true},
	})

	assert.Empty(t, issues)
	assert.Empty(t, graph.AmbiguousFunctionNames)
	assert.Len(t, graph.Exchanges, 2)
	require.Len(t, graph.Groups, 1)
	assert.Equal(t, ToolIdentityModeFunctionName, graph.Groups[0].IdentityMode)
	assert.Equal(t, ToolExchangeCompleted, graph.Groups[0].Status)
	assert.Equal(t, ToolExchangeCompleted, graph.Exchanges[0].Status)
	assert.Equal(t, ToolExchangeCompleted, graph.Exchanges[1].Status)
	require.NotNil(t, graph.Exchanges[0].ResultSequence)
	assert.Equal(t, 3, *graph.Exchanges[0].ResultSequence)
	require.NotNil(t, graph.Exchanges[1].ResultSequence)
	assert.Equal(t, 2, *graph.Exchanges[1].ResultSequence)
}

func TestValidateToolGraphBuildsCallDerivedGroupWithReverseStableIDResults(t *testing.T) {
	graph, issues := ValidateToolGraph([]ToolEvent{
		{Kind: ToolEventCall, Protocol: types.RelayFormatClaude, Sequence: 1000, ParallelGroup: 1, CallID: "call-sensitive-first", FunctionName: "lookup-sensitive-first"},
		{Kind: ToolEventCall, Protocol: types.RelayFormatClaude, Sequence: 1001, ParallelGroup: 1, CallID: "call-sensitive-second", FunctionName: "lookup-sensitive-second"},
		{Kind: ToolEventResult, Protocol: types.RelayFormatClaude, Sequence: 2000, ParallelGroup: 2, CallID: "call-sensitive-second"},
		{Kind: ToolEventResult, Protocol: types.RelayFormatClaude, Sequence: 2001, ParallelGroup: 2, CallID: "call-sensitive-first"},
	})

	assert.Empty(t, issues)
	require.Len(t, graph.Groups, 1)
	group := graph.Groups[0]
	assert.Equal(t, types.RelayFormat(types.RelayFormatClaude), group.Protocol)
	assert.Equal(t, ToolIdentityModeStableID, group.IdentityMode)
	assert.Equal(t, ToolExchangeCompleted, group.Status)
	assert.Equal(t, 1000, group.CallSequenceStart)
	assert.Equal(t, 1001, group.CallSequenceEnd)
	require.NotNil(t, group.ResultSequenceStart)
	assert.Equal(t, 2000, *group.ResultSequenceStart)
	require.NotNil(t, group.ResultSequenceEnd)
	assert.Equal(t, 2001, *group.ResultSequenceEnd)
	assert.Equal(t, []int{0, 1}, group.ExchangeIndexes)
	assert.Equal(t, 0, graph.Exchanges[0].GroupIndex)
	assert.Equal(t, 0, graph.Exchanges[1].GroupIndex)

	encodedGraph, err := common.Marshal(graph)
	require.NoError(t, err)
	encodedText := string(encodedGraph)
	assert.NotContains(t, encodedText, "call-sensitive")
	assert.NotContains(t, encodedText, "lookup-sensitive")
	assert.NotContains(t, encodedText, "ambiguous_function_names")
	assert.Contains(t, encodedText, `"identity_mode":"stable_id"`)
	assert.Contains(t, encodedText, `"group_index":0`)
	encodedEvent, err := common.Marshal(ToolEvent{CallID: "call-sensitive-event", FunctionName: "lookup-sensitive-event"})
	require.NoError(t, err)
	assert.NotContains(t, string(encodedEvent), "sensitive")
}

func TestValidateToolGraphOrdersGroupExchangeIndexesByCallSequence(t *testing.T) {
	graph, issues := ValidateToolGraph([]ToolEvent{
		{Kind: ToolEventCall, Protocol: types.RelayFormatOpenAI, Sequence: 11, ParallelGroup: 10, CallID: "call-later"},
		{Kind: ToolEventCall, Protocol: types.RelayFormatOpenAI, Sequence: 10, ParallelGroup: 10, CallID: "call-earlier"},
		{Kind: ToolEventResult, Protocol: types.RelayFormatOpenAI, Sequence: 12, ParallelGroup: 12, CallID: "call-earlier"},
		{Kind: ToolEventResult, Protocol: types.RelayFormatOpenAI, Sequence: 13, ParallelGroup: 13, CallID: "call-later"},
	})

	assert.Empty(t, issues)
	require.Len(t, graph.Groups, 1)
	assert.Equal(t, []int{1, 0}, graph.Groups[0].ExchangeIndexes)
	assert.Equal(t, 10, graph.Exchanges[graph.Groups[0].ExchangeIndexes[0]].Sequence)
	assert.Equal(t, 11, graph.Exchanges[graph.Groups[0].ExchangeIndexes[1]].Sequence)
}

func TestValidateToolGraphRejectsMissingDuplicateAndCrossGroupResults(t *testing.T) {
	tests := []struct {
		name               string
		events             []ToolEvent
		reasonCode         string
		expectedStatuses   []ToolExchangeStatus
		expectedIssueCodes []string
	}{
		{
			name: "missing result keeps group pending",
			events: []ToolEvent{
				{Kind: ToolEventCall, Protocol: types.RelayFormatOpenAI, Sequence: 1, ParallelGroup: 1, CallID: "call-1"},
			},
			reasonCode:         "missing_tool_result",
			expectedStatuses:   []ToolExchangeStatus{ToolExchangePending},
			expectedIssueCodes: []string{"missing_tool_result"},
		},
		{
			name: "duplicate result fails call group",
			events: []ToolEvent{
				{Kind: ToolEventCall, Protocol: types.RelayFormatOpenAI, Sequence: 1, ParallelGroup: 1, CallID: "call-1"},
				{Kind: ToolEventResult, Protocol: types.RelayFormatOpenAI, Sequence: 2, ParallelGroup: 2, CallID: "call-1"},
				{Kind: ToolEventResult, Protocol: types.RelayFormatOpenAI, Sequence: 3, ParallelGroup: 3, CallID: "call-1"},
			},
			reasonCode:         "duplicate_tool_result",
			expectedStatuses:   []ToolExchangeStatus{ToolExchangeFailed},
			expectedIssueCodes: []string{"duplicate_tool_result"},
		},
		{
			name: "one result container cannot close different call groups",
			events: []ToolEvent{
				{Kind: ToolEventCall, Protocol: types.RelayFormatClaude, Sequence: 1000, ParallelGroup: 1, CallID: "call-1"},
				{Kind: ToolEventCall, Protocol: types.RelayFormatClaude, Sequence: 2000, ParallelGroup: 2, CallID: "call-2"},
				{Kind: ToolEventResult, Protocol: types.RelayFormatClaude, Sequence: 3000, ParallelGroup: 3, CallID: "call-1"},
				{Kind: ToolEventResult, Protocol: types.RelayFormatClaude, Sequence: 3001, ParallelGroup: 3, CallID: "call-2"},
			},
			reasonCode:         "tool_result_group_mismatch",
			expectedStatuses:   []ToolExchangeStatus{ToolExchangeFailed, ToolExchangeFailed},
			expectedIssueCodes: []string{"tool_result_group_mismatch", "missing_tool_result"},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			graph, issues := ValidateToolGraph(test.events)
			require.Len(t, graph.Groups, len(test.expectedStatuses))
			for groupIndex, expectedStatus := range test.expectedStatuses {
				assert.Equal(t, expectedStatus, graph.Groups[groupIndex].Status)
			}
			issueCodes := make([]string, 0, len(issues))
			for _, issue := range issues {
				issueCodes = append(issueCodes, issue.Code)
			}
			assert.Contains(t, issueCodes, test.reasonCode)
			for _, expectedIssueCode := range test.expectedIssueCodes {
				assert.Contains(t, issueCodes, expectedIssueCode)
			}
		})
	}
}

func TestValidateToolGraphKeepsGeminiSameNameParallelCallsAmbiguous(t *testing.T) {
	graph, issues := ValidateToolGraph([]ToolEvent{
		{Kind: ToolEventCall, Protocol: types.RelayFormatGemini, Sequence: 1000, ParallelGroup: 1, FunctionName: "lookup-sensitive", MatchByName: true},
		{Kind: ToolEventCall, Protocol: types.RelayFormatGemini, Sequence: 1001, ParallelGroup: 1, FunctionName: "lookup-sensitive", MatchByName: true},
		{Kind: ToolEventResult, Protocol: types.RelayFormatGemini, Sequence: 2000, ParallelGroup: 2, FunctionName: "lookup-sensitive", MatchByName: true},
		{Kind: ToolEventResult, Protocol: types.RelayFormatGemini, Sequence: 2001, ParallelGroup: 2, FunctionName: "lookup-sensitive", MatchByName: true},
	})

	require.NotEmpty(t, issues)
	issueCodes := make([]string, 0, len(issues))
	for _, issue := range issues {
		issueCodes = append(issueCodes, issue.Code)
	}
	assert.Contains(t, issueCodes, "ambiguous_tool_result")
	assert.Contains(t, issueCodes, "missing_tool_result")
	assert.Equal(t, []string{"lookup-sensitive"}, graph.AmbiguousFunctionNames)
	require.Len(t, graph.Groups, 1)
	assert.Equal(t, ToolIdentityModeFunctionName, graph.Groups[0].IdentityMode)
	assert.Equal(t, ToolExchangeFailed, graph.Groups[0].Status)

	encodedGraph, err := common.Marshal(graph)
	require.NoError(t, err)
	assert.False(t, strings.Contains(string(encodedGraph), "lookup-sensitive"))
}

func TestValidateToolGraphRejectsMismatchedOrOutOfOrderResults(t *testing.T) {
	tests := []struct {
		name       string
		result     ToolEvent
		reasonCode string
	}{
		{
			name:       "protocol mismatch",
			result:     ToolEvent{Kind: ToolEventResult, Protocol: types.RelayFormatClaude, Sequence: 2, CallID: "call-1"},
			reasonCode: "tool_result_protocol_mismatch",
		},
		{
			name:       "result before call",
			result:     ToolEvent{Kind: ToolEventResult, Protocol: types.RelayFormatOpenAI, Sequence: 0, CallID: "call-1"},
			reasonCode: "tool_result_order_invalid",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			graph, issues := ValidateToolGraph([]ToolEvent{
				{Kind: ToolEventCall, Protocol: types.RelayFormatOpenAI, Sequence: 1, CallID: "call-1", FunctionName: "lookup"},
				test.result,
			})

			require.Len(t, graph.Exchanges, 1)
			assert.Equal(t, ToolExchangePending, graph.Exchanges[0].Status)
			assert.Nil(t, graph.Exchanges[0].ResultSequence)
			issueCodes := make([]string, 0, len(issues))
			for _, issue := range issues {
				issueCodes = append(issueCodes, issue.Code)
			}
			assert.Contains(t, issueCodes, test.reasonCode)
			assert.Contains(t, issueCodes, "missing_tool_result")
		})
	}
}
