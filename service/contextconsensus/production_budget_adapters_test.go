package contextconsensus

import (
	"context"
	"testing"

	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/QuantumNous/new-api/setting/model_setting"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestProductionTokenCounterCountsSupportedFinalRequestBody(t *testing.T) {
	body := []byte(`{"model":"gpt-4o","messages":[{"role":"user","content":"hello"}]}`)
	request := TokenCountRequest{
		Protocol:    types.RelayFormatOpenAI,
		Model:       "gpt-4o",
		ChannelID:   17,
		RequestBody: body,
	}

	result, err := NewProductionTokenCounter().CountPromptTokens(context.Background(), request)
	require.NoError(t, err)
	assert.True(t, result.Authoritative)
	assert.Equal(t, request.Model, result.Model)
	assert.Equal(t, request.ChannelID, result.ChannelID)
	assert.Equal(t, request.Protocol, result.Protocol)
	assert.Equal(t, digestBytes(body), result.BodyDigest)
	assert.Equal(t, productionTokenCounterSource, result.Source)
	assert.Contains(t, result.Version, "o200k_base")
	assert.Positive(t, result.Breakdown.TextTokens)
	assert.Zero(t, result.Breakdown.ToolTokens)
	assert.Zero(t, result.Breakdown.SchemaTokens)
	assert.Zero(t, result.Breakdown.MediaTokens)
	assert.Equal(t, 6, result.Breakdown.ProtocolOverheadTokens)
}

func TestProductionTokenCounterFailsClosedWithoutStrictTokenizerSupport(t *testing.T) {
	tests := []struct {
		name          string
		model         string
		protocol      types.RelayFormat
		body          []byte
		errorContains string
	}{
		{
			name:          "unknown model does not use fallback encoding",
			model:         "gpt-5-unknown",
			protocol:      types.RelayFormatOpenAI,
			body:          []byte(`{"model":"gpt-5-unknown","messages":[]}`),
			errorContains: "tokenizer is unavailable",
		},
		{
			name:          "non OpenAI protocol does not use estimate",
			model:         "claude-3-7-sonnet",
			protocol:      types.RelayFormatClaude,
			body:          []byte(`{"model":"claude-3-7-sonnet","messages":[]}`),
			errorContains: "does not support protocol",
		},
		{
			name:          "Responses protocol is not approximated",
			model:         "gpt-4o",
			protocol:      types.RelayFormatOpenAIResponses,
			body:          []byte(`{"model":"gpt-4o","input":"hello"}`),
			errorContains: "does not support protocol",
		},
		{
			name:          "body model must match final target",
			model:         "gpt-4o",
			protocol:      types.RelayFormatOpenAI,
			body:          []byte(`{"model":"gpt-4","messages":[]}`),
			errorContains: "body model does not match",
		},
		{
			name:          "body must be valid JSON",
			model:         "gpt-4o",
			protocol:      types.RelayFormatOpenAI,
			body:          []byte(`{"model":`),
			errorContains: "decode authoritative tokenizer request body",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			result, err := NewProductionTokenCounter().CountPromptTokens(context.Background(), TokenCountRequest{
				Protocol:    test.protocol,
				Model:       test.model,
				ChannelID:   3,
				RequestBody: test.body,
			})
			require.Error(t, err)
			assert.Contains(t, err.Error(), test.errorContains)
			assert.False(t, result.Authoritative)
		})
	}
}

func TestProductionTokenCounterHonorsCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := NewProductionTokenCounter().CountPromptTokens(ctx, TokenCountRequest{
		Protocol:    types.RelayFormatOpenAI,
		Model:       "gpt-4o",
		ChannelID:   3,
		RequestBody: []byte(`{"model":"gpt-4o","messages":[]}`),
	})
	require.ErrorIs(t, err, context.Canceled)
}

