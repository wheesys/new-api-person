package controller

import (
	"net/http/httptest"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestSmartRoutingMetricsWindowDefaultsAndValidatesRange(t *testing.T) {
	gin.SetMode(gin.TestMode)
	now := time.Unix(1000000, 0)

	defaultContext, _ := gin.CreateTestContext(httptest.NewRecorder())
	defaultContext.Request = httptest.NewRequest("GET", "/api/smart-routing/metrics", nil)
	startTimestamp, endTimestamp, err := smartRoutingMetricsWindow(defaultContext, now)
	require.NoError(t, err)
	assert.Equal(t, now.Unix(), endTimestamp)
	assert.Equal(t, now.Add(-24*time.Hour).Unix(), startTimestamp)

	validContext, _ := gin.CreateTestContext(httptest.NewRecorder())
	validContext.Request = httptest.NewRequest("GET", "/api/smart-routing/metrics?start_timestamp=900000&end_timestamp=950000", nil)
	startTimestamp, endTimestamp, err = smartRoutingMetricsWindow(validContext, now)
	require.NoError(t, err)
	assert.Equal(t, int64(900000), startTimestamp)
	assert.Equal(t, int64(950000), endTimestamp)

	invalidContext, _ := gin.CreateTestContext(httptest.NewRecorder())
	invalidContext.Request = httptest.NewRequest("GET", "/api/smart-routing/metrics?start_timestamp=1&end_timestamp=900000", nil)
	_, _, err = smartRoutingMetricsWindow(invalidContext, now)
	assert.ErrorContains(t, err, "cannot exceed 7 days")
}

func TestGetSmartRoutingMetricsReturnsSuccessfulDecisionAggregate(t *testing.T) {
	gin.SetMode(gin.TestMode)
	database, err := gorm.Open(sqlite.Open("file:smart-routing-metrics-controller?mode=memory&cache=shared"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, database.AutoMigrate(&model.Log{}))
	originalLogDatabase := model.LOG_DB
	model.LOG_DB = database
	t.Cleanup(func() { model.LOG_DB = originalLogDatabase })

	other := `{"smart_routing":{"schema_version":1,"enabled":true,"policy":"balanced","complexity":"standard","task_type":"general","original_model":"auto:balanced","selected_model":"selected-a","selected_channel_id":11,"selected_health":"healthy","candidate_count":2,"fallback_index":0,"score_factors":{"cost":0.2,"reliability":0.9,"latency":0.6,"throughput":0.5,"quality":0.7,"task_match":0.8,"context":0.6,"cache":0.5,"reset_window":1,"affinity":0.4}}}`
	require.NoError(t, database.Create(&model.Log{
		CreatedAt: 1000,
		Type:      model.LogTypeConsume,
		ChannelId: 11,
		ModelName: "selected-a",
		RequestId: "metrics-request",
		Other:     other,
	}).Error)

	recorder := httptest.NewRecorder()
	context, _ := gin.CreateTestContext(recorder)
	context.Request = httptest.NewRequest("GET", "/api/smart-routing/metrics?start_timestamp=900&end_timestamp=1100", nil)
	GetSmartRoutingMetrics(context)

	assert.Equal(t, 200, recorder.Code)
	var response struct {
		Success bool `json:"success"`
		Data    struct {
			SchemaVersion int `json:"schema_version"`
			Summary       struct {
				SuccessfulDecisions int64 `json:"successful_decisions"`
			} `json:"summary"`
		} `json:"data"`
	}
	require.NoError(t, common.Unmarshal(recorder.Body.Bytes(), &response))
	assert.True(t, response.Success)
	assert.Equal(t, 1, response.Data.SchemaVersion)
	assert.Equal(t, int64(1), response.Data.Summary.SuccessfulDecisions)
}
