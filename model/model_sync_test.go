package model

import (
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBatchInsertChannelsCreatesMissingModelMetadata(t *testing.T) {
	setupModelPriceTestDB(t)

	require.NoError(t, BatchInsertChannels([]Channel{
		{
			Type:   1,
			Key:    "test-key",
			Status: common.ChannelStatusEnabled,
			Name:   "test-channel",
			Group:  "default",
			Models: "zz-existing-model, zz-new-channel-model,zz-new-channel-model",
		},
	}))

	var models []Model
	require.NoError(t, DB.Order("model_name ASC").Find(&models).Error)

	modelNames := make([]string, 0, len(models))
	for _, item := range models {
		modelNames = append(modelNames, item.ModelName)
		assert.Equal(t, 1, item.Status)
		assert.Equal(t, NameRuleExact, item.NameRule)
	}
	assert.ElementsMatch(t, []string{"zz-existing-model", "zz-new-channel-model"}, modelNames)
}

func TestBatchInsertChannelsRefreshesPricingModels(t *testing.T) {
	setupModelPriceTestDB(t)

	require.NoError(t, DB.Create(&Channel{
		Id:     1,
		Type:   1,
		Key:    "test-key",
		Status: common.ChannelStatusEnabled,
		Name:   "old-channel",
		Group:  "default",
		Models: "zz-old-model",
	}).Error)
	require.NoError(t, DB.Create(&Ability{
		Group:     "default",
		Model:     "zz-old-model",
		ChannelId: 1,
		Enabled:   true,
	}).Error)

	pricingByName := map[string]Pricing{}
	for _, pricing := range GetPricing() {
		pricingByName[pricing.ModelName] = pricing
	}
	require.Contains(t, pricingByName, "zz-old-model")
	require.NotContains(t, pricingByName, "zz-new-pricing-model")

	require.NoError(t, BatchInsertChannels([]Channel{
		{
			Type:   1,
			Key:    "test-key-2",
			Status: common.ChannelStatusEnabled,
			Name:   "new-channel",
			Group:  "default",
			Models: "zz-new-pricing-model",
		},
	}))

	pricingByName = map[string]Pricing{}
	for _, pricing := range GetPricing() {
		pricingByName[pricing.ModelName] = pricing
	}
	assert.Contains(t, pricingByName, "zz-new-pricing-model")
}

func TestEnsureDefaultOptionModelsCreatesModelMetadata(t *testing.T) {
	setupModelPriceTestDB(t)

	created, err := EnsureDefaultOptionModels()
	require.NoError(t, err)
	require.Greater(t, created, 0)

	var count int64
	require.NoError(t, DB.Model(&Model{}).Where("model_name = ?", "suno_music").Count(&count).Error)
	assert.Equal(t, int64(1), count)

	var sunoMusic Model
	require.NoError(t, DB.Where("model_name = ?", "suno_music").First(&sunoMusic).Error)
	require.NotNil(t, sunoMusic.BasePrice)
	assert.Equal(t, 0.1, *sunoMusic.BasePrice)

	count = 0
	require.NoError(t, DB.Model(&Model{}).Where("model_name = ?", "gpt-4-gizmo-*").Count(&count).Error)
	assert.Equal(t, int64(1), count)
}

func TestEnsureDefaultOptionModelsBackfillsMissingBasePrice(t *testing.T) {
	setupModelPriceTestDB(t)

	existingModel := &Model{
		ModelName:    "dall-e-3",
		Description:  "keep me",
		Status:       0,
		SyncOfficial: 0,
		NameRule:     NameRuleContains,
	}
	require.NoError(t, existingModel.Insert())

	created, err := EnsureDefaultOptionModels()
	require.NoError(t, err)
	require.Greater(t, created, 0)

	var existing Model
	require.NoError(t, DB.Where("model_name = ?", "dall-e-3").First(&existing).Error)
	assert.Equal(t, "keep me", existing.Description)
	assert.Equal(t, 0, existing.Status)
	assert.Equal(t, 0, existing.SyncOfficial)
	assert.Equal(t, NameRuleContains, existing.NameRule)
	require.NotNil(t, existing.BasePrice)
	assert.Equal(t, 0.04, *existing.BasePrice)
}

func TestEnsureDefaultOptionModelsDoesNotOverwriteExistingBasePrice(t *testing.T) {
	setupModelPriceTestDB(t)

	existingModel := &Model{
		ModelName: "suno_music",
		Status:    1,
		BasePrice: ptrOfFloat64(0.25),
		NameRule:  NameRuleExact,
	}
	require.NoError(t, existingModel.Insert())

	_, err := EnsureDefaultOptionModels()
	require.NoError(t, err)

	var existing Model
	require.NoError(t, DB.Where("model_name = ?", "suno_music").First(&existing).Error)
	require.NotNil(t, existing.BasePrice)
	assert.Equal(t, 0.25, *existing.BasePrice)
}

func TestEnsureModelMetadataDoesNotOverwriteExistingModel(t *testing.T) {
	setupModelPriceTestDB(t)

	existingModel := &Model{
		ModelName:    "zz-existing-model",
		Description:  "keep me",
		Status:       0,
		SyncOfficial: 0,
		NameRule:     NameRuleContains,
	}
	require.NoError(t, existingModel.Insert())

	created, err := EnsureModelMetadata([]string{"zz-existing-model", "zz-new-model"})
	require.NoError(t, err)
	assert.Equal(t, 1, created)

	var existing Model
	require.NoError(t, DB.Where("model_name = ?", "zz-existing-model").First(&existing).Error)
	assert.Equal(t, "keep me", existing.Description)
	assert.Equal(t, 0, existing.Status)
	assert.Equal(t, 0, existing.SyncOfficial)
	assert.Equal(t, NameRuleContains, existing.NameRule)
}
