package model

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/QuantumNous/new-api/common"

	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func prepareBillingOperationTest(t *testing.T) (*User, *Token, *Channel) {
	t.Helper()
	require.NoError(t, DB.AutoMigrate(&BillingOperation{}, &BillingOperationLogOutbox{}, &Log{}))
	tables := []string{"billing_operation_log_outboxes", "billing_operations", "logs", "channels", "tokens", "users"}
	for _, table := range tables {
		require.NoError(t, DB.Exec("DELETE FROM "+table).Error)
	}
	t.Cleanup(func() {
		for _, table := range tables {
			require.NoError(t, DB.Exec("DELETE FROM "+table).Error)
		}
	})
	user := &User{Username: "billing-user", Password: "password", Status: common.UserStatusEnabled, AffCode: "billing-aff-code"}
	require.NoError(t, DB.Create(user).Error)
	token := &Token{UserId: user.Id, Key: "billing-token", Status: common.TokenStatusEnabled, RemainQuota: 100}
	require.NoError(t, DB.Create(token).Error)
	channel := &Channel{Name: "billing-channel", Key: "upstream-secret", Status: common.ChannelStatusEnabled}
	require.NoError(t, DB.Create(channel).Error)
	return user, token, channel
}

func billingOperationTestIdentity(fingerprint string) BillingOperationIdentity {
	return BillingOperationIdentity{
		Candidates: []BillingOperationLookupCandidate{{
			LookupHMAC: "lookup-active", OwnerHMAC: "owner-active",
			ConversationHMAC: "conversation-active", KeyVersion: "v2",
		}},
		ExpectedRevision: 7,
		Purpose:          "main",
		Fingerprint:      fingerprint,
	}
}

func billingOperationTestContext() *gin.Context {
	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
	ctx.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	ctx.Set(common.RequestIdKey, "request-operation")
	ctx.Set("username", "billing-user")
	return ctx
}

