package controller

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type modelsMetaPageResponse struct {
	Success bool `json:"success"`
	Data    struct {
		Items []model.Model `json:"items"`
		Total int64         `json:"total"`
	} `json:"data"`
}

func decodeModelsMetaPageResponse(t *testing.T, recorder *httptest.ResponseRecorder) modelsMetaPageResponse {
	t.Helper()

	require.Equal(t, http.StatusOK, recorder.Code)
	var payload modelsMetaPageResponse
	require.NoError(t, common.Unmarshal(recorder.Body.Bytes(), &payload))
	require.True(t, payload.Success)
	return payload
}

func modelNamesFromMeta(items []model.Model) []string {
	modelNames := make([]string, 0, len(items))
	for _, item := range items {
		modelNames = append(modelNames, item.ModelName)
	}
	return modelNames
}

func TestGetAllModelsMetaOnlyReturnsModelsWithEnabledAbilities(t *testing.T) {
	db := setupModelListControllerTestDB(t)
	require.NoError(t, db.Create(&[]model.Model{
		{ModelName: "zz-enabled-model", Status: 1, NameRule: model.NameRuleExact},
		{ModelName: "zz-disabled-ability-model", Status: 1, NameRule: model.NameRuleExact},
		{ModelName: "zz-unbound-model", Status: 1, NameRule: model.NameRuleExact},
		{ModelName: "zz-rule-prefix-", Status: 1, NameRule: model.NameRulePrefix},
	}).Error)
	require.NoError(t, db.Create(&[]model.Ability{
		{Group: "default", Model: "zz-enabled-model", ChannelId: 1, Enabled: true},
		{Group: "default", Model: "zz-disabled-ability-model", ChannelId: 1, Enabled: false},
		{Group: "default", Model: "zz-rule-prefix-target", ChannelId: 1, Enabled: true},
	}).Error)

	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(http.MethodGet, "/api/models/?page=1&page_size=20", nil)

	GetAllModelsMeta(ctx)

	payload := decodeModelsMetaPageResponse(t, recorder)
	assert.Equal(t, int64(2), payload.Data.Total)
	assert.ElementsMatch(t, []string{"zz-enabled-model", "zz-rule-prefix-"}, modelNamesFromMeta(payload.Data.Items))
}

func TestSearchModelsMetaOnlyReturnsModelsWithEnabledAbilities(t *testing.T) {
	db := setupModelListControllerTestDB(t)
	require.NoError(t, db.Create(&[]model.Model{
		{ModelName: "zz-search-enabled-model", Status: 1, NameRule: model.NameRuleExact},
		{ModelName: "zz-search-unbound-model", Status: 1, NameRule: model.NameRuleExact},
		{ModelName: "zz-other-enabled-model", Status: 1, NameRule: model.NameRuleExact},
	}).Error)
	require.NoError(t, db.Create(&[]model.Ability{
		{Group: "default", Model: "zz-search-enabled-model", ChannelId: 1, Enabled: true},
		{Group: "default", Model: "zz-other-enabled-model", ChannelId: 1, Enabled: true},
	}).Error)

	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(http.MethodGet, "/api/models/search?keyword=search&page=1&page_size=20", nil)

	SearchModelsMeta(ctx)

	payload := decodeModelsMetaPageResponse(t, recorder)
	assert.Equal(t, int64(1), payload.Data.Total)
	assert.ElementsMatch(t, []string{"zz-search-enabled-model"}, modelNamesFromMeta(payload.Data.Items))
}

func TestPricingAggregatesSameModelAcrossChannels(t *testing.T) {
	db := setupModelListControllerTestDB(t)
	require.NoError(t, db.Create(&[]model.Channel{
		{Id: 1, Name: "channel-one", Type: 1, Status: common.ChannelStatusEnabled},
		{Id: 2, Name: "channel-two", Type: 1, Status: common.ChannelStatusEnabled},
	}).Error)
	require.NoError(t, db.Create(&[]model.Ability{
		{Group: "default", Model: "zz-shared-pricing-model", ChannelId: 1, Enabled: true},
		{Group: "vip", Model: "zz-shared-pricing-model", ChannelId: 2, Enabled: true},
	}).Error)
	model.RefreshPricing()

	count := 0
	var groups []string
	for _, pricing := range model.GetPricing() {
		if pricing.ModelName != "zz-shared-pricing-model" {
			continue
		}
		count++
		groups = pricing.EnableGroup
	}

	assert.Equal(t, 1, count)
	assert.ElementsMatch(t, []string{"default", "vip"}, groups)
}
