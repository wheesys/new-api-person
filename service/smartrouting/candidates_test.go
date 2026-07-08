package smartrouting

import (
	"testing"

	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBuildCandidatesFromSnapshotsFiltersGroupAndEndpoint(t *testing.T) {
	priceRatio := 0.5
	channels := []ChannelSnapshot{
		{
			ID:            10,
			Group:         "default,pro",
			Models:        "gpt-5-mini,claude-3-7-sonnet",
			Status:        1,
			ResponseTime:  250,
			PriceRatio:    &priceRatio,
			EndpointTypes: []constant.EndpointType{constant.EndpointTypeOpenAI, constant.EndpointTypeOpenAIResponse},
		},
		{
			ID:            11,
			Group:         "pro",
			Models:        "gemini-2.5-flash",
			Status:        1,
			ResponseTime:  120,
			EndpointTypes: []constant.EndpointType{constant.EndpointTypeGemini},
		},
	}

	candidates := BuildCandidatesFromSnapshots(SmartRouteRequest{
		UsingGroup:            "default",
		EndpointType:          EndpointResponses,
		EstimatedPromptTokens: 1000,
		MaxOutputTokens:       500,
	}, []model.Pricing{
		{
			ModelName:              "gpt-5-mini",
			ModelRatio:             2,
			CompletionRatio:        3,
			SupportedEndpointTypes: []constant.EndpointType{constant.EndpointTypeOpenAI, constant.EndpointTypeOpenAIResponse},
			EnableGroup:            []string{"default"},
		},
		{
			ModelName:              "gemini-2.5-flash",
			ModelRatio:             1,
			CompletionRatio:        1,
			SupportedEndpointTypes: []constant.EndpointType{constant.EndpointTypeGemini},
			EnableGroup:            []string{"pro"},
		},
	}, channels)

	require.Len(t, candidates, 1)
	assert.Equal(t, "gpt-5-mini", candidates[0].ModelName)
	assert.Equal(t, 10, candidates[0].ChannelID)
	assert.Equal(t, QualityPremium, candidates[0].QualityTier)
	assert.Equal(t, 2500, candidates[0].EstimatedQuota)
	assert.True(t, candidates[0].SupportsTools)
	assert.True(t, candidates[0].SupportsJSONSchema)
	assert.True(t, candidates[0].SupportsStream)
	assert.InDelta(t, 0.75, candidates[0].LatencyScore, 0.001)
}

func TestInferModelCapabilitiesFromModelNameAndEndpoint(t *testing.T) {
	claude := InferModelCapabilities("claude-3-7-sonnet-20250219-thinking", []constant.EndpointType{constant.EndpointTypeAnthropic})
	assert.Equal(t, QualityReasoning, claude.QualityTier)
	assert.True(t, claude.SupportsTools)
	assert.True(t, claude.SupportsReasoning)
	assert.True(t, claude.SupportsStream)

	image := InferModelCapabilities("gpt-image-1", []constant.EndpointType{constant.EndpointTypeImageGeneration})
	assert.Equal(t, QualityPremium, image.QualityTier)
	assert.True(t, image.SupportsVision)
	assert.False(t, image.SupportsTools)

	rerank := InferModelCapabilities("jina-reranker-v2-base-multilingual", []constant.EndpointType{constant.EndpointTypeJinaRerank})
	assert.Equal(t, QualityStandard, rerank.QualityTier)
	assert.False(t, rerank.SupportsTools)
}

func TestNewChannelSnapshotCopiesRoutingFields(t *testing.T) {
	priceRatio := 0.25
	weight := uint(80)
	channel := &model.Channel{
		Id:           42,
		Type:         0,
		Group:        "default,pro",
		Models:       "gpt-5-mini",
		Status:       1,
		ResponseTime: 80,
		Weight:       &weight,
		PriceRatio:   &priceRatio,
	}

	snapshot := NewChannelSnapshot(channel)

	assert.Equal(t, 42, snapshot.ID)
	assert.Equal(t, "default,pro", snapshot.Group)
	assert.Equal(t, "gpt-5-mini", snapshot.Models)
	assert.Equal(t, 1, snapshot.Status)
	assert.Equal(t, 80, snapshot.ResponseTime)
	assert.Equal(t, 80, snapshot.Weight)
	require.NotNil(t, snapshot.PriceRatio)
	assert.Equal(t, 0.25, *snapshot.PriceRatio)
	assert.Contains(t, snapshot.EndpointTypes, constant.EndpointTypeOpenAI)
}