func TestBillingOperationSettlementFreezesAccountingAndLog(t *testing.T) {
	user, token, channel := prepareBillingOperationTest(t)
	identity := billingOperationTestIdentity("fingerprint-a")
	request := BillingOperationReserveRequest{
		Identity: identity, UserId: user.Id, TokenId: token.Id, ChannelId: channel.Id,
		ReservedQuota: 30, PricingSnapshot: `{"mode":"fixed"}`,
	}

	operation, err := ReserveBillingOperation(context.Background(), request)
	require.NoError(t, err)
	replayed, err := ReserveBillingOperation(context.Background(), request)
	require.NoError(t, err)
	assert.Equal(t, operation.Id, replayed.Id)
	require.NoError(t, DB.First(token, token.Id).Error)
	assert.Equal(t, 70, token.RemainQuota)
	assert.Equal(t, 30, token.UsedQuota)

	conflicting := request
	conflicting.Identity.Fingerprint = "fingerprint-b"
	_, err = ReserveBillingOperation(context.Background(), conflicting)
	assert.ErrorIs(t, err, ErrBillingOperationFingerprintConflict)
	conflicting = request
	conflicting.ReservedQuota = 31
	conflicting.PricingSnapshot = `{"mode":"changed"}`
	replayed, err = ReserveBillingOperation(context.Background(), conflicting)
	require.NoError(t, err)
	assert.Equal(t, operation.Id, replayed.Id)
	assert.Equal(t, 30, replayed.ReservedQuota)
	assert.Equal(t, `{"mode":"fixed"}`, replayed.PricingSnapshot)

	settlement := BillingOperationSettlement{
		ActualQuota: 20, PromptTokens: 8, CompletionTokens: 4, TotalTokens: 12,
		BillingMode: "fixed", CountUsage: true, LogUserId: user.Id,
		LogParams: RecordConsumeLogParams{
			ChannelId: channel.Id, PromptTokens: 8, CompletionTokens: 4,
			ModelName: "billing-model", TokenName: "billing-token", Quota: 20,
			TokenId: token.Id, Group: "default", Other: map[string]interface{}{"billing_mode": "fixed"},
		},
	}
	settled, settledNow, err := SettleBillingOperation(context.Background(), operation.Id, identity.Fingerprint, settlement)
	require.NoError(t, err)
	assert.True(t, settledNow)
	assert.Equal(t, BillingOperationStateSettled, settled.State)

	settlement.ActualQuota = 99
	settlement.PromptTokens = 99
	replayedSettlement, settledNow, err := SettleBillingOperation(context.Background(), operation.Id, identity.Fingerprint, settlement)
	require.NoError(t, err)
	assert.False(t, settledNow)
	assert.Equal(t, 20, replayedSettlement.ActualQuota)

	require.NoError(t, DB.First(token, token.Id).Error)
	assert.Equal(t, 80, token.RemainQuota)
	assert.Equal(t, 20, token.UsedQuota)
	require.NoError(t, DB.First(user, user.Id).Error)
	assert.Equal(t, 20, user.UsedQuota)
	assert.Equal(t, 1, user.RequestCount)
	require.NoError(t, DB.First(channel, channel.Id).Error)
	assert.Equal(t, int64(20), channel.UsedQuota)

	ctx := billingOperationTestContext()
	// Simulate LOG_DB insert success followed by loss of the outbox acknowledgement.
	var pendingOutbox BillingOperationLogOutbox
	require.NoError(t, DB.First(&pendingOutbox, "billing_operation_id = ?", operation.Id).Error)
	var pendingPayload billingOperationConsumeLogPayload
	require.NoError(t, common.Unmarshal([]byte(pendingOutbox.Payload), &pendingPayload))
	pendingPayload.Params.BillingOperationId = &operation.Id
	recorded, err := RecordConsumeLogResult(ctx, pendingPayload.UserId, pendingPayload.Params)
	require.NoError(t, err)
	assert.True(t, recorded)

	recorded, err = DeliverBillingOperationConsumeLog(ctx, operation.Id)
	require.NoError(t, err)
	assert.True(t, recorded)
	recorded, err = DeliverBillingOperationConsumeLog(ctx, operation.Id)
	require.NoError(t, err)
	assert.True(t, recorded)
	var logCount int64
	require.NoError(t, LOG_DB.Model(&Log{}).Where("billing_operation_id = ?", operation.Id).Count(&logCount).Error)
	assert.Equal(t, int64(1), logCount)
}

func TestBillingOperationSettlementFailureRollsBackEveryMutation(t *testing.T) {
	user, token, channel := prepareBillingOperationTest(t)
	identity := billingOperationTestIdentity("fingerprint-rollback")
	operation, err := ReserveBillingOperation(context.Background(), BillingOperationReserveRequest{
		Identity: identity, UserId: user.Id, TokenId: token.Id, ChannelId: channel.Id,
		ReservedQuota: 30, PricingSnapshot: `{}`,
	})
	require.NoError(t, err)
	require.NoError(t, DB.Delete(channel).Error)

	_, _, err = SettleBillingOperation(context.Background(), operation.Id, identity.Fingerprint, BillingOperationSettlement{
		ActualQuota: 50, PromptTokens: 10, CompletionTokens: 5, TotalTokens: 15,
		BillingMode: "tiered_expr", CountUsage: true, LogUserId: user.Id,
		LogParams: RecordConsumeLogParams{
			TokenId: token.Id, ChannelId: channel.Id, Quota: 50,
			PromptTokens: 10, CompletionTokens: 5, Other: map[string]interface{}{},
		},
	})
	require.Error(t, err)

	require.NoError(t, DB.First(token, token.Id).Error)
	assert.Equal(t, 70, token.RemainQuota)
	assert.Equal(t, 30, token.UsedQuota)
	require.NoError(t, DB.First(user, user.Id).Error)
	assert.Zero(t, user.UsedQuota)
	assert.Zero(t, user.RequestCount)
	require.NoError(t, DB.First(operation, operation.Id).Error)
	assert.Equal(t, BillingOperationStateReserved, operation.State)
	var outboxCount int64
	require.NoError(t, DB.Model(&BillingOperationLogOutbox{}).Count(&outboxCount).Error)
	assert.Zero(t, outboxCount)
}

