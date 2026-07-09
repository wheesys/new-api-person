package middleware

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/i18n"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service/smartrouting"
	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func setupDistributorSmartRoutingTestDB(t *testing.T) *gorm.DB {
	t.Helper()

	originalDB := model.DB
	originalLogDB := model.LOG_DB
	originalMemoryCacheEnabled := common.MemoryCacheEnabled
	originalMainDatabaseType := common.MainDatabaseType()
	originalLogDatabaseType := common.LogDatabaseType()

	common.SetDatabaseTypes(common.DatabaseTypeSQLite, common.DatabaseTypeSQLite)
	common.MemoryCacheEnabled = true

	dsn := fmt.Sprintf("file:%s?mode=memory&cache=shared", strings.ReplaceAll(t.Name(), "/", "_"))
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	require.NoError(t, err)
	model.DB = db
	model.LOG_DB = db
	require.NoError(t, db.AutoMigrate(&model.Channel{}, &model.Ability{}, &model.Model{}))
	model.InvalidatePricingCache()

	t.Cleanup(func() {
		model.InvalidatePricingCache()
		model.DB = originalDB
		model.LOG_DB = originalLogDB
		common.MemoryCacheEnabled = originalMemoryCacheEnabled
		common.SetDatabaseTypes(originalMainDatabaseType, originalLogDatabaseType)
		sqlDB, err := db.DB()
		if err == nil {
			_ = sqlDB.Close()
		}
	})

	return db
}

func TestDistributeResolvesVirtualModelBeforeChannelSelection(t *testing.T) {
	gin.SetMode(gin.TestMode)
	require.NoError(t, i18n.Init())
	db := setupDistributorSmartRoutingTestDB(t)

	require.NoError(t, db.Create(&model.Channel{
		Id:           1001,
		Type:         1,
		Key:          "test-key",
		Status:       common.ChannelStatusEnabled,
		Name:         "quality-channel",
		Models:       "gpt-5",
		Group:        "default",
		ResponseTime: 120,
	}).Error)
	require.NoError(t, db.Create(&model.Ability{
		Group:     "default",
		Model:     "gpt-5",
		ChannelId: 1001,
		Enabled:   true,
	}).Error)
	require.NoError(t, db.Create(&model.Model{
		ModelName: "gpt-5",
		Status:    1,
		NameRule:  model.NameRuleExact,
	}).Error)
	model.InitChannelCache()

	router := gin.New()
	router.Use(func(c *gin.Context) {
		common.SetContextKey(c, constant.ContextKeyUsingGroup, "default")
		common.SetContextKey(c, constant.ContextKeyUserGroup, "default")
		c.Next()
	})
	router.POST("/v1/chat/completions", Distribute(), func(c *gin.Context) {
		assert.Equal(t, "gpt-5", common.GetContextKeyString(c, constant.ContextKeyOriginalModel))
		assert.Equal(t, 1001, common.GetContextKeyInt(c, constant.ContextKeyChannelId))

		storage, err := common.GetBodyStorage(c)
		require.NoError(t, err)
		bodyBytes, err := storage.Bytes()
		require.NoError(t, err)
		assert.Contains(t, string(bodyBytes), `"model":"gpt-5"`)
		assert.NotContains(t, string(bodyBytes), "auto:quality")

		decision, ok := common.GetContextKeyType[smartrouting.Decision](c, constant.ContextKeySmartRoutingDecision)
		require.True(t, ok)
		assert.True(t, decision.Enabled)
		assert.Equal(t, "auto:quality", decision.OriginalModel)
		assert.Equal(t, "gpt-5", decision.SelectedModel)
		assert.Equal(t, 1001, decision.SelectedChannelID)

		c.Status(http.StatusOK)
	})

	body := strings.NewReader(`{"model":"auto:quality","messages":[{"role":"user","content":"设计一个迁移方案"}],"max_tokens":1024}`)
	request := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", body)
	request.Header.Set("Content-Type", "application/json")
	recorder := httptest.NewRecorder()

	router.ServeHTTP(recorder, request)

	require.Equal(t, http.StatusOK, recorder.Code)
}

