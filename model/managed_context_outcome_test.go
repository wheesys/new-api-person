package model

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func managedOutcomeTestIdentity(fingerprint string) ManagedContextOutcomeIdentity {
	return ManagedContextOutcomeIdentity{
		Candidates: []ManagedContextOutcomeLookupCandidate{{
			LookupHMAC: "idempotency-active", RevisionIntentHMAC: "revision-active",
			OwnerHMAC: "owner-active", ConversationHMAC: "conversation-active", KeyVersion: "v2",
		}},
		ExpectedRevision: 7, RequestFingerprint: fingerprint,
	}
}

func prepareManagedContextOutcomeModelTest(t *testing.T) {
	t.Helper()
	require.NoError(t, DB.AutoMigrate(&ManagedContextOutcome{}, &BillingOperation{}, &BillingOperationLogOutbox{}, &Log{}))
	require.NoError(t, DB.Exec("DELETE FROM managed_context_outcomes").Error)
	t.Cleanup(func() { require.NoError(t, DB.Exec("DELETE FROM managed_context_outcomes").Error) })
}

func TestManagedContextOutcomeBindsIdempotencyKeyAndRevisionIntent(t *testing.T) {
	prepareManagedContextOutcomeModelTest(t)
	identity := managedOutcomeTestIdentity("fingerprint-a")
	expiresAt := time.Now().Add(time.Hour)

	const workers = 8
	ids := make(chan int64, workers)
	errorsChannel := make(chan error, workers)
	var waitGroup sync.WaitGroup
	for range workers {
		waitGroup.Add(1)
		go func() {
			defer waitGroup.Done()
			outcome, _, err := ReserveManagedContextOutcome(context.Background(), identity, expiresAt)
			if outcome != nil {
				ids <- outcome.Id
			}
			errorsChannel <- err
		}()
	}
	waitGroup.Wait()
	close(ids)
	close(errorsChannel)
	var stableId int64
	for err := range errorsChannel {
		require.NoError(t, err)
	}
	for id := range ids {
		if stableId == 0 {
			stableId = id
		}
		assert.Equal(t, stableId, id)
	}
	var count int64
	require.NoError(t, DB.Model(&ManagedContextOutcome{}).Count(&count).Error)
	assert.Equal(t, int64(1), count)

	conflictingBody := identity
	conflictingBody.RequestFingerprint = "fingerprint-b"
	_, _, err := ReserveManagedContextOutcome(context.Background(), conflictingBody, expiresAt)
	assert.ErrorIs(t, err, ErrManagedContextOutcomeConflict)
	conflictingKey := identity
	conflictingKey.Candidates = append([]ManagedContextOutcomeLookupCandidate(nil), identity.Candidates...)
	conflictingKey.Candidates[0].LookupHMAC = "different-key"
	_, _, err = ReserveManagedContextOutcome(context.Background(), conflictingKey, expiresAt)
	assert.ErrorIs(t, err, ErrManagedContextOutcomeConflict, "one revision intent cannot be rebound to another key")
	conflictingRevision := identity
	conflictingRevision.Candidates = append([]ManagedContextOutcomeLookupCandidate(nil), identity.Candidates...)
	conflictingRevision.ExpectedRevision = 8
	conflictingRevision.Candidates[0].RevisionIntentHMAC = "different-revision"
	_, _, err = ReserveManagedContextOutcome(context.Background(), conflictingRevision, expiresAt)
	assert.ErrorIs(t, err, ErrManagedContextOutcomeConflict, "one idempotency key cannot be rebound to another revision")
}

