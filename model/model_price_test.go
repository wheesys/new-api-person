package model

import (
	"fmt"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/setting/ratio_setting"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func setupModelPriceTestDB(t *testing.T) *gorm.DB {
	t.Helper()

	originalDB := DB
	originalLogDB := LOG_DB

	common.SetDatabaseTypes(common.DatabaseTypeSQLite, common.DatabaseTypeSQLite)
	common.RedisEnabled = false

	dsn := fmt.Sprintf("file:%s?mode=memory&cache=shared", strings.ReplaceAll(t.Name(), "/", "_"))
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	require.NoError(t, err)
	DB = db
	LOG_DB = db
	require.NoError(t, db.AutoMigrate(&Channel{}, &Ability{}, &Model{}))

	t.Cleanup(func() {
		InvalidatePricingCache()
		DB = originalDB
		LOG_DB = originalLogDB
		sqlDB, err := db.DB()
		if err == nil {
			_ = sqlDB.Close()
		}
	})

	return db
}

func TestModelBasePriceOverridesGlobalModelPrice(t *testing.T) {
	db := setupModelPriceTestDB(t)
	const modelName = "zz-fixed-price-model"
	originalModelPrices := ratio_setting.ModelPrice2JSONString()
	require.NoError(t, ratio_setting.UpdateModelPriceByJSONString(`{"zz-fixed-price-model":0.25}`))
	t.Cleanup(func() {
		require.NoError(t, ratio_setting.UpdateModelPriceByJSONString(originalModelPrices))
	})

	require.NoError(t, db.Create(&Channel{
		Id:     1,
		Type:   1,
		Key:    "test-key",
		Status: common.ChannelStatusEnabled,
		Name:   "test-channel",
	}).Error)
	require.NoError(t, db.Create(&Ability{
		Group:     "default",
		Model:     modelName,
		ChannelId: 1,
		Enabled:   true,
	}).Error)
	require.NoError(t, db.Create(&Model{
		ModelName: modelName,
		Status:    1,
		BasePrice: ptrOfFloat64(0.12),
		NameRule:  NameRuleExact,
	}).Error)

	RefreshPricing()

	modelPrice, ok := GetModelFixedPrice(modelName, false)
	require.True(t, ok)
	assert.Equal(t, 0.12, modelPrice)

	pricingByName := make(map[string]Pricing)
	for _, pricing := range GetPricing() {
		pricingByName[pricing.ModelName] = pricing
	}
	pricing, ok := pricingByName[modelName]
	require.True(t, ok)
	assert.Equal(t, 1, pricing.QuotaType)
	assert.Equal(t, 0.12, pricing.ModelPrice)
}

func TestZeroModelBasePriceIsConfiguredFixedPrice(t *testing.T) {
	setupModelPriceTestDB(t)
	const modelName = "zz-free-fixed-price-model"
	require.NoError(t, DB.Create(&Model{
		ModelName: modelName,
		Status:    1,
		BasePrice: ptrOfFloat64(0),
		NameRule:  NameRuleExact,
	}).Error)

	modelPrice, ok := GetModelFixedPrice(modelName, false)
	require.True(t, ok)
	assert.Equal(t, float64(0), modelPrice)
}

func ptrOfFloat64(value float64) *float64 {
	return &value
}