func TestDistributeKeepsExplicitModelAndScoresChannels(t *testing.T) {
	gin.SetMode(gin.TestMode)
	require.NoError(t, i18n.Init())
	db := setupDistributorSmartRoutingTestDB(t)

	expensiveRatio := 1.5
	cheapRatio := 0.2
	highPriority := int64(100)
	lowPriority := int64(0)
	lowWeight := uint(1)
	highWeight := uint(100)

	require.NoError(t, db.Create(&[]model.Channel{
		{
			Id:           2001,
			Type:         1,
			Key:          "expensive-key",
			Status:       common.ChannelStatusEnabled,
			Name:         "expensive-priority-channel",
			Models:       "gpt-fixed",
			Group:        "default",
			ResponseTime: 900,
			PriceRatio:   &expensiveRatio,
			Priority:     &highPriority,
			Weight:       &lowWeight,
		},
		{
			Id:           2002,
			Type:         1,
			Key:          "cheap-key",
			Status:       common.ChannelStatusEnabled,
			Name:         "cheap-scored-channel",
			Models:       "gpt-fixed",
			Group:        "default",
			ResponseTime: 50,
			PriceRatio:   &cheapRatio,
			Priority:     &lowPriority,
			Weight:       &highWeight,
		},
	}).Error)
	require.NoError(t, db.Create(&[]model.Ability{
		{Group: "default", Model: "gpt-fixed", ChannelId: 2001, Enabled: true},
		{Group: "default", Model: "gpt-fixed", ChannelId: 2002, Enabled: true},
	}).Error)
	require.NoError(t, db.Create(&model.Model{
		ModelName: "gpt-fixed",
		Status:    1,
		NameRule:  model.NameRuleExact,
	}).Error)
	model.InitChannelCache()

	router := gin.New()
	router.Use(func(c *gin.Context) {
		common.SetContextKey(c, constant.ContextKeyUsingGroup, "default")
		common.SetContextKey(c, constant.ContextKeyUserGroup, "default")
		c.Next()
	})
	router.POST("/v1/chat/completions", Distribute(), func(c *gin.Context) {
		assert.Equal(t, "gpt-fixed", common.GetContextKeyString(c, constant.ContextKeyOriginalModel))
		assert.Equal(t, 2002, common.GetContextKeyInt(c, constant.ContextKeyChannelId))

		decision, ok := common.GetContextKeyType[smartrouting.Decision](c, constant.ContextKeySmartRoutingDecision)
		require.True(t, ok)
		assert.Equal(t, "gpt-fixed", decision.OriginalModel)
		assert.Equal(t, "gpt-fixed", decision.SelectedModel)
		assert.Equal(t, 2002, decision.SelectedChannelID)

		storage, err := common.GetBodyStorage(c)
		require.NoError(t, err)
		bodyBytes, err := storage.Bytes()
		require.NoError(t, err)
		assert.Contains(t, string(bodyBytes), `"model":"gpt-fixed"`)

		c.Status(http.StatusOK)
	})

	body := strings.NewReader(`{"model":"gpt-fixed","messages":[{"role":"user","content":"hello"}],"max_tokens":1024}`)
	request := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", body)
	request.Header.Set("Content-Type", "application/json")
	recorder := httptest.NewRecorder()

	router.ServeHTTP(recorder, request)

	require.Equal(t, http.StatusOK, recorder.Code)
}

