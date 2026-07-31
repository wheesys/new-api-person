package service

import (
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestContextConsensusDiagnosticsAggregatesOnlyValidatedFiniteReasons(t *testing.T) {
	accumulator := newContextConsensusDiagnosticsAccumulator(3600, 10800)
	logs := []model.SmartRoutingLogProjection{
		{CreatedAt: 3700, Other: `{"smart_routing":{"context_consensus":{"protocol":"openai","tool_compaction_diagnostic":{"schema_version":1,"status":"ready_for_sanitization","reason_codes":[]}}}}`},
		{CreatedAt: 3800, Other: `{"smart_routing":{"context_consensus":{"protocol":"openai","tool_compaction_diagnostic":{"schema_version":1,"status":"blocked","reason_codes":["tool_compaction_media_present","tool_compaction_provider_bound"]}}}}`},
		{CreatedAt: 7300, Other: `{"smart_routing":{"context_consensus":{"protocol":"claude","tool_compaction_diagnostic":{"schema_version":1,"status":"not_applicable","reason_codes":[]}}}}`},
		{CreatedAt: 7400, Other: `{"smart_routing":{"context_consensus":{"protocol":"openai"}}}`},
		{CreatedAt: 7500, Other: `{"smart_routing":{"context_consensus":{"protocol":"openai","tool_compaction_diagnostic":{"schema_version":1,"status":"blocked","reason_codes":["sensitive-unknown-reason"]}}}}`},
		{CreatedAt: 7600, Other: `{"smart_routing":{"context_consensus":{"protocol":"openai","tool_compaction_diagnostic":{"schema_version":1,"status":"blocked","reason_codes":["tool_compaction_media_present","tool_compaction_media_present"]}}}}`},
		{CreatedAt: 7700, Other: strings.Repeat("x", contextConsensusDiagnosticsMaximumLogBytes+1)},
	}
	for _, log := range logs {
		require.NoError(t, accumulator.add(log))
	}
	accumulator.result.DataQuality.MatchedLogs = int64(len(logs))

	result := accumulator.finish()
	assert.Equal(t, ContextConsensusDiagnosticsSchemaVersion, result.SchemaVersion)
	assert.Equal(t, "successful_smart_routing_consume_logs", result.DataScope)
	assert.Equal(t, int64(7), result.DataQuality.MatchedLogs)
	assert.Equal(t, int64(3), result.DataQuality.ValidDiagnostics)
	assert.Equal(t, int64(3), result.DataQuality.InvalidDiagnostics)
	assert.Equal(t, int64(1), result.DataQuality.LegacyLogs)
	assert.Equal(t, int64(1), result.DataQuality.OversizedLogs)
	assert.Equal(t, int64(1), result.Summary.NotApplicable)
	assert.Equal(t, int64(2), result.Summary.ToolContexts)
	assert.Equal(t, int64(1), result.Summary.ReadyForSanitization)
	assert.Equal(t, int64(1), result.Summary.Blocked)
	assert.Equal(t, 0.5, result.Summary.ReadyRate)
	assert.Equal(t, int64(2), result.Summary.ReasonOccurrences)
	require.Len(t, result.ByReasonCode, 2)
	assert.Equal(t, "tool_compaction_media_present", result.ByReasonCode[0].ReasonCode)
	assert.Equal(t, "tool_compaction_provider_bound", result.ByReasonCode[1].ReasonCode)
	require.Len(t, result.ByProtocol, 1)
	assert.Equal(t, "openai", result.ByProtocol[0].Protocol)
	require.Len(t, result.Timeline, 3)
	assert.Equal(t, int64(2), result.Timeline[0].ToolContexts)
	assert.Equal(t, int64(1), result.Timeline[0].ReadyForSanitization)
	assert.Equal(t, int64(1), result.Timeline[0].Blocked)

	encoded, err := common.Marshal(result)
	require.NoError(t, err)
	assert.NotContains(t, string(encoded), "sensitive-unknown-reason")
}

func TestContextConsensusDiagnosticsReturnsNonNilEmptyCollections(t *testing.T) {
	result := newContextConsensusDiagnosticsAccumulator(3600, 3600).finish()

	assert.NotNil(t, result.ByReasonCode)
	assert.NotNil(t, result.ByProtocol)
	assert.NotNil(t, result.Timeline)
	assert.Zero(t, result.Summary.ReadyRate)
}