func TestBillingOperationConcurrentReplayAndRefundAreIdempotent(t *testing.T) {
	user, token, channel := prepareBillingOperationTest(t)
	request := BillingOperationReserveRequest{
		Identity: billingOperationTestIdentity("fingerprint-concurrent"),
		UserId:   user.Id, TokenId: token.Id, ChannelId: channel.Id, ReservedQuota: 25, PricingSnapshot: `{}`,
	}

	const workers = 8
	operations := make(chan *BillingOperation, workers)
	errorsChannel := make(chan error, workers)
	var waitGroup sync.WaitGroup
	for range workers {
		waitGroup.Add(1)
		go func() {
			defer waitGroup.Done()
			operation, err := ReserveBillingOperation(context.Background(), request)
			operations <- operation
			errorsChannel <- err
		}()
	}
	waitGroup.Wait()
	close(operations)
	close(errorsChannel)
	for err := range errorsChannel {
		require.NoError(t, err)
	}
	var operationId int64
	for operation := range operations {
		require.NotNil(t, operation)
		if operationId == 0 {
			operationId = operation.Id
		}
		assert.Equal(t, operationId, operation.Id)
	}
	require.NoError(t, DB.First(token, token.Id).Error)
	assert.Equal(t, 75, token.RemainQuota)

	refundErrors := make(chan error, workers)
	for range workers {
		waitGroup.Add(1)
		go func() {
			defer waitGroup.Done()
			_, err := RefundBillingOperation(context.Background(), operationId, request.Identity.Fingerprint)
			refundErrors <- err
		}()
	}
	waitGroup.Wait()
	close(refundErrors)
	for err := range refundErrors {
		require.NoError(t, err)
	}
	require.NoError(t, DB.First(token, token.Id).Error)
	assert.Equal(t, 100, token.RemainQuota)
	assert.Zero(t, token.UsedQuota)
}

func TestBillingOperationLookupCandidatesMigrateAndRejectDuplicates(t *testing.T) {
	user, token, channel := prepareBillingOperationTest(t)
	oldIdentity := BillingOperationIdentity{
		Candidates: []BillingOperationLookupCandidate{{
			LookupHMAC: "lookup-old", OwnerHMAC: "owner-old", ConversationHMAC: "conversation-old", KeyVersion: "v1",
		}},
		ExpectedRevision: 3, Purpose: "summary", Fingerprint: "fingerprint-rotation",
	}
	operation, err := ReserveBillingOperation(context.Background(), BillingOperationReserveRequest{
		Identity: oldIdentity, UserId: user.Id, TokenId: token.Id, ChannelId: channel.Id, PricingSnapshot: `{}`,
	})
	require.NoError(t, err)

	rotatedIdentity := oldIdentity
	rotatedIdentity.Candidates = []BillingOperationLookupCandidate{
		{LookupHMAC: "lookup-new", OwnerHMAC: "owner-new", ConversationHMAC: "conversation-new", KeyVersion: "v2"},
		oldIdentity.Candidates[0],
	}
	rotated, err := ReserveBillingOperation(context.Background(), BillingOperationReserveRequest{
		Identity: rotatedIdentity, UserId: user.Id, TokenId: token.Id, ChannelId: channel.Id, PricingSnapshot: `{}`,
	})
	require.NoError(t, err)
	assert.Equal(t, operation.Id, rotated.Id)
	assert.Equal(t, "lookup-new", rotated.LookupHMAC)
	assert.Equal(t, "v2", rotated.LookupKeyVersion)

	duplicate := &BillingOperation{
		LookupHMAC: "lookup-old", OwnerHMAC: "owner-old", ConversationHMAC: "conversation-old", LookupKeyVersion: "v1",
		ExpectedRevision: 3, Purpose: "summary", Fingerprint: "fingerprint-rotation", State: BillingOperationStateReserved,
		UserId: user.Id, TokenId: token.Id, ChannelId: channel.Id, PricingSnapshot: `{}`,
	}
	require.NoError(t, DB.Create(duplicate).Error)
	_, err = ReserveBillingOperation(context.Background(), BillingOperationReserveRequest{
		Identity: rotatedIdentity, UserId: user.Id, TokenId: token.Id, ChannelId: channel.Id, PricingSnapshot: `{}`,
	})
	assert.ErrorIs(t, err, ErrBillingOperationLookupConflict)
}

