package service

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/pkg/billingexpr"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/types"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func prepareDurableTextBillingTest(t *testing.T, purpose string) (*gin.Context, *relaycommon.RelayInfo, *model.User, *model.Token, *model.Channel) {
	t.Helper()
	require.NoError(t, model.DB.AutoMigrate(&model.BillingOperation{}, &model.BillingOperationLogOutbox{}, &model.Log{}))
	tables := []string{"billing_operation_log_outboxes", "billing_operations", "logs", "channels", "tokens", "users"}
	for _, table := range tables {
		require.NoError(t, model.DB.Exec("DELETE FROM "+table).Error)
	}
	t.Cleanup(func() {
		for _, table := range tables {
			require.NoError(t, model.DB.Exec("DELETE FROM "+table).Error)
		}
	})
	user := &model.User{
		Username: "durable-user-" + purpose, Password: "password", Status: common.UserStatusEnabled,
		AffCode: "durable-aff-code-" + purpose,
	}
	require.NoError(t, model.DB.Create(user).Error)
	token := &model.Token{UserId: user.Id, Key: "durable-token-" + purpose, Status: common.TokenStatusEnabled, RemainQuota: 1000}
	require.NoError(t, model.DB.Create(token).Error)
	channel := &model.Channel{Name: "durable-channel-" + purpose, Key: "upstream-secret", Status: common.ChannelStatusEnabled}
	require.NoError(t, model.DB.Create(channel).Error)

	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
	ctx.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	ctx.Set(common.RequestIdKey, "request-"+purpose)
	ctx.Set("username", user.Username)
	ctx.Set("token_name", token.Name)
	info := &relaycommon.RelayInfo{
		UserId: user.Id, TokenId: token.Id, TokenKey: token.Key,
		OriginModelName: "durable-model", UsingGroup: "default", StartTime: time.Now(),
		RelayFormat: types.RelayFormatOpenAI,
		ChannelMeta: &relaycommon.ChannelMeta{ChannelId: channel.Id},
		BillingOperation: &relaycommon.BillingOperationIdentity{
			Candidates: []relaycommon.BillingOperationLookupCandidate{{
				LookupHMAC: "lookup-" + purpose, OwnerHMAC: "owner-" + purpose,
				ConversationHMAC: "conversation-" + purpose, KeyVersion: "v1",
			}},
			ExpectedRevision: 1, Purpose: purpose, Fingerprint: "fingerprint-" + purpose,
		},
	}
	return ctx, info, user, token, channel
}

func assertDurableTextReplay(t *testing.T, ctx *gin.Context, info *relaycommon.RelayInfo, first TextConsumeResult, user *model.User, token *model.Token, channel *model.Channel) {
	t.Helper()
	info.PriceData.ModelRatio = 99
	info.PriceData.ModelPrice = 99
	if info.TieredBillingSnapshot != nil {
		info.TieredBillingSnapshot.ExprString = `tier("changed", p * 999)`
	}
	replayed, err := PostTextConsumeQuotaResult(ctx, info, &dto.Usage{PromptTokens: 90, CompletionTokens: 10, TotalTokens: 100}, nil)
	require.NoError(t, err)
	assert.Equal(t, first.PromptTokens, replayed.PromptTokens)
	assert.Equal(t, first.CompletionTokens, replayed.CompletionTokens)
	assert.Equal(t, first.TotalTokens, replayed.TotalTokens)
	assert.Equal(t, first.ActualQuota, replayed.ActualQuota)

	require.NoError(t, model.DB.First(user, user.Id).Error)
	assert.Equal(t, first.ActualQuota, user.UsedQuota)
	assert.Equal(t, 1, user.RequestCount)
	require.NoError(t, model.DB.First(channel, channel.Id).Error)
	assert.Equal(t, int64(first.ActualQuota), channel.UsedQuota)
	require.NoError(t, model.DB.First(token, token.Id).Error)
	assert.Equal(t, 1000-first.ActualQuota, token.RemainQuota)
	var logCount int64
	require.NoError(t, model.LOG_DB.Model(&model.Log{}).Where("billing_operation_id = ?", info.BillingOperationId).Count(&logCount).Error)
	assert.Equal(t, int64(1), logCount)
}

