package model_setting

import (
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/setting/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func setSmartRoutingVirtualModelPoolsForTest(t *testing.T, pools map[string][]string) {
	t.Helper()

	encodedPools, err := common.Marshal(pools)
	require.NoError(t, err)
	require.NoError(t, config.GlobalConfig.LoadFromDB(map[string]string{
		"smart_routing.virtual_model_pools": string(encodedPools),
	}))
}

func TestSmartRoutingVirtualModelPoolsLoadFromConfig(t *testing.T) {
	originalPools := GetSmartRoutingSettings().VirtualModelPools
	t.Cleanup(func() {
		setSmartRoutingVirtualModelPoolsForTest(t, originalPools)
	})

	require.NoError(t, config.GlobalConfig.LoadFromDB(map[string]string{
		"smart_routing.virtual_model_pools": `{"auto:quality":["gpt-5","claude-opus-4"],"auto:cheap":["gpt-5-mini"]}`,
	}))

	qualityPool, qualityConfigured := GetSmartRoutingVirtualModelPool("auto:quality")
	assert.True(t, qualityConfigured)
	assert.Equal(t, []string{"gpt-5", "claude-opus-4"}, qualityPool)
	smartQualityPool, smartQualityConfigured := GetSmartRoutingVirtualModelPool("smart:quality")
	assert.True(t, smartQualityConfigured)
	assert.Equal(t, []string{"gpt-5", "claude-opus-4"}, smartQualityPool)
	cheapPool, cheapConfigured := GetSmartRoutingVirtualModelPool("auto:cheap")
	assert.True(t, cheapConfigured)
	assert.Equal(t, []string{"gpt-5-mini"}, cheapPool)
	balancedPool, balancedConfigured := GetSmartRoutingVirtualModelPool("auto:balanced")
	assert.False(t, balancedConfigured)
	assert.Empty(t, balancedPool)
}

func TestSmartRoutingVirtualModelPoolReturnsCopy(t *testing.T) {
	originalPools := GetSmartRoutingSettings().VirtualModelPools
	t.Cleanup(func() {
		setSmartRoutingVirtualModelPoolsForTest(t, originalPools)
	})

	setSmartRoutingVirtualModelPoolsForTest(t, map[string][]string{
		"auto:quality": {"gpt-5"},
	})

	pool, configured := GetSmartRoutingVirtualModelPool("auto:quality")
	require.True(t, configured)
	require.Equal(t, []string{"gpt-5"}, pool)
	pool[0] = "mutated"

	unchangedPool, unchangedConfigured := GetSmartRoutingVirtualModelPool("auto:quality")
	assert.True(t, unchangedConfigured)
	assert.Equal(t, []string{"gpt-5"}, unchangedPool)
}

func TestSmartRoutingVirtualModelPoolFailsClosedForLegacyInvalidPool(t *testing.T) {
	originalPools := GetSmartRoutingSettings().VirtualModelPools
	t.Cleanup(func() {
		setSmartRoutingVirtualModelPoolsForTest(t, originalPools)
	})

	require.NoError(t, config.GlobalConfig.LoadFromDB(map[string]string{
		"smart_routing.virtual_model_pools": `{"auto:quality":null}`,
	}))

	pool, configured := GetSmartRoutingVirtualModelPool("auto:quality")
	assert.True(t, configured)
	assert.Empty(t, pool)
}

func TestValidateSmartRoutingVirtualModelPools(t *testing.T) {
	tests := []struct {
		name      string
		value     string
		wantError string
	}{
		{name: "valid", value: `{"auto:quality":["gpt-5"],"auto:fast":[]}`},
		{name: "empty object", value: `{}`},
		{name: "invalid json", value: `{`, wantError: "invalid smart routing virtual model pools"},
		{name: "null", value: `null`, wantError: "must be a JSON object"},
		{name: "non-array pool", value: `{"auto:quality":"gpt-5"}`, wantError: "invalid smart routing virtual model pools"},
		{name: "null pool", value: `{"auto:quality":null}`, wantError: "must be an array"},
		{name: "blank model name", value: `{"auto:quality":[" "]}`, wantError: "contains an empty model name"},
		{name: "unsupported key", value: `{"smart:quality":["gpt-5"]}`, wantError: "unsupported smart routing virtual model key"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := ValidateSmartRoutingVirtualModelPools(test.value)
			if test.wantError == "" {
				require.NoError(t, err)
				return
			}
			require.ErrorContains(t, err, test.wantError)
		})
	}
}

func TestValidateSmartRoutingCompactionModelPool(t *testing.T) {
	tests := []struct {
		name      string
		value     string
		wantError string
	}{
		{name: "explicit models", value: `["gpt-5-mini","claude-haiku-4"]`},
		{name: "empty pool", value: `[]`},
		{name: "invalid json", value: `{`, wantError: "invalid smart routing compaction model pool"},
		{name: "null", value: `null`, wantError: "must be an array"},
		{name: "blank model", value: `[" "]`, wantError: "empty model name"},
		{name: "auto alias", value: `["auto"]`, wantError: "explicit real models"},
		{name: "smart virtual model", value: `["smart:quality"]`, wantError: "explicit real models"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := ValidateSmartRoutingCompactionModelPool(test.value)
			if test.wantError == "" {
				require.NoError(t, err)
				return
			}
			require.ErrorContains(t, err, test.wantError)
		})
	}
}

func TestSmartRoutingCompactionSettingsUseImmutableSnapshot(t *testing.T) {
	original := GetSmartRoutingSettings()
	t.Cleanup(func() {
		smartRoutingSettings = *cloneSmartRoutingSettings(original)
		smartRoutingSettings.OnConfigUpdated()
	})

	require.NoError(t, config.GlobalConfig.LoadFromDB(map[string]string{
		"smart_routing.context_consensus_enabled":        "true",
		"smart_routing.auto_compaction_enabled":          "true",
		"smart_routing.compaction_model_pool":            `["gpt-5-mini"]`,
		"smart_routing.context_safety_margin_tokens":     "2048",
		"smart_routing.preserved_recent_turns":           "4",
		"smart_routing.max_summary_tokens":               "1024",
		"smart_routing.max_compaction_input_tokens":      "64000",
		"smart_routing.max_compaction_calls_per_request": "1",
		"smart_routing.max_compaction_quota":             "5000",
		"smart_routing.compaction_timeout_seconds":       "15",
	}))

	snapshot := GetSmartRoutingSettings()
	require.True(t, snapshot.ContextConsensusEnabled)
	require.True(t, snapshot.AutoCompactionEnabled)
	assert.Equal(t, []string{"gpt-5-mini"}, snapshot.CompactionModelPool)
	assert.Equal(t, 2048, snapshot.ContextSafetyMarginTokens)
	assert.Equal(t, 4, snapshot.PreservedRecentTurns)
	assert.Equal(t, 1024, snapshot.MaxSummaryTokens)
	assert.Equal(t, 64000, snapshot.MaxCompactionInputTokens)
	assert.Equal(t, 1, snapshot.MaxCompactionCallsPerRequest)
	assert.Equal(t, 5000, snapshot.MaxCompactionQuota)
	assert.Equal(t, 15, snapshot.CompactionTimeoutSeconds)

	snapshot.CompactionModelPool[0] = "mutated"
	assert.Equal(t, []string{"gpt-5-mini"}, GetSmartRoutingSettings().CompactionModelPool)
}