func TestBillingSettlementAndManagedOutcomeCheckpointAreAtomic(t *testing.T) {
	user, token, channel := prepareBillingOperationTest(t)
	prepareManagedContextOutcomeModelTest(t)
	outcomeIdentity := managedOutcomeTestIdentity("outcome-fingerprint")
	outcome, _, err := ReserveManagedContextOutcome(context.Background(), outcomeIdentity, time.Now().Add(time.Hour))
	require.NoError(t, err)
	require.NoError(t, AdvanceManagedContextOutcomePhase(context.Background(), outcome.Id, outcomeIdentity.RequestFingerprint, ManagedContextOutcomePhaseIntent, ManagedContextOutcomePhaseMainDispatched))

	billingIdentity := billingOperationTestIdentity("billing-fingerprint")
	operation, err := ReserveBillingOperation(context.Background(), BillingOperationReserveRequest{
		Identity: billingIdentity, UserId: user.Id, TokenId: token.Id, ChannelId: channel.Id, ReservedQuota: 25, PricingSnapshot: `{}`,
	})
	require.NoError(t, err)
	settlement := BillingOperationSettlement{
		ActualQuota: 15, PromptTokens: 10, CompletionTokens: 5, TotalTokens: 15,
		BillingMode: "fixed", CountUsage: true, LogUserId: user.Id,
		LogParams: RecordConsumeLogParams{ChannelId: channel.Id, TokenId: token.Id, Quota: 15, PromptTokens: 10, CompletionTokens: 5, Other: map[string]interface{}{}},
		OutcomeCheckpoint: &ManagedContextOutcomeCheckpoint{
			OutcomeId: outcome.Id + 1, RequestFingerprint: outcomeIdentity.RequestFingerprint,
			ExpectedPhase: ManagedContextOutcomePhaseMainDispatched, NextPhase: ManagedContextOutcomePhaseMainSettled,
			ResponseStatus: 200, ResponseContentType: "application/json", ResponsePayload: []byte("cipher-response"), AssistantPayload: []byte("cipher-assistant"),
			SummaryExecutionPayload: []byte("cipher-summary-execution"),
		},
	}
	_, _, err = SettleBillingOperation(context.Background(), operation.Id, billingIdentity.Fingerprint, settlement)
	assert.ErrorIs(t, err, ErrManagedContextOutcomePhaseConflict)
	require.NoError(t, DB.First(token, token.Id).Error)
	assert.Equal(t, 75, token.RemainQuota)
	assert.Equal(t, 25, token.UsedQuota)
	require.NoError(t, DB.First(operation, operation.Id).Error)
	assert.Equal(t, BillingOperationStateReserved, operation.State)

	settlement.OutcomeCheckpoint.OutcomeId = outcome.Id
	_, settledNow, err := SettleBillingOperation(context.Background(), operation.Id, billingIdentity.Fingerprint, settlement)
	require.NoError(t, err)
	assert.True(t, settledNow)
	require.NoError(t, DB.First(outcome, outcome.Id).Error)
	assert.Equal(t, ManagedContextOutcomePhaseMainSettled, outcome.Phase)
	assert.Equal(t, operation.Id, outcome.MainBillingOperationId)
	assert.Equal(t, []byte("cipher-response"), outcome.ResponsePayload)

	require.NoError(t, AdvanceManagedContextOutcomePhase(context.Background(), outcome.Id, outcomeIdentity.RequestFingerprint, ManagedContextOutcomePhaseMainSettled, ManagedContextOutcomePhaseSummaryDispatched))
	summaryIdentity := billingOperationTestIdentity("summary-billing-fingerprint")
	summaryIdentity.Purpose = "summary"
	summaryOperation, err := ReserveBillingOperation(context.Background(), BillingOperationReserveRequest{
		Identity: summaryIdentity, UserId: user.Id, TokenId: token.Id, ChannelId: channel.Id, ReservedQuota: 5, PricingSnapshot: `{}`,
	})
	require.NoError(t, err)
	summarySettlement := settlement
	summarySettlement.ActualQuota = 5
	summarySettlement.PromptTokens = 3
	summarySettlement.CompletionTokens = 2
	summarySettlement.TotalTokens = 5
	summarySettlement.LogParams.Quota = 5
	summarySettlement.LogParams.PromptTokens = 3
	summarySettlement.LogParams.CompletionTokens = 2
	summarySettlement.OutcomeCheckpoint = &ManagedContextOutcomeCheckpoint{
		OutcomeId: outcome.Id + 1, RequestFingerprint: outcomeIdentity.RequestFingerprint,
		ExpectedPhase: ManagedContextOutcomePhaseSummaryDispatched, NextPhase: ManagedContextOutcomePhaseSettledPendingCommit,
		SummaryResultPayload: []byte("cipher-summary-result"), NextStatePayload: []byte("cipher-next-state"),
	}
	_, _, err = SettleBillingOperation(context.Background(), summaryOperation.Id, summaryIdentity.Fingerprint, summarySettlement)
	assert.ErrorIs(t, err, ErrManagedContextOutcomePhaseConflict)
	require.NoError(t, DB.First(summaryOperation, summaryOperation.Id).Error)
	assert.Equal(t, BillingOperationStateReserved, summaryOperation.State)
	require.NoError(t, DB.First(outcome, outcome.Id).Error)
	assert.Equal(t, ManagedContextOutcomePhaseSummaryDispatched, outcome.Phase)

	summarySettlement.OutcomeCheckpoint.OutcomeId = outcome.Id
	_, settledNow, err = SettleBillingOperation(context.Background(), summaryOperation.Id, summaryIdentity.Fingerprint, summarySettlement)
	require.NoError(t, err)
	assert.True(t, settledNow)
	require.NoError(t, DB.First(outcome, outcome.Id).Error)
	assert.Equal(t, ManagedContextOutcomePhaseSettledPendingCommit, outcome.Phase)
	assert.Equal(t, summaryOperation.Id, outcome.SummaryBillingOperationId)
	assert.Equal(t, []byte("cipher-next-state"), outcome.NextStatePayload)
}

func TestManagedContextOutcomeExpiryDoesNotExtendOnReplay(t *testing.T) {
	prepareManagedContextOutcomeModelTest(t)
	identity := managedOutcomeTestIdentity("fingerprint-expiry")
	expiresAt := time.Now().Add(time.Hour)
	outcome, _, err := ReserveManagedContextOutcome(context.Background(), identity, expiresAt)
	require.NoError(t, err)
	replayed, created, err := ReserveManagedContextOutcome(context.Background(), identity, time.Now().Add(time.Hour))
	require.NoError(t, err)
	assert.False(t, created)
	assert.Equal(t, outcome.ExpiresAt.UnixNano(), replayed.ExpiresAt.UnixNano())
	require.NoError(t, DB.Model(&ManagedContextOutcome{}).Where("id = ?", outcome.Id).Updates(map[string]interface{}{
		"expires_at": time.Now().Add(-time.Second), "response_payload": []byte("ciphertext"), "next_state_payload": []byte("cipher-state"),
	}).Error)
	_, _, err = ReserveManagedContextOutcome(context.Background(), identity, time.Now().Add(time.Hour))
	assert.ErrorIs(t, err, ErrManagedContextOutcomeExpired)
	require.NoError(t, DB.First(outcome, outcome.Id).Error)
	assert.Equal(t, ManagedContextOutcomePhaseExpired, outcome.Phase)
	assert.NotNil(t, outcome.ExpiredAt)
	assert.Empty(t, outcome.ResponsePayload)
	assert.Empty(t, outcome.NextStatePayload)
}
