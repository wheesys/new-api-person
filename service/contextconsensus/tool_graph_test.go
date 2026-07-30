package contextconsensus

import (
	"testing"

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
	assert.Equal(t, ToolExchangeCompleted, graph.Exchanges[0].Status)
	assert.Equal(t, ToolExchangeCompleted, graph.Exchanges[1].Status)
	require.NotNil(t, graph.Exchanges[0].ResultSequence)
	assert.Equal(t, 3, *graph.Exchanges[0].ResultSequence)
	require.NotNil(t, graph.Exchanges[1].ResultSequence)
	assert.Equal(t, 2, *graph.Exchanges[1].ResultSequence)
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
