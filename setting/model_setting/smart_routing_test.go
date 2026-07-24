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
