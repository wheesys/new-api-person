package controller

import (
	"fmt"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/service/smartrouting"
	"github.com/QuantumNous/new-api/types"
	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestShouldRecordRuntimeHealthFailureUsesRetryableChannelErrors(t *testing.T) {
	tests := []struct {
		name       string
		statusCode int
		expected   bool
	}{
		{name: "client request error", statusCode: 400, expected: false},
		{name: "rate limited", statusCode: 429, expected: true},
		{name: "upstream unavailable", statusCode: 503, expected: true},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := types.NewOpenAIError(fmt.Errorf("status %d", test.statusCode), types.ErrorCodeBadResponseStatusCode, test.statusCode)
			assert.Equal(t, test.expected, shouldRecordRuntimeHealthFailure(err))
		})
	}
}

func TestRecordRuntimeHealthSuccessForAttemptUsesValidScoringLatency(t *testing.T) {
	smartrouting.ClearRuntimeHealth()
	t.Cleanup(smartrouting.ClearRuntimeHealth)

	recordRuntimeHealthSuccessForAttempt(3901, "stream-without-ttft", true, relaycommon.UpstreamAttemptSample{
		Latency:    2 * time.Second,
		HasLatency: true,
	})
	streamWithoutTTFT := smartrouting.GetRuntimeHealthSnapshot(3901, "stream-without-ttft")
	assert.Equal(t, 0, streamWithoutTTFT.LatencySampleCount)

	recordRuntimeHealthSuccessForAttempt(3902, "stream-with-ttft", true, relaycommon.UpstreamAttemptSample{
		Latency:    2 * time.Second,
		HasLatency: true,
		TTFT:       200 * time.Millisecond,
		HasTTFT:    true,
	})
	streamWithTTFT := smartrouting.GetRuntimeHealthSnapshot(3902, "stream-with-ttft")
	assert.Equal(t, 1, streamWithTTFT.LatencySampleCount)

	recordRuntimeHealthSuccessForAttempt(3903, "non-stream", false, relaycommon.UpstreamAttemptSample{
		Latency:    750 * time.Millisecond,
		HasLatency: true,
	})
	nonStream := smartrouting.GetRuntimeHealthSnapshot(3903, "non-stream")
	assert.Equal(t, 1, nonStream.LatencySampleCount)
}

