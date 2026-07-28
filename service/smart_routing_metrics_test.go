package service

import (
	"encoding/json"
	"testing"

	"github.com/QuantumNous/new-api/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSmartRoutingMetricsAccumulatorAggregatesValidAndLegacyDecisions(t *testing.T) {
	accumulator := newSmartRoutingMetricsAccumulator(3600, 10800)
	currentLog := `{"smart_routing":{"schema_version":1,"enabled":true,"policy":"balanced","complexity":"standard","task_type":"general","original_model":"auto:balanced","selected_model":"selected-a","selected_channel_id":11,"selected_health":"healthy","candidate_count":2,"fallback_index":0,"score_factors":{"cost":0.2,"reliability":0.9,"latency":0.6,"throughput":0.5,"quality":0.7,"task_match":0.8,"context":0.6,"cache":0.5,"reset_window":1,"affinity":0.4}}}`
	legacyLog := `{"smart_routing":{"enabled":true,"policy":"quality_first","complexity":"complex","task_type":"coding","original_model":"auto:quality","selected_model":"selected-b","selected_channel_id":12,"selected_health":"degraded","candidate_count":3,"fallback_index":1,"score_factors":{"cost":0.4,"reliability":0.7,"latency":0.2,"throughput":0.3,"quality":1,"task_match":0.9,"context":0.8,"cache":0.7,"reset_window":0.5,"affinity":0.6}}}`
	invalidLog := `{"smart_routing":{"schema_version":1,"enabled":true,"policy":"balanced","complexity":"standard","task_type":"general","original_model":"auto:balanced","selected_model":"selected-c","selected_channel_id":99,"selected_health":"healthy","candidate_count":1,"fallback_index":0,"score_factors":{"cost":0.5,"reliability":0.5,"latency":0.5,"throughput":0.5,"quality":0.5,"task_match":0.5,"context":0.5,"cache":0.5,"reset_window":0.5,"affinity":0.5}}}`

	require.NoError(t, accumulator.add(model.SmartRoutingLogProjection{CreatedAt: 3700, ChannelID: 11, ModelName: "selected-a", Other: currentLog}))
	require.NoError(t, accumulator.add(model.SmartRoutingLogProjection{CreatedAt: 7300, ChannelID: 12, ModelName: "selected-b", Other: legacyLog}))
	require.NoError(t, accumulator.add(model.SmartRoutingLogProjection{CreatedAt: 7400, ChannelID: 13, ModelName: "selected-c", Other: invalidLog}))
	accumulator.result.DataQuality.MatchedLogs = 3

	result := accumulator.finish()
	assert.Equal(t, SmartRoutingMetricsSchemaVersion, result.SchemaVersion)
	assert.Equal(t, int64(3), result.DataQuality.MatchedLogs)
	assert.Equal(t, int64(2), result.DataQuality.ValidDecisions)
	assert.Equal(t, int64(1), result.DataQuality.InvalidDecisions)
	assert.Equal(t, int64(1), result.DataQuality.LegacyDecisions)
	assert.Equal(t, int64(2), result.Summary.SuccessfulDecisions)
	assert.Equal(t, int64(1), result.Summary.FallbackDecisions)
	assert.Equal(t, 0.5, result.Summary.FallbackRate)
	assert.Equal(t, int64(1), result.Summary.FallbackHopSum)
	assert.Equal(t, 2.5, result.Summary.AverageCandidateCount)
	assert.Equal(t, 0.3, result.AverageScoreFactors.Cost)
	assert.Equal(t, 0.8, result.AverageScoreFactors.Reliability)
	require.Len(t, result.ByPolicy, 2)
	assert.Equal(t, "balanced", result.ByPolicy[0].Name)
	require.Len(t, result.BySelectedChannel, 2)
	assert.Equal(t, 11, result.BySelectedChannel[0].ChannelID)
	require.Len(t, result.Timeline, 3)
	assert.Equal(t, int64(1), result.Timeline[0].SuccessfulDecisions)
	assert.Equal(t, int64(1), result.Timeline[1].FallbackDecisions)
}

func TestValidateSmartRoutingMetricsDecisionRejectsInvalidScore(t *testing.T) {
	enabled := true
	channelID := 1
	candidateCount := 1
	fallbackIndex := 0
	validScore := 0.5
	invalidScore := 1.1
	decision := &smartRoutingMetricsDecisionLog{
		SchemaVersion:     json.RawMessage("1"),
		Enabled:           &enabled,
		Policy:            "balanced",
		Complexity:        "standard",
		TaskType:          "general",
		OriginalModel:     "auto:balanced",
		SelectedModel:     "selected-a",
		SelectedChannelID: &channelID,
		SelectedHealth:    "healthy",
		CandidateCount:    &candidateCount,
		FallbackIndex:     &fallbackIndex,
		ScoreFactors: &smartRoutingMetricsScoreFactors{
			Cost:        &invalidScore,
			Reliability: &validScore,
			Latency:     &validScore,
			Throughput:  &validScore,
			Quality:     &validScore,
			TaskMatch:   &validScore,
			Context:     &validScore,
			Cache:       &validScore,
			ResetWindow: &validScore,
			Affinity:    &validScore,
		},
	}

	legacy, valid := validateSmartRoutingMetricsDecision(decision, model.SmartRoutingLogProjection{ChannelID: 1, ModelName: "selected-a"})
	assert.False(t, legacy)
	assert.False(t, valid)

	decision.ScoreFactors.Cost = &validScore
	decision.SchemaVersion = json.RawMessage("null")
	legacy, valid = validateSmartRoutingMetricsDecision(decision, model.SmartRoutingLogProjection{ChannelID: 1, ModelName: "selected-a"})
	assert.False(t, legacy)
	assert.False(t, valid)

	decision.SchemaVersion = nil
	legacy, valid = validateSmartRoutingMetricsDecision(decision, model.SmartRoutingLogProjection{ChannelID: 1, ModelName: "selected-a"})
	assert.True(t, legacy)
	assert.True(t, valid)
}
