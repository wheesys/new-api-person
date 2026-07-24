package contextconsensus

import (
	"context"
	"testing"

	"github.com/QuantumNous/new-api/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type fixedTokenCounter struct {
	breakdown TokenBreakdown
	err       error
}

func (counter fixedTokenCounter) CountPromptTokens(_ context.Context, _ TokenCountRequest) (TokenBreakdown, error) {
	return counter.breakdown, counter.err
}

func TestEvaluateTokenBudgetBoundaries(t *testing.T) {
	counter := fixedTokenCounter{breakdown: TokenBreakdown{
		TextTokens:             50,
		ToolTokens:             10,
		SchemaTokens:           5,
		MediaTokens:            20,
		ProtocolOverheadTokens: 5,
	}}
	tests := []struct {
		name      string
		limit     int
		wantFits  bool
		remaining int
	}{
		{name: "limit minus one", limit: 104, wantFits: false, remaining: -1},
		{name: "exact limit", limit: 105, wantFits: true, remaining: 0},
		{name: "limit plus one", limit: 106, wantFits: true, remaining: 1},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			budget, err := EvaluateTokenBudget(
				context.Background(),
				counter,
				TokenCountRequest{Protocol: types.RelayFormatOpenAI, Model: "gpt-5"},
				TokenBudgetRequest{ContextLimitTokens: test.limit, DefaultMaxOutputTokens: 10, SafetyMarginTokens: 5},
			)
			require.NoError(t, err)
			assert.Equal(t, 90, budget.PromptTokens)
			assert.Equal(t, 105, budget.RequiredTokens)
			assert.Equal(t, test.wantFits, budget.Fits)
			assert.Equal(t, test.remaining, budget.RemainingTokens)
		})
	}
}

func TestEvaluateTokenBudgetPreservesExplicitZeroOutput(t *testing.T) {
	explicitZero := uint(0)
	budget, err := EvaluateTokenBudget(
		context.Background(),
		fixedTokenCounter{breakdown: TokenBreakdown{TextTokens: 90}},
		TokenCountRequest{Protocol: types.RelayFormatClaude, Model: "claude-sonnet-4"},
		TokenBudgetRequest{
			ContextLimitTokens:     100,
			RequestedMaxOutput:     &explicitZero,
			DefaultMaxOutputTokens: 50,
			SafetyMarginTokens:     10,
		},
	)

	require.NoError(t, err)
	assert.True(t, budget.ExplicitZeroOutput)
	assert.Zero(t, budget.OutputTokens)
	assert.True(t, budget.Fits)
	assert.Zero(t, budget.RemainingTokens)
}

func TestEvaluateTokenBudgetRejectsNegativeCounterComponents(t *testing.T) {
	_, err := EvaluateTokenBudget(
		context.Background(),
		fixedTokenCounter{breakdown: TokenBreakdown{MediaTokens: -1}},
		TokenCountRequest{},
		TokenBudgetRequest{ContextLimitTokens: 100},
	)

	require.ErrorContains(t, err, "media tokens must not be negative")
}
