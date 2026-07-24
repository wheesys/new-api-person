package contextconsensus

import (
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBuildCompactionPromptIncludesOnlyCoveredPureTextHistory(t *testing.T) {
	tests := []struct {
		name     string
		protocol types.RelayFormat
		model    string
		body     string
	}{
		{name: "Chat Completions", protocol: types.RelayFormatOpenAI, model: "gpt-5-mini", body: chatRewriteBody},
		{name: "Responses", protocol: types.RelayFormatOpenAIResponses, model: "gpt-5-mini", body: responsesRewriteBody},
		{name: "Claude", protocol: types.RelayFormatClaude, model: "gpt-5-mini", body: claudeRewriteBody},
		{name: "Gemini", protocol: types.RelayFormatGemini, model: "gpt-5-mini", body: geminiRewriteBody},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			plan := testCompactionPlan(t, test.protocol, test.body)
			prompt, err := BuildCompactionPrompt(CompactionPromptRequest{
				Model: test.model, Protocol: test.protocol, Body: []byte(test.body), Plan: plan,
			})
			require.NoError(t, err)
			require.Len(t, prompt.Messages, 2)
			assert.Equal(t, "system", prompt.Messages[0].Role)
			assert.Equal(t, compactionPromptSystemMessage, prompt.Messages[0].StringContent())
			assert.Equal(t, "user", prompt.Messages[1].Role)
			assert.Equal(t, test.model, prompt.Model)
			require.NotNil(t, prompt.Stream)
			assert.False(t, *prompt.Stream)
			require.NotNil(t, prompt.MaxCompletionTokens)
			assert.Equal(t, uint(plan.MaxSummaryTokens), *prompt.MaxCompletionTokens)

			payload := decodeCompactionPromptPayload(t, prompt.Messages[1].StringContent())
			assert.Equal(t, plan.SourceDigest, payload.SourceDigest)
			assert.Equal(t, plan.CoveredRanges, payload.SourceRanges)
			require.Len(t, payload.History, 2)
			assert.Equal(t, "user", payload.History[0].Role)
			assert.Equal(t, "old user", payload.History[0].Content)
			assert.Equal(t, "assistant", payload.History[1].Role)
			assert.Equal(t, "old assistant", payload.History[1].Content)

			userPayload := prompt.Messages[1].StringContent()
			assert.NotContains(t, userPayload, "system invariant")
			assert.NotContains(t, userPayload, "developer invariant")
			assert.NotContains(t, userPayload, "recent user")
			assert.NotContains(t, userPayload, "recent assistant")
			assert.NotContains(t, userPayload, "current user")
			assert.NotContains(t, userPayload, "json_schema")
		})
	}
}

func TestBuildCompactionPromptIsDeterministic(t *testing.T) {
	plan := testCompactionPlan(t, types.RelayFormatOpenAI, chatRewriteBody)
	request := CompactionPromptRequest{
		Model: "gpt-5-mini", Protocol: types.RelayFormatOpenAI, Body: []byte(chatRewriteBody), Plan: plan,
	}
	first, err := BuildCompactionPrompt(request)
	require.NoError(t, err)
	second, err := BuildCompactionPrompt(request)
	require.NoError(t, err)
	firstJSON, err := common.Marshal(first)
	require.NoError(t, err)
	secondJSON, err := common.Marshal(second)
	require.NoError(t, err)
	assert.Equal(t, firstJSON, secondJSON)
}

func TestBuildCompactionPromptRejectsPlanOrSourceMismatch(t *testing.T) {
	plan := testCompactionPlan(t, types.RelayFormatOpenAI, chatRewriteBody)
	tamperedBody := strings.Replace(chatRewriteBody, "old user", "tampered user", 1)
	_, err := BuildCompactionPrompt(CompactionPromptRequest{
		Model: "gpt-5-mini", Protocol: types.RelayFormatOpenAI, Body: []byte(tamperedBody), Plan: plan,
	})
	require.ErrorContains(t, err, "compaction plan")

	_, err = BuildCompactionPrompt(CompactionPromptRequest{
		Model: "auto:cheap", Protocol: types.RelayFormatOpenAI, Body: []byte(chatRewriteBody), Plan: plan,
	})
	require.ErrorContains(t, err, "explicit real model")
}

func decodeCompactionPromptPayload(t *testing.T, content string) compactionPromptPayload {
	t.Helper()
	require.True(t, strings.HasPrefix(content, compactionPromptUserPreamble))
	var payload compactionPromptPayload
	require.NoError(t, common.Unmarshal([]byte(strings.TrimPrefix(content, compactionPromptUserPreamble)), &payload))
	return payload
}
