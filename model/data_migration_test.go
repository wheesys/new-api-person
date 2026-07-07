package model

import (
	"strconv"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRunDataMigrationsBackfillsLegacyChannelData(t *testing.T) {
	db := setupModelPriceTestDB(t)
	require.NoError(t, db.AutoMigrate(&Option{}))

	require.NoError(t, db.Create(&Channel{
		Id:     1,
		Type:   1,
		Key:    "test-key",
		Status: common.ChannelStatusEnabled,
		Name:   "legacy-channel",
		Group:  "default, vip",
		Models: "gpt-legacy, claude-legacy,gpt-legacy",
	}).Error)
	require.NoError(t, db.Exec("UPDATE channels SET price_ratio = NULL WHERE id = ?", 1).Error)

	require.NoError(t, RunDataMigrations())

	var channel Channel
	require.NoError(t, db.First(&channel, "id = ?", 1).Error)
	require.NotNil(t, channel.PriceRatio)
	assert.Equal(t, float64(1), *channel.PriceRatio)

	var abilities []Ability
	require.NoError(t, db.Order("model ASC, "+commonGroupCol+" ASC").Find(&abilities).Error)
	actualAbilityKeys := make([]string, 0, len(abilities))
	for _, ability := range abilities {
		actualAbilityKeys = append(actualAbilityKeys, ability.Group+"|"+ability.Model)
	}
	assert.ElementsMatch(t, []string{
		"default|claude-legacy",
		"vip|claude-legacy",
		"default|gpt-legacy",
		"vip|gpt-legacy",
	}, actualAbilityKeys)

	var modelNames []string
	require.NoError(t, db.Model(&Model{}).Order("model_name ASC").Pluck("model_name", &modelNames).Error)
	assert.Contains(t, modelNames, "claude-legacy")
	assert.Contains(t, modelNames, "gpt-legacy")

	var option Option
	require.NoError(t, db.First(&option, "key = ?", dataMigrationVersionOptionKey).Error)
	assert.Equal(t, strconv.Itoa(currentDataMigrationVersion), option.Value)
}

func TestRunDataMigrationsSkipsLegacyScanWhenVersionIsCurrent(t *testing.T) {
	db := setupModelPriceTestDB(t)
	require.NoError(t, db.AutoMigrate(&Option{}))
	require.NoError(t, db.Create(&Option{
		Key:   dataMigrationVersionOptionKey,
		Value: strconv.Itoa(currentDataMigrationVersion),
	}).Error)
	require.NoError(t, db.Create(&Channel{
		Id:     1,
		Type:   1,
		Key:    "test-key",
		Status: common.ChannelStatusEnabled,
		Name:   "already-migrated-channel",
		Group:  "default",
		Models: "gpt-should-not-scan",
	}).Error)

	require.NoError(t, RunDataMigrations())

	var count int64
	require.NoError(t, db.Model(&Model{}).Where("model_name = ?", "gpt-should-not-scan").Count(&count).Error)
	assert.Equal(t, int64(0), count)
}