func TestDistributeStoresSameModelSmartRoutingRetryCandidates(t *testing.T) {
	gin.SetMode(gin.TestMode)
	require.NoError(t, i18n.Init())
	db := setupDistributorSmartRoutingTestDB(t)

	selectedRatio := 0.1
	retryRatio := 0.2
	otherModelRatio := 10.0
	require.NoError(t, db.Create(&[]model.Channel{
		{
			Id:           3001,
			Type:         1,
			Key:          "selected-key",
			Status:       common.ChannelStatusEnabled,
			Name:         "selected-gpt-5-channel",
			Models:       "gpt-5",
			Group:        "default",
			ResponseTime: 50,
			PriceRatio:   &selectedRatio,
		},
		{
			Id:           3002,
			Type:         1,
			Key:          "retry-key",
			Status:       common.ChannelStatusEnabled,
			Name:         "retry-gpt-5-channel",
			Models:       "gpt-5",
			Group:        "default",
			ResponseTime: 120,
			PriceRatio:   &retryRatio,
		},
		{
			Id:           3003,
			Type:         1,
			Key:          "other-model-key",
			Status:       common.ChannelStatusEnabled,
			Name:         "other-model-channel",
			Models:       "gpt-5-mini",
			Group:        "default",
			ResponseTime: 900,
			PriceRatio:   &otherModelRatio,
		},
	}).Error)
	require.NoError(t, db.Create(&[]model.Ability{
		{Group: "default", Model: "gpt-5", ChannelId: 3001, Enabled: true},
		{Group: "default", Model: "gpt-5", ChannelId: 3002, Enabled: true},
		{Group: "default", Model: "gpt-5-mini", ChannelId: 3003, Enabled: true},
	}).Error)
	require.NoError(t, db.Create(&[]model.Model{
		{ModelName: "gpt-5", Status: 1, NameRule: model.NameRuleExact},
		{ModelName: "gpt-5-mini", Status: 1, NameRule: model.NameRuleExact},
	}).Error)
	model.InitChannelCache()

	router := gin.New()
	router.Use(func(c *gin.Context) {
		common.SetContextKey(c, constant.ContextKeyUsingGroup, "default")
		common.SetContextKey(c, constant.ContextKeyUserGroup, "default")
		c.Next()
	})
	router.POST("/v1/chat/completions", Distribute(), func(c *gin.Context) {
		assert.Equal(t, "gpt-5", common.GetContextKeyString(c, constant.ContextKeyOriginalModel))
		assert.Equal(t, 3001, common.GetContextKeyInt(c, constant.ContextKeyChannelId))

		rawCandidates, ok := common.GetContextKey(c, constant.ContextKeySmartRoutingRetryCandidates)
		require.True(t, ok)
		retryCandidates, ok := rawCandidates.([]smartrouting.SmartRouteCandidate)
		require.True(t, ok)
		require.Len(t, retryCandidates, 2)
		assert.Equal(t, 3001, retryCandidates[0].ChannelID)
		assert.Equal(t, 3002, retryCandidates[1].ChannelID)
		for _, candidate := range retryCandidates {
			assert.Equal(t, "gpt-5", candidate.ModelName)
		}

		c.Status(http.StatusOK)
	})

	body := strings.NewReader(`{"model":"auto:quality","messages":[{"role":"user","content":"设计一个迁移方案"}],"max_tokens":1024}`)
	request := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", body)
	request.Header.Set("Content-Type", "application/json")
	recorder := httptest.NewRecorder()

	router.ServeHTTP(recorder, request)

	require.Equal(t, http.StatusOK, recorder.Code)
}

func TestDistributeDoesNotStoreRetryCandidatesWhenExplicitModelSelectionFallsBack(t *testing.T) {
	gin.SetMode(gin.TestMode)
	require.NoError(t, i18n.Init())
	db := setupDistributorSmartRoutingTestDB(t)

	require.NoError(t, db.Create(&model.Channel{
		Id:           3100,
		Type:         1,
		Key:          "fallback-key",
		Status:       common.ChannelStatusEnabled,
		Name:         "fallback-channel",
		Models:       "gpt-fixed",
		Group:        "default",
		ResponseTime: 900,
	}).Error)
	require.NoError(t, db.Create(&model.Ability{
		Group:     "default",
		Model:     "gpt-fixed",
		ChannelId: 3100,
		Enabled:   true,
	}).Error)
	model.InitChannelCache()
	require.NoError(t, db.Create(&model.Channel{
		Id:           3101,
		Type:         1,
		Key:          "stale-cache-key",
		Status:       common.ChannelStatusEnabled,
		Name:         "stale-cache-channel",
		Models:       "gpt-fixed",
		Group:        "default",
		ResponseTime: 50,
	}).Error)
	require.NoError(t, db.Create(&model.Ability{
		Group:     "default",
		Model:     "gpt-fixed",
		ChannelId: 3101,
		Enabled:   true,
	}).Error)
	require.NoError(t, db.Create(&model.Model{
		ModelName: "gpt-fixed",
		Status:    1,
		NameRule:  model.NameRuleExact,
	}).Error)

	router := gin.New()
	router.Use(func(c *gin.Context) {
		common.SetContextKey(c, constant.ContextKeyUsingGroup, "default")
		common.SetContextKey(c, constant.ContextKeyUserGroup, "default")
		c.Next()
	})
	router.POST("/v1/chat/completions", Distribute(), func(c *gin.Context) {
		_, ok := common.GetContextKey(c, constant.ContextKeySmartRoutingRetryCandidates)
		assert.False(t, ok)
		c.Status(http.StatusOK)
	})

	body := strings.NewReader(`{"model":"gpt-fixed","messages":[{"role":"user","content":"hello"}],"max_tokens":1024}`)
	request := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", body)
	request.Header.Set("Content-Type", "application/json")
	recorder := httptest.NewRecorder()

	router.ServeHTTP(recorder, request)

	require.Equal(t, http.StatusOK, recorder.Code)
}
