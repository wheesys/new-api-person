package controller

import (
	"context"
	"fmt"
	"net/http/httptest"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service/contextconsensus"
	"github.com/QuantumNous/new-api/service/smartrouting"
	"github.com/QuantumNous/new-api/types"
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
		preparer,
	)

	require.ErrorContains(t, err, "tokenizer unavailable")
	assert.Nil(t, commit)
	assert.False(t, allOverLimit)
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