func TestFrozenContextLimitResolverRequiresExactCandidateMatch(t *testing.T) {
	settings := &model_setting.SmartRoutingSettings{
		AuthoritativeContextLimits: map[string]model_setting.AuthoritativeContextLimitConfig{
			"gpt-4o": {
				MaxContextTokens: 128000,
				Version:          "openai-2026-07",
				ChannelIDs:       []int{17},
				RelayFormats:     []string{string(types.RelayFormatOpenAIResponses)},
			},
		},
	}
	resolver := NewFrozenContextLimitResolver(settings)
	request := ContextLimitRequest{
		Model:     "gpt-4o",
		ChannelID: 17,
		Protocol:  types.RelayFormatOpenAIResponses,
	}

	result, err := resolver.ResolveContextLimit(context.Background(), request)
	require.NoError(t, err)
	assert.True(t, result.Authoritative)
	assert.Equal(t, 128000, result.ContextLimitTokens)
	assert.Equal(t, request.Model, result.Model)
	assert.Equal(t, request.ChannelID, result.ChannelID)
	assert.Equal(t, request.Protocol, result.Protocol)
	assert.Equal(t, configuredContextLimitSource, result.Source)
	assert.Equal(t, "openai-2026-07", result.Version)

	tests := []struct {
		name          string
		request       ContextLimitRequest
		errorContains string
	}{
		{
			name:          "model is exact",
			request:       ContextLimitRequest{Model: "GPT-4O", ChannelID: 17, Protocol: types.RelayFormatOpenAIResponses},
			errorContains: "is unavailable",
		},
		{
			name:          "channel is exact",
			request:       ContextLimitRequest{Model: "gpt-4o", ChannelID: 18, Protocol: types.RelayFormatOpenAIResponses},
			errorContains: "does not allow channel",
		},
		{
			name:          "protocol is exact",
			request:       ContextLimitRequest{Model: "gpt-4o", ChannelID: 17, Protocol: types.RelayFormatOpenAI},
			errorContains: "does not allow protocol",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			result, err := resolver.ResolveContextLimit(context.Background(), test.request)
			require.Error(t, err)
			assert.Contains(t, err.Error(), test.errorContains)
			assert.False(t, result.Authoritative)
		})
	}
}

func TestFrozenContextLimitResolverUsesImmutableSnapshot(t *testing.T) {
	settings := &model_setting.SmartRoutingSettings{
		AuthoritativeContextLimits: map[string]model_setting.AuthoritativeContextLimitConfig{
			"gpt-4o": {
				MaxContextTokens: 128000,
				Version:          "v1",
				ChannelIDs:       []int{17},
				RelayFormats:     []string{string(types.RelayFormatOpenAI)},
			},
		},
	}
	resolver := NewFrozenContextLimitResolver(settings)

	limit := settings.AuthoritativeContextLimits["gpt-4o"]
	limit.MaxContextTokens = 1
	limit.Version = "changed"
	limit.ChannelIDs[0] = 99
	limit.RelayFormats[0] = string(types.RelayFormatClaude)
	settings.AuthoritativeContextLimits["gpt-4o"] = limit

	result, err := resolver.ResolveContextLimit(context.Background(), ContextLimitRequest{
		Model:     "gpt-4o",
		ChannelID: 17,
		Protocol:  types.RelayFormatOpenAI,
	})
	require.NoError(t, err)
	assert.Equal(t, 128000, result.ContextLimitTokens)
	assert.Equal(t, "v1", result.Version)
}

func TestFrozenContextLimitResolverFailsClosedForInvalidConfiguration(t *testing.T) {
	settings := &model_setting.SmartRoutingSettings{
		AuthoritativeContextLimits: map[string]model_setting.AuthoritativeContextLimitConfig{
			"gpt-4o": {
				MaxContextTokens: 128000,
				ChannelIDs:       []int{17},
				RelayFormats:     []string{string(types.RelayFormatOpenAI)},
			},
		},
	}
	resolver := NewFrozenContextLimitResolver(settings)

	result, err := resolver.ResolveContextLimit(context.Background(), ContextLimitRequest{
		Model:     "gpt-4o",
		ChannelID: 17,
		Protocol:  types.RelayFormatOpenAI,
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "version")
	assert.False(t, result.Authoritative)
}