func setupSmartRoutingRetryTestDB(t *testing.T) *gorm.DB {
	t.Helper()

	originalDB := model.DB
	originalLogDB := model.LOG_DB
	originalMemoryCacheEnabled := common.MemoryCacheEnabled
	originalMainDatabaseType := common.MainDatabaseType()
	originalLogDatabaseType := common.LogDatabaseType()

	common.SetDatabaseTypes(common.DatabaseTypeSQLite, common.DatabaseTypeSQLite)
	common.MemoryCacheEnabled = false

	dsn := fmt.Sprintf("file:%s?mode=memory&cache=shared", strings.ReplaceAll(t.Name(), "/", "_"))
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	require.NoError(t, err)
	model.DB = db
	model.LOG_DB = db
	require.NoError(t, db.AutoMigrate(&model.Channel{}))

	t.Cleanup(func() {
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

func TestGetChannelUsesSameModelSmartRoutingRetryCandidate(t *testing.T) {
	gin.SetMode(gin.TestMode)
	db := setupSmartRoutingRetryTestDB(t)

	require.NoError(t, db.Create(&[]model.Channel{
		{
			Id:     4001,
			Type:   constant.ChannelTypeOpenAI,
			Key:    "first-key",
			Status: common.ChannelStatusEnabled,
			Name:   "first-smart-channel",
			Models: "gpt-5",
			Group:  "default",
		},
		{
			Id:     4002,
			Type:   constant.ChannelTypeOpenAI,
			Key:    "retry-key",
			Status: common.ChannelStatusEnabled,
			Name:   "retry-smart-channel",
			Models: "gpt-5",
			Group:  "default",
		},
	}).Error)

	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest("POST", "/v1/chat/completions", nil)
	common.SetContextKey(ctx, constant.ContextKeySmartRoutingRetryCandidates, []smartrouting.SmartRouteCandidate{
		{ModelName: "gpt-5", ChannelID: 4001, Group: "default"},
		{ModelName: "gpt-5", ChannelID: 4002, Group: "default", ScoreFactors: smartrouting.ScoreFactors{Latency: 0.8}},
	})
	common.SetContextKey(ctx, constant.ContextKeySmartRoutingDecision, smartrouting.Decision{
		Enabled:           true,
		OriginalModel:     "auto:quality",
		SelectedModel:     "gpt-5",
		SelectedChannelID: 4001,
		FallbackIndex:     0,
	})

	relayInfo := &relaycommon.RelayInfo{
		OriginModelName: "gpt-5",
		ChannelMeta:     &relaycommon.ChannelMeta{},
	}
	channel, channelErr := getChannel(ctx, relayInfo, &service.RetryParam{
		Ctx:         ctx,
		TokenGroup:  "default",
		ModelName:   "gpt-5",
		RequestPath: "/v1/chat/completions",
		Retry:       common.GetPointer(1),
	})

	require.Nil(t, channelErr)
	require.NotNil(t, channel)
	assert.Equal(t, 4002, channel.Id)
	assert.Equal(t, 4002, common.GetContextKeyInt(ctx, constant.ContextKeyChannelId))

	decision, ok := common.GetContextKeyType[smartrouting.Decision](ctx, constant.ContextKeySmartRoutingDecision)
	require.True(t, ok)
	assert.Equal(t, 1, decision.FallbackIndex)
	assert.Equal(t, 4002, decision.SelectedChannelID)
	assert.Equal(t, 0.8, decision.ScoreFactors.Latency)
}

func TestGetChannelAllowsNormalizedSameModelSmartRoutingRetryCandidate(t *testing.T) {
	gin.SetMode(gin.TestMode)
	db := setupSmartRoutingRetryTestDB(t)

	require.NoError(t, db.Create(&model.Channel{
		Id:     4051,
		Type:   constant.ChannelTypeOpenAI,
		Key:    "normalized-key",
		Status: common.ChannelStatusEnabled,
		Name:   "normalized-smart-channel",
		Models: "gpt-4o-gizmo-*",
		Group:  "default",
	}).Error)

	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest("POST", "/v1/chat/completions", nil)
	common.SetContextKey(ctx, constant.ContextKeySmartRoutingRetryCandidates, []smartrouting.SmartRouteCandidate{
		{ModelName: "gpt-4o-gizmo-*", ChannelID: 4051, Group: "default"},
	})

	relayInfo := &relaycommon.RelayInfo{
		OriginModelName: "gpt-4o-gizmo-custom",
		ChannelMeta:     &relaycommon.ChannelMeta{},
	}
	channel, channelErr := getChannel(ctx, relayInfo, &service.RetryParam{
		Ctx:         ctx,
		TokenGroup:  "default",
		ModelName:   "gpt-4o-gizmo-custom",
		RequestPath: "/v1/chat/completions",
		Retry:       common.GetPointer(0),
	})

	require.Nil(t, channelErr)
	require.NotNil(t, channel)
	assert.Equal(t, 4051, channel.Id)
	assert.Equal(t, "gpt-4o-gizmo-custom", common.GetContextKeyString(ctx, constant.ContextKeyOriginalModel))
}

func TestGetChannelDoesNotFallbackAfterSmartRoutingRetryCandidatesExhausted(t *testing.T) {
	gin.SetMode(gin.TestMode)
	_ = setupSmartRoutingRetryTestDB(t)

	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest("POST", "/v1/chat/completions", nil)
	common.SetContextKey(ctx, constant.ContextKeySmartRoutingRetryCandidates, []smartrouting.SmartRouteCandidate{
		{ModelName: "gpt-5", ChannelID: 4101, Group: "default"},
	})

	relayInfo := &relaycommon.RelayInfo{
		OriginModelName: "gpt-5",
		ChannelMeta:     &relaycommon.ChannelMeta{},
	}
	channel, channelErr := getChannel(ctx, relayInfo, &service.RetryParam{
		Ctx:         ctx,
		TokenGroup:  "default",
		ModelName:   "gpt-5",
		RequestPath: "/v1/chat/completions",
		Retry:       common.GetPointer(1),
	})

	require.Nil(t, channel)
	require.NotNil(t, channelErr)
	assert.Contains(t, channelErr.Error(), "智能路由候选序列已耗尽")
}