func TestPostTextConsumeQuotaDurableOperationFreezesFixedBilling(t *testing.T) {
	originalBatchUpdateEnabled := common.BatchUpdateEnabled
	common.BatchUpdateEnabled = true
	t.Cleanup(func() { common.BatchUpdateEnabled = originalBatchUpdateEnabled })
	ctx, info, user, token, channel := prepareDurableTextBillingTest(t, "main-fixed")
	info.PriceData = types.PriceData{
		ModelRatio: 1, CompletionRatio: 1, ChannelRatio: 1, ChannelRatioSet: true,
		QuotaToPreConsume: 20, GroupRatioInfo: types.GroupRatioInfo{GroupRatio: 1},
	}
	require.Nil(t, PreConsumeBilling(ctx, 20, info))
	result, err := PostTextConsumeQuotaResult(ctx, info, &dto.Usage{PromptTokens: 10, CompletionTokens: 5, TotalTokens: 15}, nil)
	require.NoError(t, err)
	assert.Equal(t, 15, result.ActualQuota)
	assertDurableTextReplay(t, ctx, info, result, user, token, channel)
}

func TestNewBillingSessionDurableOperationRestoresReservedPricingSnapshot(t *testing.T) {
	ctx, info, _, token, _ := prepareDurableTextBillingTest(t, "reserved-replay")
	info.PriceData = types.PriceData{
		ModelRatio: 1, CompletionRatio: 2, ChannelRatio: 1, ChannelRatioSet: true,
		QuotaToPreConsume: 20, GroupRatioInfo: types.GroupRatioInfo{GroupRatio: 1},
	}
	require.Nil(t, PreConsumeBilling(ctx, 20, info))

	replayedInfo := *info
	replayedInfo.Billing = nil
	replayedInfo.BillingOperationId = 0
	replayedInfo.PriceData.ModelRatio = 9
	replayedInfo.PriceData.QuotaToPreConsume = 31
	replayedSession, apiErr := NewBillingSession(ctx, &replayedInfo, 31)
	require.Nil(t, apiErr)
	assert.Equal(t, 20, replayedSession.GetPreConsumedQuota())
	assert.Equal(t, float64(1), replayedInfo.PriceData.ModelRatio)
	assert.Equal(t, 20, replayedInfo.PriceData.QuotaToPreConsume)
	require.NoError(t, model.DB.First(token, token.Id).Error)
	assert.Equal(t, 980, token.RemainQuota)
}

func TestPostTextConsumeQuotaDurableOperationFreezesTieredBilling(t *testing.T) {
	ctx, info, user, token, channel := prepareDurableTextBillingTest(t, "summary")
	expression := `tier("base", p * 2 + c * 4)`
	info.PriceData = types.PriceData{
		ModelRatio: 1, CompletionRatio: 1, ChannelRatio: 1, ChannelRatioSet: true,
		QuotaToPreConsume: 20, GroupRatioInfo: types.GroupRatioInfo{GroupRatio: 1},
	}
	info.TieredBillingSnapshot = &billingexpr.BillingSnapshot{
		BillingMode: "tiered_expr", ModelName: info.OriginModelName, ExprString: expression,
		ExprHash: billingexpr.ExprHashString(expression), GroupRatio: 1, ChannelRatio: 1,
		ChannelRatioSet: true, EstimatedQuotaAfterGroup: 20, QuotaPerUnit: common.QuotaPerUnit,
	}
	require.Nil(t, PreConsumeBilling(ctx, 20, info))
	result, err := PostTextConsumeQuotaResult(ctx, info, &dto.Usage{PromptTokens: 10, CompletionTokens: 5, TotalTokens: 15}, nil)
	require.NoError(t, err)
	expected := billingexpr.QuotaRound(float64(40) / 1_000_000 * common.QuotaPerUnit)
	assert.Equal(t, expected, result.ActualQuota)
	assertDurableTextReplay(t, ctx, info, result, user, token, channel)
}

func TestPostTextConsumeQuotaDurableOperationFreezesFreeBilling(t *testing.T) {
	ctx, info, user, token, channel := prepareDurableTextBillingTest(t, "main-free")
	info.PriceData = types.PriceData{FreeModel: true, ChannelRatio: 1, ChannelRatioSet: true}
	require.Nil(t, PreConsumeBilling(ctx, 0, info))
	result, err := PostTextConsumeQuotaResult(ctx, info, &dto.Usage{PromptTokens: 10, CompletionTokens: 5, TotalTokens: 15}, nil)
	require.NoError(t, err)
	assert.Zero(t, result.ActualQuota)
	assert.NotZero(t, info.BillingOperationId)
	assertDurableTextReplay(t, ctx, info, result, user, token, channel)
}
