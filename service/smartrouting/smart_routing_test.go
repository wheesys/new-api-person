package smartrouting

import (
	"testing"

	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestResolveVirtualModel(t *testing.T) {
	profile, ok := ResolveVirtualModel("auto")
	require.True(t, ok)
	assert.Equal(t, "auto:balanced", profile.Name)
	assert.Equal(t, PolicyBalanced, profile.Policy)

	profile, ok = ResolveVirtualModel("auto:cheap")
	require.True(t, ok)
	assert.Equal(t, "auto:cheap", profile.Name)
	assert.Equal(t, PolicyCostFirst, profile.Policy)
	assert.Equal(t, []ModelQualityTier{QualityEconomy, QualityStandard}, profile.PreferredTiers)

	profile, ok = ResolveVirtualModel("smart:quality")
	require.True(t, ok)
	assert.Equal(t, "auto:quality", profile.Name)
	assert.Equal(t, PolicyQualityFirst, profile.Policy)
	assert.Equal(t, []ModelQualityTier{QualityPremium, QualityReasoning, QualityStandard}, profile.PreferredTiers)

	_, ok = ResolveVirtualModel("gpt-5-mini")
	assert.False(t, ok)
}

func TestAnalyzeRequestSeparatesTaskComplexityFromContextRequirement(t *testing.T) {
	analysis := AnalyzeRequest(SmartRouteRequest{
		OriginalModel:         "auto:balanced",
		EndpointType:          EndpointChatCompletions,
		EstimatedPromptTokens: 42000,
		MaxOutputTokens:       256,
		TokenMeta: &types.TokenCountMeta{
			CombineText:   "把上面那句话翻译成英文",
			MessagesCount: 48,
			MaxTokens:     256,
		},
	})

	assert.Equal(t, TaskSimple, analysis.TaskComplexity)
	assert.Equal(t, ContextLong, analysis.ContextRequirement)
	assert.Contains(t, analysis.TaskReasons, "simple_rewrite_or_translation")
	assert.Contains(t, analysis.ContextReasons, "large_prompt_tokens")
}

func TestAnalyzeRequestPromotesStrictToolAndReasoningRequests(t *testing.T) {
	analysis := AnalyzeRequest(SmartRouteRequest{
		OriginalModel:       "auto:quality",
		EndpointType:        EndpointResponses,
		Stream:              true,
		HasTools:            true,
		ToolCount:           3,
		RequiresJSONSchema:  true,
		HasImages:           true,
		ReasoningRequested:  true,
		RequiresReliability: true,
		TokenMeta: &types.TokenCountMeta{
			CombineText:   "设计一个多步骤迁移方案，生成严格 JSON schema，并调用工具验证每一步",
			MessagesCount: 6,
			MaxTokens:     4096,
		},
	})

	assert.Equal(t, TaskCritical, analysis.TaskComplexity)
	assert.Equal(t, ContextMedium, analysis.ContextRequirement)
	assert.Contains(t, analysis.TaskReasons, "tools_required")
	assert.Contains(t, analysis.TaskReasons, "json_schema_required")
	assert.Contains(t, analysis.TaskReasons, "reasoning_requested")
	assert.Contains(t, analysis.TaskReasons, "high_reliability_required")
}

func TestRankCandidatesFiltersHardRequirementsAndAppliesPolicy(t *testing.T) {
	request := SmartRouteRequest{
		OriginalModel:         "auto:quality",
		EndpointType:          EndpointResponses,
		Stream:                true,
		HasTools:              true,
		RequiresJSONSchema:    true,
		ContextTokensRequired: 12000,
	}

	candidates := []SmartRouteCandidate{
		{
			ModelName:        "economy-fast",
			ChannelID:        1,
			Group:            "default",
			QualityTier:      QualityEconomy,
			MaxContextTokens: 128000,
			SupportsStream:   true,
			EstimatedQuota:   20,
			Reliability:      0.96,
			LatencyScore:     0.95,
			QualityScore:     0.45,
		},
		{
			ModelName:          "standard-tools",
			ChannelID:          2,
			Group:              "default",
			QualityTier:        QualityStandard,
			MaxContextTokens:   128000,
			SupportsTools:      true,
			SupportsJSONSchema: true,
			SupportsStream:     true,
			EstimatedQuota:     45,
			Reliability:        0.90,
			LatencyScore:       0.80,
			QualityScore:       0.70,
		},
		{
			ModelName:          "premium-reasoning",
			ChannelID:          3,
			Group:              "default",
			QualityTier:        QualityReasoning,
			MaxContextTokens:   200000,
			SupportsTools:      true,
			SupportsJSONSchema: true,
			SupportsStream:     true,
			EstimatedQuota:     80,
			Reliability:        0.98,
			LatencyScore:       0.55,
			QualityScore:       0.97,
		},
	}

	ranked, rejections := RankCandidates(request, candidates, PolicyQualityFirst)
	require.Len(t, ranked, 2)
	require.Len(t, rejections, 1)
	assert.Equal(t, "economy-fast", rejections[0].Candidate.ModelName)
	assert.Contains(t, rejections[0].Reasons, "tools_not_supported")
	assert.Equal(t, "premium-reasoning", ranked[0].ModelName)
	assert.Greater(t, ranked[0].FinalScore, ranked[1].FinalScore)

	costFirst, rejections := RankCandidates(request, candidates, PolicyCostFirst)
	require.Len(t, costFirst, 2)
	require.Len(t, rejections, 1)
	assert.Equal(t, "standard-tools", costFirst[0].ModelName)
}
