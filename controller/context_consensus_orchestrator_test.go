package controller

import (
	"context"
	"fmt"
	"net/http/httptest"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/QuantumNous/new-api/service/contextconsensus"
	"github.com/QuantumNous/new-api/service/smartrouting"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type unusedContextConsensusCounter struct{}

func (unusedContextConsensusCounter) CountPromptTokens(context.Context, contextconsensus.TokenCountRequest) (contextconsensus.TokenCountResult, error) {
	return contextconsensus.TokenCountResult{}, fmt.Errorf("unused")
}

type unusedContextConsensusResolver struct{}

func (unusedContextConsensusResolver) ResolveContextLimit(context.Context, contextconsensus.ContextLimitRequest) (contextconsensus.ContextLimitResult, error) {
	return contextconsensus.ContextLimitResult{}, fmt.Errorf("unused")
}

func TestEvaluateContextConsensusCandidatesUsesLaterFullContextFitWithoutCompaction(t *testing.T) {
	context, _ := gin.CreateTestContext(httptest.NewRecorder())
	candidates := []smartrouting.SmartRouteCandidate{
		{ModelName: "gpt-4o-mini", ChannelID: 1},
		{ModelName: "gpt-4o", ChannelID: 2},
	}
	preparedChannels := make([]int, 0, 2)
	preparer := func(
		_ *gin.Context,
		_ types.RelayFormat,
		_ []byte,
		candidate smartrouting.SmartRouteCandidate,
		_ contextconsensus.TokenCounter,
		_ contextconsensus.ContextLimitResolver,
		_ int,
	) (*contextConsensusMainCommit, error) {
		preparedChannels = append(preparedChannels, candidate.ChannelID)
		return &contextConsensusMainCommit{
			candidate: candidate,
			budget: contextconsensus.TokenBudget{
				PromptTokens: 100 + candidate.ChannelID,
				Fits:         candidate.ChannelID == 2,
			},
		}, nil
	}

	commit, allOverLimit, inputTokens, err := evaluateContextConsensusCandidates(
		context,
		types.RelayFormatOpenAI,
		[]byte(`{"model":"gpt-4o","messages":[]}`),
		candidates,
		unusedContextConsensusCounter{},
		unusedContextConsensusResolver{},
		1024,
		"",
		preparer,
	)

	require.NoError(t, err)
	require.NotNil(t, commit)
	assert.Equal(t, 2, commit.candidate.ChannelID)
	assert.False(t, allOverLimit)
	assert.Equal(t, 102, inputTokens)
	assert.Equal(t, []int{1, 2}, preparedChannels)
}

func TestEvaluateContextConsensusCandidatesFailsClosedOnUnknownEvidence(t *testing.T) {
	context, _ := gin.CreateTestContext(httptest.NewRecorder())
	candidates := []smartrouting.SmartRouteCandidate{
		{ModelName: "unknown", ChannelID: 1},
		{ModelName: "gpt-4o", ChannelID: 2},
	}
	preparer := func(
		_ *gin.Context,
		_ types.RelayFormat,
		_ []byte,
		candidate smartrouting.SmartRouteCandidate,
		_ contextconsensus.TokenCounter,
		_ contextconsensus.ContextLimitResolver,
		_ int,
	) (*contextConsensusMainCommit, error) {
		if candidate.ChannelID == 1 {
			return nil, fmt.Errorf("tokenizer unavailable")
		}
		return &contextConsensusMainCommit{candidate: candidate, budget: contextconsensus.TokenBudget{PromptTokens: 120, Fits: false}}, nil
	}

	commit, allOverLimit, _, err := evaluateContextConsensusCandidates(
		context,
		types.RelayFormatOpenAI,
		[]byte(`{"model":"gpt-4o","messages":[]}`),
		candidates,
		unusedContextConsensusCounter{},
		unusedContextConsensusResolver{},
		1024,
		"",
		preparer,
	)

	require.ErrorContains(t, err, "tokenizer unavailable")
	assert.Nil(t, commit)
	assert.False(t, allOverLimit)
}

func TestEvaluateContextConsensusCandidatesRequiresFinalProtocol(t *testing.T) {
	context, _ := gin.CreateTestContext(httptest.NewRecorder())
	candidates := []smartrouting.SmartRouteCandidate{
		{ModelName: "claude-sonnet", ChannelID: 1},
		{ModelName: "gpt-5", ChannelID: 2},
	}
	preparedChannels := make([]int, 0, len(candidates))
	preparer := func(
		_ *gin.Context,
		_ types.RelayFormat,
		_ []byte,
		candidate smartrouting.SmartRouteCandidate,
		_ contextconsensus.TokenCounter,
		_ contextconsensus.ContextLimitResolver,
		_ int,
	) (*contextConsensusMainCommit, error) {
		preparedChannels = append(preparedChannels, candidate.ChannelID)
		preparedProtocol := types.RelayFormat(types.RelayFormatClaude)
		if candidate.ChannelID == 2 {
			preparedProtocol = types.RelayFormatOpenAI
		}
		return &contextConsensusMainCommit{
			candidate:        candidate,
			preparedProtocol: preparedProtocol,
			budget:           contextconsensus.TokenBudget{PromptTokens: 100, Fits: true},
		}, nil
	}

	commit, allOverLimit, _, err := evaluateContextConsensusCandidates(
		context,
		types.RelayFormatOpenAI,
		[]byte(`{"model":"gpt-5","messages":[]}`),
		candidates,
		unusedContextConsensusCounter{},
		unusedContextConsensusResolver{},
		1024,
		types.RelayFormatOpenAI,
		preparer,
	)

	require.NoError(t, err)
	require.NotNil(t, commit)
	assert.Equal(t, 2, commit.candidate.ChannelID)
	assert.False(t, allOverLimit)
	assert.Equal(t, []int{1, 2}, preparedChannels)
}

func TestBuildContextConsensusCompactionSelectsV2WithoutRawToolState(t *testing.T) {
	body, envelope, policy, provider := contextConsensusToolCompactionFixture(t)

	plan, err := buildContextConsensusCompactionPlan(types.RelayFormatOpenAI, body, envelope, policy, provider)
	require.NoError(t, err)
	assert.Equal(t, contextconsensus.ConsensusSummaryVersionV2, plan.SummaryVersion)
	assert.True(t, plan.ToolContextPresent)

	summaryRequest, err := buildContextConsensusCompactionPrompt(contextconsensus.CompactionPromptRequest{
		Model: "summary-model", Protocol: types.RelayFormatOpenAI, Body: body, Plan: plan,
	})
	require.NoError(t, err)
	encoded, err := common.Marshal(summaryRequest)
	require.NoError(t, err)
	assert.Contains(t, fmt.Sprint(summaryRequest.Messages[0].Content), "Set version to 2")
	for _, rawToolState := range []string{"call-sensitive", "lookup_private_account", "account-secret"} {
		assert.NotContains(t, string(encoded), rawToolState)
	}
}

func TestBuildContextConsensusCompactionDefaultsToNoToolPolicies(t *testing.T) {
	body, envelope, policy, _ := contextConsensusToolCompactionFixture(t)
	provider, err := newServerToolSanitizationPolicyProvider()
	require.NoError(t, err)

	plan, err := buildContextConsensusCompactionPlan(types.RelayFormatOpenAI, body, envelope, policy, provider)
	require.NoError(t, err)
	assert.Equal(t, contextconsensus.ConsensusSummaryVersion, plan.SummaryVersion)
	require.Len(t, plan.CoveredRanges, 1)
	assert.Equal(t, 0, plan.CoveredRanges[0].StartSequence)
	assert.Equal(t, 1, plan.CoveredRanges[0].EndSequence)
}

func contextConsensusToolCompactionFixture(t *testing.T) ([]byte, *contextconsensus.ContextEnvelope, contextconsensus.CompactionPolicySnapshot, contextconsensus.ToolSanitizationPolicyProvider) {
	t.Helper()
	body := []byte(`{
  "model":"auto:balanced",
  "messages":[
    {"role":"user","content":"older question"},
    {"role":"assistant","content":"older answer"},
    {"role":"user","content":"lookup account"},
    {"role":"assistant","tool_calls":[{"id":"call-sensitive","type":"function","function":{"name":"lookup_private_account","arguments":"{\"account\":\"account-secret\"}"}}]},
    {"role":"tool","tool_call_id":"call-sensitive","content":"{\"status\":\"ok\"}"},
    {"role":"assistant","content":"lookup complete"},
    {"role":"user","content":"recent question"},
    {"role":"assistant","content":"recent answer"},
    {"role":"user","content":"continue"}
  ],
  "tools":[{"type":"function","function":{"name":"lookup_private_account","parameters":{"type":"object","properties":{"account":{"type":"string"}}}}}]
}`)
	envelope, err := contextconsensus.Extract(contextconsensus.ExtractionRequest{Protocol: types.RelayFormatOpenAI, Body: body})
	require.NoError(t, err)
	assessment := contextconsensus.AssessSingleSerialToolCompaction(envelope)
	require.True(t, assessment.ReadyForSanitization)
	require.NotNil(t, assessment.Evidence)
	provider, err := contextconsensus.NewStaticToolSanitizationPolicyProvider([]contextconsensus.ToolResultSanitizationPolicy{{
		SanitizerVersion:   contextconsensus.ToolResultSanitizerVersion,
		Version:            "lookup-policy-v1",
		ToolIdentityDigest: assessment.Evidence.ToolIdentityDigest,
		SchemaDigest:       assessment.Evidence.SchemaDigest,
		MaxInputBytes:      4096,
		MaxOutputBytes:     2048,
		MaxDepth:           4,
		Rules: []contextconsensus.ToolResultProjectionRule{{
			JSONPointer: "/status", OutputField: "status", ValueType: contextconsensus.ToolResultScalarString,
			MaxStringBytes: 16, AllowedStringValues: []string{"ok"},
		}},
	}})
	require.NoError(t, err)
	policy := contextconsensus.CompactionPolicy{
		SystemEnabled: true, AllowToolResultCompaction: true, PolicyVersion: "context-consensus-v1",
		PreservedRecentTurns: 1, TargetInputTokens: 4096, MaxSummaryTokens: 512,
	}.Snapshot(true, true)
	return body, envelope, policy, provider
}

func TestRewriteContextConsensusSourceModelPreservesExplicitZero(t *testing.T) {
	body, err := rewriteContextConsensusSourceModel(
		types.RelayFormatOpenAI,
		[]byte(`{"model":"auto:balanced","messages":[{"role":"user","content":"hello"}],"max_completion_tokens":0}`),
		"gpt-4o",
	)
	require.NoError(t, err)

	var request dto.GeneralOpenAIRequest
	require.NoError(t, common.Unmarshal(body, &request))
	assert.Equal(t, "gpt-4o", request.Model)
	require.NotNil(t, request.MaxCompletionTokens)
	assert.Zero(t, *request.MaxCompletionTokens)
}

func TestFrozenContextConsensusChannelKeyDoesNotAdvancePollingState(t *testing.T) {
	channel := &model.Channel{
		Key: "first\nsecond",
		ChannelInfo: model.ChannelInfo{
			IsMultiKey:           true,
			MultiKeyPollingIndex: 1,
			MultiKeyStatusList:   map[int]int{0: common.ChannelStatusEnabled, 1: common.ChannelStatusEnabled},
		},
	}

	key, index, err := frozenContextConsensusChannelKey(channel)

	require.NoError(t, err)
	assert.Equal(t, "first", key)
	assert.Zero(t, index)
	assert.Equal(t, 1, channel.ChannelInfo.MultiKeyPollingIndex)
}
