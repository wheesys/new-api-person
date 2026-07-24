package model_setting

import (
	"fmt"
	"strings"
	"sync/atomic"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/setting/config"
)

type SmartRoutingSettings struct {
	VirtualModelPools map[string][]string `json:"virtual_model_pools"`
}

var smartRoutingSettings = SmartRoutingSettings{
	VirtualModelPools: map[string][]string{},
}

var smartRoutingSettingsSnapshot atomic.Pointer[SmartRoutingSettings]

var supportedSmartRoutingVirtualModels = map[string]struct{}{
	"auto:cheap":     {},
	"auto:balanced":  {},
	"auto:quality":   {},
	"auto:fast":      {},
	"auto:reasoning": {},
}

func init() {
	smartRoutingSettings.OnConfigUpdated()
	config.GlobalConfig.Register("smart_routing", &smartRoutingSettings)
}

func GetSmartRoutingSettings() *SmartRoutingSettings {
	return cloneSmartRoutingSettings(smartRoutingSettingsSnapshot.Load())
}

func (settings *SmartRoutingSettings) OnConfigUpdated() {
	smartRoutingSettingsSnapshot.Store(cloneSmartRoutingSettings(settings))
}

func GetSmartRoutingVirtualModelPool(virtualModel string) ([]string, bool) {
	settings := smartRoutingSettingsSnapshot.Load()
	if settings == nil || len(settings.VirtualModelPools) == 0 {
		return nil, false
	}

	pool, configured := settings.VirtualModelPools[normalizeSmartRoutingVirtualModel(virtualModel)]
	if !configured || (pool != nil && len(pool) == 0) {
		return nil, false
	}

	normalizedPool := make([]string, 0, len(pool))
	for _, modelName := range pool {
		modelName = strings.TrimSpace(modelName)
		if modelName != "" {
			normalizedPool = append(normalizedPool, modelName)
		}
	}
	if len(normalizedPool) == 0 {
		return nil, true
	}
	return append([]string(nil), normalizedPool...), true

}

func ValidateSmartRoutingVirtualModelPools(value string) error {
	var pools map[string][]string
	if err := common.UnmarshalJsonStr(value, &pools); err != nil {
		return fmt.Errorf("invalid smart routing virtual model pools: %w", err)
	}
	if pools == nil {
		return fmt.Errorf("smart routing virtual model pools must be a JSON object")
	}
	for virtualModel := range pools {
		if _, ok := supportedSmartRoutingVirtualModels[virtualModel]; !ok {
			return fmt.Errorf("unsupported smart routing virtual model key %q", virtualModel)
		}
		pool := pools[virtualModel]
		if pool == nil {
			return fmt.Errorf("smart routing virtual model pool %q must be an array", virtualModel)
		}
		for _, modelName := range pool {
			if strings.TrimSpace(modelName) == "" {
				return fmt.Errorf("smart routing virtual model pool %q contains an empty model name", virtualModel)
			}
		}
	}
	return nil
}

func cloneSmartRoutingSettings(settings *SmartRoutingSettings) *SmartRoutingSettings {
	cloned := &SmartRoutingSettings{VirtualModelPools: map[string][]string{}}
	if settings == nil {
		return cloned
	}
	for virtualModel, pool := range settings.VirtualModelPools {
		cloned.VirtualModelPools[virtualModel] = append([]string(nil), pool...)
	}
	return cloned
}

func normalizeSmartRoutingVirtualModel(virtualModel string) string {
	normalized := strings.ToLower(strings.TrimSpace(virtualModel))
	if normalized == "auto" || normalized == "smart" {
		return "auto:balanced"
	}
	if strings.HasPrefix(normalized, "smart:") {
		return "auto:" + strings.TrimPrefix(normalized, "smart:")
	}
	return normalized
}
