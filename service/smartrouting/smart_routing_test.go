package smartrouting

import (
	"testing"

	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/QuantumNous/new-api/service/contextconsensus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRankCandidatesRejectsModelSwitchForProviderBoundContext(t *testing.T) {
	request := SmartRouteRequest{
		OriginalModel: "auto:quality",
		ContextConstraint: contextconsensus.ContextRoutingConstraint{
			ValidationMode:  "validate_only",
			RequiredBinding: contextconsensus.BindingLevelCredential,
			SwitchAllowed:   false,
			WouldBlock:      true,
		},
	}
	candidates := []SmartRouteCandidate{{ModelName: "gpt-5", ChannelID: 1}}

	ranked, rejections := RankCandidates(request, candidates, PolicyQualityFirst)

	assert.Empty(t, ranked)
	require.Len(t, rejections, 1)
	assert.Contains(t, rejections[0].Reasons, "context_state_switch_not_allowed")
}

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
	assert.Equal(t, TaskTypeTranslation, analysis.TaskType)
	assert.Equal(t, QualityEconomy, analysis.RecommendedTier)
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
	assert.Equal(t, TaskTypeReasoning, analysis.TaskType)
	assert.Equal(t, QualityReasoning, analysis.RecommendedTier)
	assert.Equal(t, ContextMedium, analysis.ContextRequirement)
	assert.Contains(t, analysis.TaskReasons, "tools_required")
	assert.Contains(t, analysis.TaskReasons, "json_schema_required")
	assert.Contains(t, analysis.TaskReasons, "reasoning_requested")
	assert.Contains(t, analysis.TaskReasons, "high_reliability_required")
}

func TestAnalyzeRequestClassifiesTaskTypes(t *testing.T) {
	tests := []struct {
		name     string
		text     string
		expected TaskType
	}{
		{name: "coding", text: "请调试这段代码并给出迁移方案", expected: TaskTypeCoding},
		{name: "analysis", text: "比较两个方案并分析优缺点", expected: TaskTypeAnalysis},
		{name: "creative", text: "创作一个短篇故事", expected: TaskTypeCreative},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			analysis := AnalyzeRequest(SmartRouteRequest{TokenMeta: &types.TokenCountMeta{CombineText: test.text}})
			assert.Equal(t, test.expected, analysis.TaskType)
		})
	}
}

func TestRankCandidatesScoresTaskContextCacheResetAndSessionAffinity(t *testing.T) {
	request := SmartRouteRequest{
		OriginalModel:         "auto:quality",
		ContextTokensRequired: 64000,
		TokenMeta: &types.TokenCountMeta{
			CombineText: "分析并修复这段代码的根因",
		},
	}
	candidates := []SmartRouteCandidate{
		{
			ModelName:        "general-model",
			ChannelID:        1,
			QualityTier:      QualityStandard,
			MaxContextTokens: 128000,
			Reliability:      1,
			LatencyScore:     0.5,
			ThroughputScore:  0.5,
			QualityScore:     0.7,
			CodingScore:      0.4,
			ResetWindowScore: 0.5,
			HealthState:      ChannelHealthHealthy,
		},
		{
			ModelName:          "coding-affinity-model",
			ChannelID:          2,
			QualityTier:        QualityPremium,
			MaxContextTokens:   256000,
			Reliability:        1,
			LatencyScore:       0.5,
			ThroughputScore:    0.5,
			QualityScore:       0.7,
			CodingScore:        0.95,
			CacheAffinityScore: 1,
			AffinityScore:      1,
			ResetWindowScore:   1,
			HealthState:        ChannelHealthHealthy,
		},
	}

	ranked, rejections := RankCandidates(request, candidates, PolicyQualityFirst)

	require.Empty(t, rejections)
	require.Len(t, ranked, 2)
	assert.Equal(t, "coding-affinity-model", ranked[0].ModelName)
	assert.Greater(t, ranked[0].ScoreFactors.TaskMatch, ranked[1].ScoreFactors.TaskMatch)
	assert.Greater(t, ranked[0].ScoreFactors.Context, ranked[1].ScoreFactors.Context)
	assert.Equal(t, 1.0, ranked[0].ScoreFactors.Cache)
	assert.Equal(t, 1.0, ranked[0].ScoreFactors.ResetWindow)
	assert.Equal(t, 1.0, ranked[0].ScoreFactors.Affinity)
}

func TestTaskMatchPrefersRecommendedEconomyTierForSimpleTranslation(t *testing.T) {
	request := SmartRouteRequest{TokenMeta: &types.TokenCountMeta{CombineText: "翻译成英文"}}
	candidates := []SmartRouteCandidate{
		{ModelName: "economy", ChannelID: 1, QualityTier: QualityEconomy, QualityScore: 0.42, Reliability: 1},
		{ModelName: "premium", ChannelID: 2, QualityTier: QualityPremium, QualityScore: 0.88, Reliability: 1},
	}

	ranked, rejections := RankCandidates(request, candidates, PolicyBalanced)

	require.Empty(t, rejections)
	require.Len(t, ranked, 2)
	factorsByModel := map[string]ScoreFactors{
		ranked[0].ModelName: ranked[0].ScoreFactors,
		ranked[1].ModelName: ranked[1].ScoreFactors,
	}
	assert.Greater(t, factorsByModel["economy"].TaskMatch, factorsByModel["premium"].TaskMatch)
}

func TestRankCandidatesAlwaysIsolatesOpenHealth(t *testing.T) {
	request := SmartRouteRequest{OriginalModel: "auto:balanced"}
	healthy := SmartRouteCandidate{
		ModelName: "healthy", ChannelID: 1, Reliability: 0.8, HealthState: ChannelHealthHealthy,
	}
	openSoon := SmartRouteCandidate{
		ModelName: "open-soon", ChannelID: 2, Reliability: 0.2, ResetWindowScore: 0.9, HealthState: ChannelHealthOpen,
	}
	openLater := SmartRouteCandidate{
		ModelName: "open-later", ChannelID: 3, Reliability: 0.2, ResetWindowScore: 0.2, HealthState: ChannelHealthOpen,
	}

	ranked, rejections := RankCandidates(request, []SmartRouteCandidate{openSoon, healthy}, PolicyReliabilityFirst)
	require.Len(t, ranked, 1)
	assert.Equal(t, "healthy", ranked[0].ModelName)
	require.Len(t, rejections, 1)
	assert.Contains(t, rejections[0].Reasons, "channel_health_open")

	ranked, rejections = RankCandidates(request, []SmartRouteCandidate{openLater, openSoon}, PolicyReliabilityFirst)
	assert.Empty(t, ranked)
	require.Len(t, rejections, 2)
	assert.Contains(t, rejections[0].Reasons, "channel_health_open")
}

func TestRankCandidatesAlwaysRejectsHardUnavailableChannel(t *testing.T) {
	ranked, rejections := RankCandidates(SmartRouteRequest{OriginalModel: "auto:balanced"}, []SmartRouteCandidate{{
		ModelName:       "no-active-key",
		ChannelID:       1,
		HealthState:     ChannelHealthOpen,
		HardUnavailable: true,
	}}, PolicyBalanced)

	assert.Empty(t, ranked)
	require.Len(t, rejections, 1)
	assert.Contains(t, rejections[0].Reasons, "channel_hard_unavailable")
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
