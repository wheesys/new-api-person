package contextconsensus

import (
	"context"
	"testing"

	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type fixedTokenCounter struct {
	result TokenCountResult
	err    error
}

func (counter fixedTokenCounter) CountPromptTokens(_ context.Context, _ TokenCountRequest) (TokenCountResult, error) {
	return counter.result, counter.err
}

type fixedContextLimitResolver struct {
	result ContextLimitResult
	err    error
}

func (resolver fixedContextLimitResolver) ResolveContextLimit(_ context.Context, _ ContextLimitRequest) (ContextLimitResult, error) {
	return resolver.result, resolver.err
}

func TestEvaluateTokenBudgetBoundaries(t *testing.T) {
	countRequest := testTokenCountRequest()
	counter := authoritativeTokenCounter(countRequest, TokenBreakdown{
		TextTokens:             50,
		ToolTokens:             10,
		SchemaTokens:           5,
		MediaTokens:            20,
		ProtocolOverheadTokens: 5,
	})
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
				countRequest,
				TokenBudgetRequest{ContextLimitTokens: test.limit, DefaultMaxOutputTokens: 10, SafetyMarginTokens: 5},
			)
			require.NoError(t, err)
			assert.Equal(t, 90, budget.PromptTokens)
			assert.Equal(t, 105, budget.RequiredTokens)
			assert.Equal(t, test.wantFits, budget.Fits)
			assert.Equal(t, test.remaining, budget.RemainingTokens)
			assert.True(t, budget.TokenCount.Authoritative)
		})
	}
}

func TestEvaluateTokenBudgetPreservesExplicitZeroOutput(t *testing.T) {
	countRequest := testTokenCountRequest()
	explicitZero := uint(0)
	budget, err := EvaluateTokenBudget(
		context.Background(),
		authoritativeTokenCounter(countRequest, TokenBreakdown{TextTokens: 90}),
		countRequest,
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

func TestEvaluateTokenBudgetRejectsNonAuthoritativeOrMismatchedCountEvidence(t *testing.T) {
	countRequest := testTokenCountRequest()
	validResult := authoritativeTokenCounter(countRequest, TokenBreakdown{TextTokens: 20}).result
	tests := []struct {
		name          string
		mutate        func(*TokenCountResult)
		errorContains string
	}{
		{name: "not authoritative", mutate: func(result *TokenCountResult) { result.Authoritative = false }, errorContains: "not authoritative"},
		{name: "model mismatch", mutate: func(result *TokenCountResult) { result.Model = "other-model" }, errorContains: "model does not match"},
		{name: "channel mismatch", mutate: func(result *TokenCountResult) { result.ChannelID++ }, errorContains: "channel ID does not match"},
		{name: "protocol mismatch", mutate: func(result *TokenCountResult) { result.Protocol = types.RelayFormatClaude }, errorContains: "protocol does not match"},
		{name: "body mismatch", mutate: func(result *TokenCountResult) { result.BodyDigest = digestString("other") }, errorContains: "body digest does not match"},
		{name: "missing source", mutate: func(result *TokenCountResult) { result.Source = "" }, errorContains: "source is required"},
		{name: "missing version", mutate: func(result *TokenCountResult) { result.Version = "" }, errorContains: "version is required"},
		{name: "negative component", mutate: func(result *TokenCountResult) { result.Breakdown.MediaTokens = -1 }, errorContains: "media tokens must not be negative"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			result := validResult
			test.mutate(&result)
			_, err := EvaluateTokenBudget(context.Background(), fixedTokenCounter{result: result}, countRequest, TokenBudgetRequest{ContextLimitTokens: 100})
			require.ErrorContains(t, err, test.errorContains)
		})
	}
}

func TestEvaluateResolvedTokenBudgetRequiresAuthoritativeCandidateLimit(t *testing.T) {
	countRequest := testTokenCountRequest()
	limit := ContextLimitResult{
		ContextLimitTokens: 120,
		Authoritative:      true,
		Model:              countRequest.Model,
		ChannelID:          countRequest.ChannelID,
		Protocol:           countRequest.Protocol,
		Source:             "configured_model_context_limit",
		Version:            "settings-v4",
	}
	budget, err := EvaluateResolvedTokenBudget(
		context.Background(),
		authoritativeTokenCounter(countRequest, TokenBreakdown{TextTokens: 90}),
		fixedContextLimitResolver{result: limit},
		countRequest,
		ResolvedTokenBudgetRequest{DefaultMaxOutputTokens: 10, SafetyMarginTokens: 5},
	)
	require.NoError(t, err)
	require.NotNil(t, budget.ContextLimit)
	assert.Equal(t, limit, *budget.ContextLimit)
	assert.Equal(t, 15, budget.RemainingTokens)

	limit.Authoritative = false
	_, err = EvaluateResolvedTokenBudget(
		context.Background(),
		authoritativeTokenCounter(countRequest, TokenBreakdown{TextTokens: 90}),
		fixedContextLimitResolver{result: limit},
		countRequest,
		ResolvedTokenBudgetRequest{},
	)
	require.ErrorContains(t, err, "context limit result is not authoritative")
}

func TestEvaluateResolvedTokenBudgetRejectsCrossCandidateLimitEvidence(t *testing.T) {
	countRequest := testTokenCountRequest()
	limit := ContextLimitResult{
		ContextLimitTokens: 120,
		Authoritative:      true,
		Model:              countRequest.Model,
		ChannelID:          countRequest.ChannelID + 1,
		Protocol:           countRequest.Protocol,
		Source:             "configured_model_context_limit",
		Version:            "settings-v4",
	}
	_, err := EvaluateResolvedTokenBudget(
		context.Background(),
		authoritativeTokenCounter(countRequest, TokenBreakdown{TextTokens: 90}),
		fixedContextLimitResolver{result: limit},
		countRequest,
		ResolvedTokenBudgetRequest{},
	)
	require.ErrorContains(t, err, "context limit result channel ID does not match request")
}

func testTokenCountRequest() TokenCountRequest {
	return TokenCountRequest{
		Protocol:    types.RelayFormatOpenAI,
		Model:       "gpt-5",
		ChannelID:   17,
		RequestBody: []byte(`{"model":"gpt-5","messages":[{"role":"user","content":"hello"}]}`),
	}
}

func authoritativeTokenCounter(request TokenCountRequest, breakdown TokenBreakdown) fixedTokenCounter {
	return fixedTokenCounter{result: TokenCountResult{
		Breakdown:     breakdown,
		Authoritative: true,
		Model:         request.Model,
		ChannelID:     request.ChannelID,
		Protocol:      request.Protocol,
		BodyDigest:    digestBytes(request.RequestBody),
		Source:        "provider_tokenizer",
		Version:       "tokenizer-v2",
	}}
}
