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
