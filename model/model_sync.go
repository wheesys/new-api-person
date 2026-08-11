package model

import (
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/setting/ratio_setting"

	"github.com/samber/lo"
	"gorm.io/gorm"
)

func normalizeModelMetadataNames(modelNames []string) []string {
	seen := make(map[string]struct{}, len(modelNames))
	normalized := make([]string, 0, len(modelNames))
	for _, modelName := range modelNames {
		modelName = strings.TrimSpace(modelName)
		if modelName == "" {
			continue
		}
		if _, exists := seen[modelName]; exists {
			continue
		}
		seen[modelName] = struct{}{}
		normalized = append(normalized, modelName)
	}
	return normalized
}

func ensureModelMetadata(tx *gorm.DB, modelNames []string) (int, error) {
	modelNames = normalizeModelMetadataNames(modelNames)
	if len(modelNames) == 0 {
		return 0, nil
	}

	useDB := DB
	if tx != nil {
		useDB = tx
	}

	var existingNames []string
	if err := useDB.Model(&Model{}).Where("model_name IN ?", modelNames).Pluck("model_name", &existingNames).Error; err != nil {
		return 0, err
	}
	existingSet := make(map[string]struct{}, len(existingNames))
	for _, modelName := range existingNames {
		existingSet[modelName] = struct{}{}
	}

	now := common.GetTimestamp()
	models := make([]Model, 0, len(modelNames))
	for _, modelName := range modelNames {
		if _, exists := existingSet[modelName]; exists {
			continue
		}
		models = append(models, Model{
			ModelName:    modelName,
			Status:       1,
			SyncOfficial: 1,
			NameRule:     NameRuleExact,
			CreatedTime:  now,
			UpdatedTime:  now,
		})
	}
	if len(models) == 0 {
		return 0, nil
	}

	for _, chunk := range lo.Chunk(models, 50) {
		if err := useDB.Create(&chunk).Error; err != nil {
			return 0, err
		}
	}

	return len(models), nil
}

func EnsureModelMetadata(modelNames []string) (int, error) {
	return ensureModelMetadata(nil, modelNames)
}

func EnsureDefaultOptionModels() (int, error) {
	defaultModelPrices := ratio_setting.GetDefaultModelPriceMap()
	modelNames := make([]string, 0, len(ratio_setting.GetDefaultModelRatioMap())+len(defaultModelPrices))
	for modelName := range ratio_setting.GetDefaultModelRatioMap() {
		modelNames = append(modelNames, modelName)
	}
	for modelName := range defaultModelPrices {
		modelNames = append(modelNames, modelName)
	}
	// 渠道模型映射的目标（上游模型）也纳入模型列表/定价列表，便于为它们配置价格。
	modelNames = append(modelNames, collectChannelMappingTargetModels()...)
	created, err := EnsureModelMetadata(modelNames)
	if err != nil {
		return 0, err
	}

	for modelName, price := range defaultModelPrices {
		if err := DB.Model(&Model{}).
			Where("model_name = ? AND base_price IS NULL", strings.TrimSpace(modelName)).
			Update("base_price", price).Error; err != nil {
			return 0, err
		}
	}
	InvalidatePricingCache()
	return created, nil
}

// extractMappingTargets 从一组渠道 model_mapping 中收集所有映射目标模型名（上游模型）。
func extractMappingTargets(mappings []*string) []string {
	targets := make(map[string]struct{})
	for _, mapping := range mappings {
		if mapping == nil || strings.TrimSpace(*mapping) == "" || *mapping == "{}" {
			continue
		}
		var modelMap map[string]string
		if err := common.Unmarshal([]byte(*mapping), &modelMap); err != nil {
			continue
		}
		for _, target := range modelMap {
			target = strings.TrimSpace(target)
			if target != "" {
				targets[target] = struct{}{}
			}
		}
	}
	out := make([]string, 0, len(targets))
	for target := range targets {
		out = append(out, target)
	}
	return out
}

// collectChannelMappingTargetModels 返回所有渠道 model_mapping 的映射目标模型名
// （即渠道把请求模型重定向到的上游模型），确保它们出现在模型列表与定价列表中，便于配价。
func collectChannelMappingTargetModels() []string {
	var mappings []*string
	if err := DB.Model(&Channel{}).Pluck("model_mapping", &mappings).Error; err != nil {
		common.SysError("collect channel mapping target models failed: " + err.Error())
		return nil
	}
	return extractMappingTargets(mappings)
}

// EnsureChannelMappingTargetModels 把指定渠道 model_mapping 的映射目标（上游模型）
// 纳入 models 表，使它们在模型列表/定价列表中可见可配价。
func EnsureChannelMappingTargetModels(channels []Channel) (int, error) {
	mappings := make([]*string, 0, len(channels))
	for i := range channels {
		mappings = append(mappings, channels[i].ModelMapping)
	}
	targets := extractMappingTargets(mappings)
	if len(targets) == 0 {
		return 0, nil
	}
	return EnsureModelMetadata(targets)
}
