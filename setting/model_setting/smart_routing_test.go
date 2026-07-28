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

func TestValidateSmartRoutingCompactionChannelIDs(t *testing.T) {
	tests := []struct {
		name      string
		value     string
		wantError string
	}{
		{name: "explicit channels", value: `[3,7]`},
		{name: "empty whitelist", value: `[]`},
		{name: "invalid json", value: `{`, wantError: "invalid smart routing compaction channel IDs"},
		{name: "null", value: `null`, wantError: "must be an array"},
		{name: "non-positive", value: `[0]`, wantError: "must be positive"},
		{name: "duplicate", value: `[3,3]`, wantError: "is duplicated"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := ValidateSmartRoutingCompactionChannelIDs(test.value)
			if test.wantError == "" {
				require.NoError(t, err)
				return
			}
			require.ErrorContains(t, err, test.wantError)
		})
	}
}

func TestValidateSmartRoutingAuthoritativeContextLimits(t *testing.T) {
	valid := `{"gpt-5":{"max_context_tokens":128000,"version":"2026-07","channel_ids":[3],"relay_formats":["openai","openai_responses"]}}`
	tests := []struct {
		name      string
		value     string
		wantError string
	}{
		{name: "explicit authoritative limit", value: valid},
		{name: "empty map", value: `{}`},
		{name: "invalid json", value: `{`, wantError: "invalid smart routing authoritative context limits"},
		{name: "null", value: `null`, wantError: "must be a JSON object"},
		{name: "blank model", value: `{" ":{"max_context_tokens":1,"version":"v1","channel_ids":[1],"relay_formats":["openai"]}}`, wantError: "empty model name"},
		{name: "non-positive limit", value: `{"gpt-5":{"max_context_tokens":0,"version":"v1","channel_ids":[1],"relay_formats":["openai"]}}`, wantError: "must be positive"},
		{name: "missing version", value: `{"gpt-5":{"max_context_tokens":1,"channel_ids":[1],"relay_formats":["openai"]}}`, wantError: "requires a version"},
		{name: "missing channel", value: `{"gpt-5":{"max_context_tokens":1,"version":"v1","relay_formats":["openai"]}}`, wantError: "requires channel IDs"},
		{name: "duplicate channel", value: `{"gpt-5":{"max_context_tokens":1,"version":"v1","channel_ids":[1,1],"relay_formats":["openai"]}}`, wantError: "duplicate channel ID"},
		{name: "missing protocol", value: `{"gpt-5":{"max_context_tokens":1,"version":"v1","channel_ids":[1]}}`, wantError: "requires relay formats"},
		{name: "unsupported protocol", value: `{"gpt-5":{"max_context_tokens":1,"version":"v1","channel_ids":[1],"relay_formats":["openai_realtime"]}}`, wantError: "unsupported relay format"},
		{name: "duplicate protocol", value: `{"gpt-5":{"max_context_tokens":1,"version":"v1","channel_ids":[1],"relay_formats":["openai","openai"]}}`, wantError: "duplicate relay format"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := ValidateSmartRoutingAuthoritativeContextLimits(test.value)
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
		"smart_routing.managed_context_enabled":          "true",
		"smart_routing.compaction_model_pool":            `["gpt-5-mini"]`,
		"smart_routing.compaction_channel_ids":           `[3,7]`,
		"smart_routing.authoritative_context_limits":     `{"gpt-5-mini":{"max_context_tokens":128000,"version":"2026-07","channel_ids":[3],"relay_formats":["openai"]}}`,
		"smart_routing.context_safety_margin_tokens":     "2048",
		"smart_routing.preserved_recent_turns":           "4",
		"smart_routing.max_summary_tokens":               "1024",
		"smart_routing.max_compaction_input_tokens":      "64000",
		"smart_routing.max_compaction_calls_per_request": "1",
		"smart_routing.max_compaction_quota":             "5000",
		"smart_routing.compaction_timeout_seconds":       "15",
		"smart_routing.context_state_ttl_seconds":        "7200",
	}))

	snapshot := GetSmartRoutingSettings()
	require.True(t, snapshot.ContextConsensusEnabled)
	require.True(t, snapshot.AutoCompactionEnabled)
	require.True(t, snapshot.ManagedContextEnabled)
	assert.Equal(t, []string{"gpt-5-mini"}, snapshot.CompactionModelPool)
	assert.Equal(t, []int{3, 7}, snapshot.CompactionChannelIDs)
	assert.Equal(t, 128000, snapshot.AuthoritativeContextLimits["gpt-5-mini"].MaxContextTokens)
	assert.Equal(t, 2048, snapshot.ContextSafetyMarginTokens)
	assert.Equal(t, 4, snapshot.PreservedRecentTurns)
	assert.Equal(t, 1024, snapshot.MaxSummaryTokens)
	assert.Equal(t, 64000, snapshot.MaxCompactionInputTokens)
	assert.Equal(t, 1, snapshot.MaxCompactionCallsPerRequest)
	assert.Equal(t, 5000, snapshot.MaxCompactionQuota)
	assert.Equal(t, 15, snapshot.CompactionTimeoutSeconds)
	assert.Equal(t, 7200, snapshot.ContextStateTTLSeconds)

	snapshot.CompactionModelPool[0] = "mutated"
	snapshot.CompactionChannelIDs[0] = 99
	limit := snapshot.AuthoritativeContextLimits["gpt-5-mini"]
	limit.ChannelIDs[0] = 99
	snapshot.AuthoritativeContextLimits["gpt-5-mini"] = limit
	assert.Equal(t, []string{"gpt-5-mini"}, GetSmartRoutingSettings().CompactionModelPool)
	assert.Equal(t, []int{3, 7}, GetSmartRoutingSettings().CompactionChannelIDs)
	assert.Equal(t, []int{3}, GetSmartRoutingSettings().AuthoritativeContextLimits["gpt-5-mini"].ChannelIDs)
}