func TestBillingOperationConsumeLogFailsClosedForClickHouse(t *testing.T) {
	previousType := common.LogDatabaseType()
	t.Cleanup(func() { common.SetLogDatabaseType(previousType) })
	common.SetLogDatabaseType(common.DatabaseTypeClickHouse)
	operationId := int64(1)
	_, err := RecordConsumeLogResult(billingOperationTestContext(), 1, RecordConsumeLogParams{
		BillingOperationId: &operationId,
	})
	assert.Error(t, err)
	assert.Error(t, ValidateBillingOperationLogBackend())
}

func TestBillingOperationOutboxDeliversOnceToSeparateSQLLogDatabase(t *testing.T) {
	user, token, channel := prepareBillingOperationTest(t)
	separateLogDatabase, err := gorm.Open(sqlite.Open("file:billing-operation-log?mode=memory&cache=shared"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, separateLogDatabase.AutoMigrate(&Log{}))
	originalLogDatabase := LOG_DB
	LOG_DB = separateLogDatabase
	t.Cleanup(func() { LOG_DB = originalLogDatabase })

	identity := billingOperationTestIdentity("fingerprint-separate-log")
	operation, err := ReserveBillingOperation(context.Background(), BillingOperationReserveRequest{
		Identity: identity, UserId: user.Id, TokenId: token.Id, ChannelId: channel.Id,
		ReservedQuota: 10, PricingSnapshot: `{}`,
	})
	require.NoError(t, err)
	_, _, err = SettleBillingOperation(context.Background(), operation.Id, identity.Fingerprint, BillingOperationSettlement{
		ActualQuota: 10, PromptTokens: 1, TotalTokens: 1, BillingMode: "fixed", CountUsage: true, LogUserId: user.Id,
		LogParams: RecordConsumeLogParams{
			ChannelId: channel.Id, TokenId: token.Id, Quota: 10, PromptTokens: 1, Other: map[string]interface{}{},
		},
	})
	require.NoError(t, err)
	contextValue := billingOperationTestContext()
	recorded, err := DeliverBillingOperationConsumeLog(contextValue, operation.Id)
	require.NoError(t, err)
	assert.True(t, recorded)
	recorded, err = DeliverBillingOperationConsumeLog(contextValue, operation.Id)
	require.NoError(t, err)
	assert.True(t, recorded)
	var logCount int64
	require.NoError(t, separateLogDatabase.Model(&Log{}).Where("billing_operation_id = ?", operation.Id).Count(&logCount).Error)
	assert.Equal(t, int64(1), logCount)
}

func TestRefundSettledBillingOperationDoesNotReverseCharge(t *testing.T) {
	user, token, channel := prepareBillingOperationTest(t)
	identity := billingOperationTestIdentity("fingerprint-settled-refund")
	operation, err := ReserveBillingOperation(context.Background(), BillingOperationReserveRequest{
		Identity: identity, UserId: user.Id, TokenId: token.Id, ChannelId: channel.Id,
		ReservedQuota: 10, PricingSnapshot: `{}`,
	})
	require.NoError(t, err)
	_, _, err = SettleBillingOperation(context.Background(), operation.Id, identity.Fingerprint, BillingOperationSettlement{
		ActualQuota: 10, PromptTokens: 1, TotalTokens: 1, BillingMode: "fixed", CountUsage: true, LogUserId: user.Id,
		LogParams: RecordConsumeLogParams{
			ChannelId: channel.Id, TokenId: token.Id, Quota: 10, PromptTokens: 1, Other: map[string]interface{}{},
		},
	})
	require.NoError(t, err)
	refunded, err := RefundBillingOperation(context.Background(), operation.Id, identity.Fingerprint)
	require.NoError(t, err)
	assert.Equal(t, BillingOperationStateSettled, refunded.State)
	require.NoError(t, DB.First(token, token.Id).Error)
	assert.Equal(t, 90, token.RemainQuota)
}
