package service

import (
	"net/http/httptest"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	relayconstant "github.com/QuantumNous/new-api/relay/constant"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRecordSmartRoutingOutputTokensUsesAuthoritativeStreamingUsage(t *testing.T) {
	gin.SetMode(gin.TestMode)
	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
	startedAt := time.Unix(1000, 0)
	observedAt := startedAt.Add(time.Second)
	info := &relaycommon.RelayInfo{
		IsStream:    true,
		RelayFormat: types.RelayFormatOpenAI,
		RelayMode:   relayconstant.RelayModeChatCompletions,
	}
	attempt := info.BeginUpstreamAttempt(801, 0, startedAt)
	attempt.MarkFirstResponse(startedAt.Add(250 * time.Millisecond))

	recordSmartRoutingOutputTokens(ctx, info, &dto.Usage{CompletionTokens: 12}, observedAt)
	sample := attempt.Finish(observedAt.Add(5 * time.Second))

	require.True(t, sample.HasOutputTokens)
	assert.Equal(t, int64(12), sample.OutputTokens)
	assert.Equal(t, 750*time.Millisecond, sample.Generation)
	assert.InDelta(t, 16.0, sample.ThroughputTokensPerSecond, 0.000001)
}

func TestRecordSmartRoutingOutputTokensRejectsLocalEstimatesAndNonTextCases(t *testing.T) {
	gin.SetMode(gin.TestMode)
	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
	common.SetContextKey(ctx, constant.ContextKeyLocalCountTokens, true)
	info := &relaycommon.RelayInfo{
		IsStream:    true,
		RelayFormat: types.RelayFormatOpenAI,
		RelayMode:   relayconstant.RelayModeChatCompletions,
	}
	attempt := info.BeginUpstreamAttempt(802, 0, time.Now().Add(-time.Second))
	recordSmartRoutingOutputTokens(ctx, info, &dto.Usage{CompletionTokens: 20}, time.Now())
	assert.False(t, attempt.Snapshot(time.Now()).HasOutputTokens)

	nonStreamInfo := &relaycommon.RelayInfo{}
	nonStreamAttempt := nonStreamInfo.BeginUpstreamAttempt(803, 0, time.Now().Add(-time.Second))
	common.SetContextKey(ctx, constant.ContextKeyLocalCountTokens, false)
	recordSmartRoutingOutputTokens(ctx, nonStreamInfo, &dto.Usage{CompletionTokens: 20}, time.Now())
	assert.False(t, nonStreamAttempt.Snapshot(time.Now()).HasOutputTokens)

	recordSmartRoutingOutputTokens(ctx, info, &dto.Usage{CompletionTokens: 7}, time.Now())
	assert.False(t, attempt.Snapshot(time.Now()).HasOutputTokens)
}

func TestRecordSmartRoutingOutputTokensRejectsStreamingImageUsage(t *testing.T) {
	gin.SetMode(gin.TestMode)
	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
	info := &relaycommon.RelayInfo{
		IsStream:    true,
		RelayFormat: types.RelayFormatOpenAIImage,
		RelayMode:   relayconstant.RelayModeImagesGenerations,
	}
	attempt := info.BeginUpstreamAttempt(804, 0, time.Now().Add(-time.Second))

	recordSmartRoutingOutputTokens(ctx, info, &dto.Usage{CompletionTokens: 20}, time.Now())

	assert.False(t, attempt.Snapshot(time.Now()).HasOutputTokens)
}
