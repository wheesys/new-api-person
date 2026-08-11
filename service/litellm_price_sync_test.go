package service

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestComputeLitellmModelRatioMerge(t *testing.T) {
	f := func(v float64) *float64 { return &v }

	t.Run("covers default and unconfigured models", func(t *testing.T) {
		current := map[string]float64{
			"gpt-4o":    1.25, // 等于默认，未手填
			"custom-llm": 9.0, // 代码默认表里没有，视为手填
		}
		defaults := map[string]float64{"gpt-4o": 1.25}
		entries := map[string]litellmPriceEntry{
			"gpt-4o":        {InputCostPerToken: f(2.5e-6)}, // 2.5e-6 * 500000 = 1.25
			"claude-opus-4": {InputCostPerToken: f(3e-6)},  // 1.5
		}
		merged, updated := computeLitellmModelRatioMerge(current, defaults, entries)
		require.Equal(t, 2, updated)
		assert.InDelta(t, 1.25, merged["gpt-4o"], 1e-9)
		assert.InDelta(t, 1.5, merged["claude-opus-4"], 1e-9)
		// 自定义模型保留，不被覆盖。
		assert.Equal(t, 9.0, merged["custom-llm"])
	})

	t.Run("does not overwrite hand-filled price", func(t *testing.T) {
		current := map[string]float64{
			"gpt-4o": 3.0, // 手填，≠ 默认 1.25
		}
		defaults := map[string]float64{"gpt-4o": 1.25}
		entries := map[string]litellmPriceEntry{
			"gpt-4o": {InputCostPerToken: f(2.5e-6)},
		}
		merged, updated := computeLitellmModelRatioMerge(current, defaults, entries)
		require.Equal(t, 0, updated)
		assert.Equal(t, 3.0, merged["gpt-4o"])
	})

	t.Run("skips non-token and provider-prefixed entries", func(t *testing.T) {
		entries := map[string]litellmPriceEntry{
			"dall-e-3":      {InputCostPerToken: nil},      // 图片按像素，无 token 单价
			"azure/gpt-4o":  {InputCostPerToken: f(2e-6)},  // provider 前缀
			"gpt-4o":        {InputCostPerToken: f(2.5e-6)},
		}
		merged, updated := computeLitellmModelRatioMerge(nil, nil, entries)
		require.Equal(t, 1, updated)
		_, ok := merged["dall-e-3"]
		assert.False(t, ok)
		_, ok = merged["azure/gpt-4o"]
		assert.False(t, ok)
		assert.InDelta(t, 1.25, merged["gpt-4o"], 1e-9)
	})
}
