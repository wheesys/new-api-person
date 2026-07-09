package smartrouting

import (
	"testing"
	"time"

	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestModelRoutingProfileCacheRoundTrip(t *testing.T) {
	ClearModelRoutingProfileCache()
	t.Cleanup(ClearModelRoutingProfileCache)

	SetModelRoutingProfiles([]ModelRoutingProfile{
		{
			ModelName:        "gpt-5",
			ModelPattern:     "gpt-5",
			QualityScore:     0.91,
			CodingScore:      0.88,
			ReasoningScore:   0.86,
			SpeedScore:       0.42,
			CostScore:        0.31,
			ContextScore:     0.78,
			MaxContextTokens: 128000,
			SupportsTools:    true,
			UpdatedAt:        123,
		},
	})

	profile, ok := GetModelRoutingProfile("gpt-5")
	require.True(t, ok)
	assert.Equal(t, "gpt-5", profile.ModelName)
	assert.Equal(t, 0.91, profile.QualityScore)
	assert.True(t, profile.SupportsTools)

	_, ok = GetModelRoutingProfile("missing-model")
	assert.False(t, ok)
}

func TestBuildCandidatesUsesCachedModelRoutingProfileScores(t *testing.T) {
	ClearModelRoutingProfileCache()
	t.Cleanup(ClearModelRoutingProfileCache)
	SetModelRoutingProfiles([]ModelRoutingProfile{
		{
			ModelName:          "gpt-5-mini",
			QualityScore:       0.97,
			CodingScore:        0.93,
			ReasoningScore:     0.91,
			SpeedScore:         0.82,
			ContextScore:       0.88,
			MaxContextTokens:   256000,
			SupportsTools:      true,
			SupportsJSONSchema: true,
			SupportsReasoning:  true,
			SupportsStream:     true,
		},
	})

	candidates := BuildCandidatesFromSnapshots(SmartRouteRequest{
		UsingGroup:   "default",
		EndpointType: EndpointResponses,
		HasTools:     true,
	}, []model.Pricing{
		{
			ModelName:              "gpt-5-mini",
			ModelRatio:             1,
			CompletionRatio:        1,
			SupportedEndpointTypes: []constant.EndpointType{constant.EndpointTypeOpenAIResponse},
			EnableGroup:            []string{"default"},
		},
	}, []ChannelSnapshot{
		{
			ID:            5001,
			Group:         "default",
			Models:        "gpt-5-mini",
			Status:        1,
			ResponseTime:  80,
			EndpointTypes: []constant.EndpointType{constant.EndpointTypeOpenAIResponse},
		},
	})

	require.Len(t, candidates, 1)
	assert.Equal(t, 0.97, candidates[0].QualityScore)
	assert.True(t, candidates[0].SupportsReasoning)
	assert.Equal(t, 256000, candidates[0].MaxContextTokens)
}

func TestRankCandidatesUsesProfileCodingScoreForCodingTasks(t *testing.T) {
	request := SmartRouteRequest{
		OriginalModel: "auto:quality",
		TokenMeta: &types.TokenCountMeta{
			CombineText: "请调试这段代码并给出迁移方案",
		},
	}
	candidates := []SmartRouteCandidate{
		{ModelName: "general-model", ChannelID: 1, QualityScore: 0.95, CodingScore: 0.20, Reliability: 1, SupportsStream: true},
		{ModelName: "coding-model", ChannelID: 2, QualityScore: 0.70, CodingScore: 0.98, Reliability: 1, SupportsStream: true},
	}

	ranked, _ := RankCandidates(request, candidates, PolicyQualityFirst)

	require.Len(t, ranked, 2)
	assert.Equal(t, "coding-model", ranked[0].ModelName)
}

func TestParseAiderLeaderboardJSON(t *testing.T) {
	records, err := ParseAiderLeaderboard([]byte(`[
		{"model":"gpt-5","pass_rate":72.5,"cost":3.2,"seconds":45},
		{"model":"claude-sonnet-4","percent_correct":68,"cost_per_case":4.5,"seconds_per_case":52}
	]`))

	require.NoError(t, err)
	require.Len(t, records, 2)
	assert.Equal(t, "gpt-5", records[0].ModelName)
	assert.Equal(t, 72.5, records[0].PassRate)
	assert.Equal(t, 3.2, records[0].Cost)
	assert.Equal(t, 45.0, records[0].Seconds)
	assert.Equal(t, "claude-sonnet-4", records[1].ModelName)
	assert.Equal(t, 68.0, records[1].PassRate)
}

func TestParseAiderLeaderboardYAML(t *testing.T) {
	records, err := ParseAiderLeaderboard([]byte(`
- model: gpt-5
  pass_rate_2: 82.5
  total_cost: 4.25
  seconds_per_case: 31.5
`))

	require.NoError(t, err)
	require.Len(t, records, 1)
	assert.Equal(t, "gpt-5", records[0].ModelName)
	assert.Equal(t, 82.5, records[0].PassRate)
	assert.Equal(t, 4.25, records[0].Cost)
	assert.Equal(t, 31.5, records[0].Seconds)
}

func TestParseSWEBenchLeaderboardJSON(t *testing.T) {
	records, err := ParseSWEBenchLeaderboard([]byte(`[
		{"model":"swe-agent-model","resolved":64.5,"cost":2.4,"seconds":88}
	]`))

	require.NoError(t, err)
	require.Len(t, records, 1)
	assert.Equal(t, BenchmarkSourceSWEBench, records[0].Source)
	assert.Equal(t, "swe-agent-model", records[0].ModelName)
	assert.Equal(t, 64.5, records[0].PassRate)
	assert.Equal(t, 2.4, records[0].Cost)
	assert.Equal(t, 88.0, records[0].Seconds)
}

func TestParseArtificialAnalysisLeaderboardCSV(t *testing.T) {
	records, err := ParseArtificialAnalysisLeaderboard([]byte("Model,Intelligence Index,Output Speed,Blended Price\nquality-model,72.4,115,$1.25\n"))

	require.NoError(t, err)
	require.Len(t, records, 1)
	assert.Equal(t, BenchmarkSourceArtificialAnalysis, records[0].Source)
	assert.Equal(t, "quality-model", records[0].ModelName)
	assert.Equal(t, 72.4, records[0].PassRate)
	assert.Equal(t, 1.25, records[0].Cost)
	assert.Equal(t, 115.0, records[0].Seconds)
}

func TestParseArenaLeaderboardMarkdown(t *testing.T) {
	records, err := ParseArenaLeaderboard([]byte(`
| Model | Arena Score | Votes |
| --- | ---: | ---: |
| preference-model | 1287 | 42,000 |
`))

	require.NoError(t, err)
	require.Len(t, records, 1)
	assert.Equal(t, BenchmarkSourceArena, records[0].Source)
	assert.Equal(t, "preference-model", records[0].ModelName)
	assert.Equal(t, 1287.0, records[0].PassRate)
}

func TestBuildProfilesFromExternalBenchmarksCalculatesMultipleDimensions(t *testing.T) {
	profiles := BuildProfilesFromExternalBenchmarks([]ExternalBenchmarkRecord{
		{Source: BenchmarkSourceAider, ModelName: "fast-code", PassRate: 60, Cost: 1, Seconds: 10},
		{Source: BenchmarkSourceAider, ModelName: "slow-code", PassRate: 80, Cost: 4, Seconds: 40},
	}, 1000)

	require.Len(t, profiles, 2)
	fast, ok := findProfile(profiles, "fast-code")
	require.True(t, ok)
	slow, ok := findProfile(profiles, "slow-code")
	require.True(t, ok)
	assert.Greater(t, slow.CodingScore, fast.CodingScore)
	assert.Greater(t, fast.SpeedScore, slow.SpeedScore)
	assert.Greater(t, fast.CostScore, slow.CostScore)
	assert.Equal(t, int64(1000), fast.UpdatedAt)
}

func TestBuildProfilesFromExternalBenchmarksMergesSourcesByModel(t *testing.T) {
	profiles := BuildProfilesFromExternalBenchmarks([]ExternalBenchmarkRecord{
		{Source: BenchmarkSourceAider, ModelName: "shared-model", PassRate: 60, Cost: 2, Seconds: 40},
		{Source: BenchmarkSourceSWEBench, ModelName: "shared-model", PassRate: 75, Cost: 3, Seconds: 50},
		{Source: BenchmarkSourceArena, ModelName: "shared-model", PassRate: 1200},
	}, 1000)

	require.Len(t, profiles, 1)
	profile := profiles[0]
	assert.Equal(t, "shared-model", profile.ModelName)
	assert.ElementsMatch(t, []string{BenchmarkSourceAider, BenchmarkSourceSWEBench, BenchmarkSourceArena}, profile.Sources)
	assert.Contains(t, profile.SourceScores, BenchmarkSourceAider)
	assert.Contains(t, profile.SourceScores, BenchmarkSourceSWEBench)
	assert.Contains(t, profile.SourceScores, BenchmarkSourceArena)
	assert.Greater(t, profile.CodingScore, 0.0)
	assert.Greater(t, profile.GeneralScore, 0.0)
	assert.Greater(t, profile.PreferenceScore, 0.0)
}

func TestBuildProfilesFromExternalBenchmarksNormalizesScoresWithinEachSource(t *testing.T) {
	profiles := BuildProfilesFromExternalBenchmarks([]ExternalBenchmarkRecord{
		{Source: BenchmarkSourceAider, ModelName: "top-model", PassRate: 80},
		{Source: BenchmarkSourceAider, ModelName: "second-model", PassRate: 40},
		{Source: BenchmarkSourceArena, ModelName: "top-model", PassRate: 1200},
		{Source: BenchmarkSourceArena, ModelName: "second-model", PassRate: 600},
	}, 1000)

	top, ok := findProfile(profiles, "top-model")
	require.True(t, ok)
	second, ok := findProfile(profiles, "second-model")
	require.True(t, ok)
	assert.Equal(t, 1.0, top.SourceScores[BenchmarkSourceAider])
	assert.Equal(t, 1.0, top.SourceScores[BenchmarkSourceArena])
	assert.Equal(t, 0.5, second.SourceScores[BenchmarkSourceAider])
	assert.Equal(t, 0.5, second.SourceScores[BenchmarkSourceArena])
}

func TestSmartRoutingProfileTaskIntervals(t *testing.T) {
	assert.Equal(t, 10*24*time.Hour, ExternalBenchmarkRefreshInterval())
	assert.Equal(t, 24*time.Hour, ModelProfileRefreshInterval())
}

func TestRefreshModelRoutingProfilesUsesExternalBenchmarkCache(t *testing.T) {
	ClearModelRoutingProfileCache()
	t.Cleanup(ClearModelRoutingProfileCache)
	SetExternalBenchmarkRecords([]ExternalBenchmarkRecord{
		{Source: BenchmarkSourceAider, ModelName: "cached-code-model", PassRate: 75, Cost: 2, Seconds: 30},
	}, 2000)

	result, err := RefreshModelRoutingProfiles(t.Context())

	require.NoError(t, err)
	assert.Equal(t, 1, result.ProfileCount)
	profile, ok := GetModelRoutingProfile("cached-code-model")
	require.True(t, ok)
	assert.Equal(t, int64(2000), profile.UpdatedAt)
	assert.Equal(t, []string{BenchmarkSourceAider}, profile.Sources)
}

func findProfile(profiles []ModelRoutingProfile, modelName string) (ModelRoutingProfile, bool) {
	for _, profile := range profiles {
		if profile.ModelName == modelName {
			return profile, true
		}
	}
	return ModelRoutingProfile{}, false
}
