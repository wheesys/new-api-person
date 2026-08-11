package service

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/logger"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/setting/ratio_setting"

	"github.com/bytedance/gopkg/util/gopool"
)

// LiteLLM 维护的模型价格数据（美元/token）。社区数据源，覆盖各主流模型，
// 作为「页面手填 > LiteLLM 社区默认 > 代码默认表」三层价格中的第二层。
const (
	litellmPriceURL        = "https://raw.githubusercontent.com/BerriAI/litellm/main/litellm/model_prices_and_context_window.json"
	litellmPriceBackupURL  = "https://raw.githubusercontent.com/BerriAI/litellm/main/litellm/model_prices_and_context_window_backup.json"
	litellmPriceSyncTimeout = 60 * time.Second

	// modelRatio = input_cost_per_token(美元/token) * 500_000。
	// 依据 setting/ratio_setting/model_ratio.go：1 ratio = $0.002/1K tokens = $2/1M tokens，
	// 故 ratio = ($/1M) / 2 = input_cost_per_token * 1_000_000 / 2。
	litellmRatioPerDollarPerToken = 500_000
)

var (
	litellmPriceSyncOnce     sync.Once
	litellmPriceSyncRunning  atomic.Bool
	litellmPriceSyncInterval = common.GetEnvOrDefault("LITELLM_PRICE_SYNC_INTERVAL", 1440) // 分钟，默认 24h
)

// litellmPriceEntry 是 LiteLLM 价格 JSON 中单个模型条目的相关字段。
// 统一使用 *float64，缺失的字段为 nil。
type litellmPriceEntry struct {
	InputCostPerToken  *float64 `json:"input_cost_per_token"`
	OutputCostPerToken *float64 `json:"output_cost_per_token"`
	Mode               string   `json:"mode"`
	LitellmProvider    string   `json:"litellm_provider"`
}

// StartLitellmPriceSync 启动周期性同步 LiteLLM 社区价格到系统默认价目表的任务。
func StartLitellmPriceSync() {
	litellmPriceSyncOnce.Do(func() {
		if !common.IsMasterNode {
			return
		}
		if litellmPriceSyncInterval <= 0 {
			logger.LogInfo(context.Background(), "litellm price sync disabled (interval<=0)")
			return
		}
		gopool.Go(func() {
			logger.LogInfo(context.Background(), fmt.Sprintf("litellm price sync started: interval=%dm", litellmPriceSyncInterval))
			ticker := time.NewTicker(time.Duration(litellmPriceSyncInterval) * time.Minute)
			defer ticker.Stop()

			syncLitellmPricesOnce()
			for range ticker.C {
				syncLitellmPricesOnce()
			}
		})
	})
}

func syncLitellmPricesOnce() {
	if !litellmPriceSyncRunning.CompareAndSwap(false, true) {
		return
	}
	defer litellmPriceSyncRunning.Store(false)
	if err := syncLitellmPrices(); err != nil {
		logger.LogWarn(context.Background(), fmt.Sprintf("litellm price sync failed: %v", err))
	}
}

func syncLitellmPrices() error {
	entries, err := fetchLitellmPrices()
	if err != nil {
		return err
	}
	mergeLitellmModelRatio(entries)
	return nil
}

// fetchLitellmPrices 拉取并解析 LiteLLM 价格 JSON，返回按模型名分组的条目。
func fetchLitellmPrices() (map[string]litellmPriceEntry, error) {
	body, err := fetchLitellmRaw(litellmPriceURL)
	if err != nil {
		body, err = fetchLitellmRaw(litellmPriceBackupURL)
		if err != nil {
			return nil, err
		}
	}
	var entries map[string]litellmPriceEntry
	if err := common.Unmarshal(body, &entries); err != nil {
		return nil, fmt.Errorf("unmarshal litellm prices: %w", err)
	}
	return entries, nil
}

func fetchLitellmRaw(url string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), litellmPriceSyncTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "new-api-litellm-price-sync")
	resp, err := GetHttpClient().Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		io.Copy(io.Discard, resp.Body)
		return nil, fmt.Errorf("fetch %s: unexpected status %d", url, resp.StatusCode)
	}
	return io.ReadAll(resp.Body)
}

// mergeLitellmModelRatio 把 LiteLLM 的 token 输入倍率合并进系统 ModelRatio option，
// 仅覆盖「当前值等于代码默认值或未配置」的模型，绝不覆盖管理员手动填写的价格。
func mergeLitellmModelRatio(entries map[string]litellmPriceEntry) {
	current := ratio_setting.GetModelRatioCopy()
	defaults := ratio_setting.GetDefaultModelRatioMap()

	merged, updated := computeLitellmModelRatioMerge(current, defaults, entries)

	if updated == 0 {
		logger.LogInfo(context.Background(), "litellm price sync: no model ratio updated")
		return
	}
	jsonStr, err := common.Marshal(merged)
	if err != nil {
		logger.LogWarn(context.Background(), fmt.Sprintf("litellm price sync: marshal merged ratio failed: %v", err))
		return
	}
	if err := model.UpdateOption("ModelRatio", string(jsonStr)); err != nil {
		logger.LogWarn(context.Background(), fmt.Sprintf("litellm price sync: update ModelRatio option failed: %v", err))
		return
	}
	logger.LogInfo(context.Background(), fmt.Sprintf("litellm price sync: updated %d model ratios", updated))
}

// computeLitellmModelRatioMerge 计算把 LiteLLM 倍率合并进 ModelRatio 后的完整表。
// 仅覆盖「当前值等于代码默认值或未配置」的模型，绝不覆盖手填（当前值 ≠ 默认值）。
// 返回合并后的表与本次新增/更新的模型数。纯函数，便于单测。
func computeLitellmModelRatioMerge(current, defaults map[string]float64, entries map[string]litellmPriceEntry) (map[string]float64, int) {
	merged := make(map[string]float64, len(current)+len(entries))
	for name, ratio := range current {
		merged[name] = ratio
	}

	updated := 0
	for name, entry := range entries {
		// 仅同步按 token 计费的聊天/补全模型；跳过图片/音频按像素/秒的产品及带 provider 前缀的条目。
		if entry.InputCostPerToken == nil || *entry.InputCostPerToken <= 0 {
			continue
		}
		if strings.Contains(name, "/") {
			continue
		}
		ratio := *entry.InputCostPerToken * litellmRatioPerDollarPerToken
		if ratio <= 0 {
			continue
		}
		// 已手填（当前值不等于代码默认值）则跳过。
		if cur, ok := current[name]; ok {
			if defaultRatio, hasDefault := defaults[name]; hasDefault && cur != defaultRatio {
				continue
			}
		}
		merged[name] = ratio
		updated++
	}
	return merged, updated
}
