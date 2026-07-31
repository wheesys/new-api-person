package model

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func prepareManagedProviderFileModelTest(t *testing.T) {
	t.Helper()
	require.NoError(t, DB.AutoMigrate(
		&Channel{},
		&ManagedProviderFileLifecycle{},
		&ManagedProviderFileDeletionOutbox{},
		&ManagedProviderFileLifecycleEvent{},
	))
	require.NoError(t, DB.Where("id = ?", 41).FirstOrCreate(&Channel{Id: 41, Type: 1, Key: "provider-file-test-key", Name: "provider-file-test"}).Error)
	require.NoError(t, DB.Exec("DELETE FROM managed_provider_file_lifecycle_events").Error)
	require.NoError(t, DB.Exec("DELETE FROM managed_provider_file_deletion_outboxes").Error)
	require.NoError(t, DB.Exec("DELETE FROM managed_provider_file_lifecycles").Error)
	t.Cleanup(func() {
		require.NoError(t, DB.Exec("DELETE FROM managed_provider_file_lifecycle_events").Error)
		require.NoError(t, DB.Exec("DELETE FROM managed_provider_file_deletion_outboxes").Error)
		require.NoError(t, DB.Exec("DELETE FROM managed_provider_file_lifecycles").Error)
	})
}

func managedProviderFileDigest(character string) string {
	return strings.Repeat(character, 64)
}

func managedProviderFileIntent() ManagedProviderFileLifecycle {
	return ManagedProviderFileLifecycle{
		UploadIntentHMAC:                managedProviderFileDigest("1"),
		HandleLookupHMAC:                managedProviderFileDigest("0"),
		OwnerHMAC:                       managedProviderFileDigest("2"),
		LookupKeyVersion:                "hmac-v1",
		RequestFingerprint:              managedProviderFileDigest("3"),
		Provider:                        "openai",
		State:                           ManagedProviderFileLifecycleStateIntent,
		ChannelId:                       41,
		ChannelType:                     1,
		ChannelMultiKeyIndex:            0,
		CredentialFingerprint:           managedProviderFileDigest("4"),
		CredentialFingerprintKeyVersion: "hmac-v1",
		EndpointFingerprint:             managedProviderFileDigest("5"),
		ProviderScopeFingerprint:        managedProviderFileDigest("6"),
		PayloadKeyVersion:               "aead-v1",
		Purpose:                         "user_data",
	}
}

func managedProviderFileEvent(sequence int64, eventType, fromState, toState string, attemptCount int, resultCode, eventCharacter string) ManagedProviderFileLifecycleEvent {
	return ManagedProviderFileLifecycleEvent{
		Sequence:       sequence,
		EventHMAC:      managedProviderFileDigest(eventCharacter),
		EventType:      eventType,
		FromState:      fromState,
		ToState:        toState,
		AttemptCount:   attemptCount,
		ResultCode:     resultCode,
		EvidenceDigest: managedProviderFileDigest("a"),
		KeyVersion:     "hmac-v1",
	}
}

func TestManagedProviderFileLifecycleIntentIsIdempotentAndOwnerBound(t *testing.T) {
	prepareManagedProviderFileModelTest(t)
	intent := managedProviderFileIntent()
	event := managedProviderFileEvent(1, ManagedProviderFileLifecycleEventIntentCreated, "", ManagedProviderFileLifecycleStateIntent, 0, "", "b")

	created, createdNow, err := CreateManagedProviderFileLifecycleIntent(context.Background(), intent, event)
	require.NoError(t, err)
	assert.True(t, createdNow)
	assert.Positive(t, created.Id)

	replayed, createdNow, err := CreateManagedProviderFileLifecycleIntent(context.Background(), intent, event)
	require.NoError(t, err)
	assert.False(t, createdNow)
	assert.Equal(t, created.Id, replayed.Id)
	require.NoError(t, created.Validate())
	intentLookup, err := FindManagedProviderFileLifecycleByUploadIntent(context.Background(), []ManagedProviderFileUploadIntentLookupCandidate{{
		UploadIntentHMAC: intent.UploadIntentHMAC, OwnerHMAC: intent.OwnerHMAC, KeyVersion: intent.LookupKeyVersion,
	}})
	require.NoError(t, err)
	assert.Equal(t, created.Id, intentLookup.Id)

	staleEvent := managedProviderFileEvent(2, ManagedProviderFileLifecycleEventUploadDispatched,
		ManagedProviderFileLifecycleStateIntent, ManagedProviderFileLifecycleStateUploadDispatched, 0, "", "c")
	staleEvent.LifecycleId = created.Id
	staleEvent.PreviousEventHMAC = created.LastEventHMAC
	err = AdvanceManagedProviderFileUploadState(context.Background(), ManagedProviderFileUploadTransition{
		LifecycleId: created.Id, ExpectedVersion: created.Version + 1, RequestFingerprint: intent.RequestFingerprint,
		ExpectedState: ManagedProviderFileLifecycleStateIntent, NextState: ManagedProviderFileLifecycleStateUploadDispatched,
		Event: staleEvent,
	})
	assert.ErrorIs(t, err, ErrManagedProviderFileLifecycleStateConflict)

	conflict := intent
	conflict.OwnerHMAC = managedProviderFileDigest("c")
	_, _, err = CreateManagedProviderFileLifecycleIntent(context.Background(), conflict, event)
	assert.ErrorIs(t, err, ErrManagedProviderFileLifecycleConflict)
	conflictingHandle := intent
	conflictingHandle.UploadIntentHMAC = managedProviderFileDigest("d")
	_, _, err = CreateManagedProviderFileLifecycleIntent(context.Background(), conflictingHandle, event)
	assert.ErrorIs(t, err, ErrManagedProviderFileLifecycleConflict)
	replayedWithDifferentGeneratedHandle := intent
	replayedWithDifferentGeneratedHandle.HandleLookupHMAC = managedProviderFileDigest("e")
	replayed, createdNow, err = CreateManagedProviderFileLifecycleIntent(context.Background(), replayedWithDifferentGeneratedHandle, event)
	require.NoError(t, err)
	assert.False(t, createdNow)
	assert.Equal(t, created.Id, replayed.Id)
	assert.Equal(t, intent.HandleLookupHMAC, replayed.HandleLookupHMAC)
	boundChannel, err := GetChannelById(intent.ChannelId, true)
	require.NoError(t, err)
	boundChannel.Key = "rotated-provider-file-test-key"
	assert.ErrorIs(t, boundChannel.Update(), ErrManagedProviderFileChannelBound)
	boundChannel, err = GetChannelById(intent.ChannelId, true)
	require.NoError(t, err)
	boundChannel.BaseURL = common.GetPointer("https://example.com")
	assert.ErrorIs(t, boundChannel.Update(), ErrManagedProviderFileChannelBound)
	boundChannel, err = GetChannelById(intent.ChannelId, true)
	require.NoError(t, err)
	boundChannel.OpenAIOrganization = common.GetPointer("org-rotated")
	assert.ErrorIs(t, boundChannel.Update(), ErrManagedProviderFileChannelBound)
	boundChannel, err = GetChannelById(intent.ChannelId, true)
	require.NoError(t, err)
	boundChannel.ParamOverride = common.GetPointer(`{"temperature":0}`)
	assert.ErrorIs(t, boundChannel.Update(), ErrManagedProviderFileChannelBound)
	assert.ErrorIs(t, (&Channel{Id: intent.ChannelId}).Delete(), ErrManagedProviderFileChannelBound)
	_, batchDeleteErr := BatchDeleteChannels([]int{intent.ChannelId})
	assert.ErrorIs(t, batchDeleteErr, ErrManagedProviderFileChannelBound)
	statusChannel, err := GetChannelById(intent.ChannelId, true)
	require.NoError(t, err)
	_, err = DeleteChannelByStatus(int64(statusChannel.Status))
	assert.ErrorIs(t, err, ErrManagedProviderFileChannelBound)

	var eventCount int64
	require.NoError(t, DB.Model(&ManagedProviderFileLifecycleEvent{}).Where("lifecycle_id = ?", created.Id).Count(&eventCount).Error)
	assert.Equal(t, int64(1), eventCount)
	var storedEvent ManagedProviderFileLifecycleEvent
	require.NoError(t, DB.First(&storedEvent, "lifecycle_id = ?", created.Id).Error)
	assert.ErrorIs(t, DB.Model(&storedEvent).Update("result_code", "changed").Error, ErrManagedProviderFileEventAppendOnly)
	assert.ErrorIs(t, DB.Delete(&storedEvent).Error, ErrManagedProviderFileEventAppendOnly)
}

func TestManagedProviderFileActivationLookupAndDeletionScrubSensitivePayload(t *testing.T) {
	prepareManagedProviderFileModelTest(t)
	intent := managedProviderFileIntent()
	created, _, err := CreateManagedProviderFileLifecycleIntent(context.Background(), intent,
		managedProviderFileEvent(1, ManagedProviderFileLifecycleEventIntentCreated, "", ManagedProviderFileLifecycleStateIntent, 0, "", "b"))
	require.NoError(t, err)
	dispatchedEvent := managedProviderFileEvent(2, ManagedProviderFileLifecycleEventUploadDispatched,
		ManagedProviderFileLifecycleStateIntent, ManagedProviderFileLifecycleStateUploadDispatched, 0, "", "c")
	dispatchedEvent.LifecycleId = created.Id
	dispatchedEvent.PreviousEventHMAC = created.LastEventHMAC
	require.NoError(t, AdvanceManagedProviderFileUploadState(context.Background(), ManagedProviderFileUploadTransition{
		LifecycleId: created.Id, ExpectedVersion: created.Version, RequestFingerprint: intent.RequestFingerprint,
		ExpectedState: ManagedProviderFileLifecycleStateIntent, NextState: ManagedProviderFileLifecycleStateUploadDispatched,
		Event: dispatchedEvent,
	}))

	providerCreatedAt := time.Now().UTC().Add(-time.Minute).Truncate(time.Second)
	metadataVerifiedAt := time.Now().UTC().Truncate(time.Second)
	expiresAt := time.Now().UTC().Add(time.Hour).Truncate(time.Second)
	providerLookupHMAC := managedProviderFileDigest("7")
	providerPayload := []byte("encrypted-provider-file-reference")
	activationEvent := managedProviderFileEvent(3, ManagedProviderFileLifecycleEventActivated,
		ManagedProviderFileLifecycleStateUploadDispatched, ManagedProviderFileLifecycleStateActive, 0, "", "d")
	activationEvent.LifecycleId = created.Id
	activationEvent.PreviousEventHMAC = dispatchedEvent.EventHMAC
	activation := ManagedProviderFileLifecycleActivation{
		LifecycleId: created.Id, ExpectedVersion: created.Version + 1, RequestFingerprint: intent.RequestFingerprint,
		ProviderLookupHMAC: providerLookupHMAC, ProviderPayload: providerPayload,
		ProviderBytes: 1024, ProviderCreatedAt: providerCreatedAt, MetadataVerifiedAt: metadataVerifiedAt, ExpiresAt: expiresAt,
		DeletionOperationHMAC: managedProviderFileDigest("8"), MaxDeletionAttempts: 3,
		Event: activationEvent,
	}
	activated, outbox, activatedNow, err := ActivateManagedProviderFileLifecycle(context.Background(), activation)
	require.NoError(t, err)
	assert.True(t, activatedNow)
	require.NoError(t, activated.Validate())
	assert.Equal(t, expiresAt, outbox.NextAttemptAt)
	replayed, replayedOutbox, activatedNow, err := ActivateManagedProviderFileLifecycle(context.Background(), activation)
	require.NoError(t, err)
	assert.False(t, activatedNow)
	assert.Equal(t, activated.Id, replayed.Id)
	assert.Equal(t, outbox.Id, replayedOutbox.Id)

	found, err := FindManagedProviderFileLifecycle(context.Background(), []ManagedProviderFileLifecycleLookupCandidate{{
		HandleLookupHMAC: intent.HandleLookupHMAC,
		OwnerHMAC:        intent.OwnerHMAC,
		KeyVersion:       intent.LookupKeyVersion,
	}})
	require.NoError(t, err)
	assert.Equal(t, created.Id, found.Id)

	_, err = FindManagedProviderFileLifecycle(context.Background(), []ManagedProviderFileLifecycleLookupCandidate{{
		HandleLookupHMAC: intent.HandleLookupHMAC,
		OwnerHMAC:        managedProviderFileDigest("9"),
		KeyVersion:       intent.LookupKeyVersion,
	}})
	assert.ErrorIs(t, err, ErrManagedProviderFileLifecycleConflict)

	dueAt := time.Now().UTC().Add(-time.Second)
	require.NoError(t, DB.Model(&ManagedProviderFileDeletionOutbox{}).Where("id = ?", outbox.Id).Update("next_attempt_at", dueAt).Error)
	leaseUntil := time.Now().UTC().Add(time.Minute)
	claimEvent := managedProviderFileEvent(4, ManagedProviderFileLifecycleEventDeletionStarted,
		ManagedProviderFileLifecycleStateActive, ManagedProviderFileLifecycleStateDeletionPending, 1, "", "e")
	claimEvent.LifecycleId = created.Id
	claimEvent.PreviousEventHMAC = activationEvent.EventHMAC
	_, staleClaimed, err := ClaimManagedProviderFileDeletion(context.Background(), ManagedProviderFileDeletionClaim{
		OutboxId: outbox.Id, ExpectedVersion: outbox.Version + 1, LeaseTokenHMAC: managedProviderFileDigest("9"), LeaseExpiresAt: leaseUntil,
		ExpectedState: ManagedProviderFileDeletionOutboxStatePending, ExpectedAttemptCount: 0,
		Event: claimEvent,
	})
	require.NoError(t, err)
	assert.False(t, staleClaimed)
	claimed, claimedNow, err := ClaimManagedProviderFileDeletion(context.Background(), ManagedProviderFileDeletionClaim{
		OutboxId: outbox.Id, ExpectedVersion: outbox.Version, LeaseTokenHMAC: managedProviderFileDigest("9"), LeaseExpiresAt: leaseUntil,
		ExpectedState: ManagedProviderFileDeletionOutboxStatePending, ExpectedAttemptCount: 0,
		Event: claimEvent,
	})
	require.NoError(t, err)
	assert.True(t, claimedNow)
	assert.Equal(t, 1, claimed.AttemptCount)
	prematureCompletionEvent := managedProviderFileEvent(5, ManagedProviderFileLifecycleEventDeletionCompleted,
		ManagedProviderFileLifecycleStateDeletionPending, ManagedProviderFileLifecycleStateDeleted, 1,
		ManagedProviderFileDeletionResultDeleted, "f")
	prematureCompletionEvent.LifecycleId = created.Id
	prematureCompletionEvent.PreviousEventHMAC = claimEvent.EventHMAC
	err = CompleteManagedProviderFileDeletion(context.Background(), ManagedProviderFileDeletionTerminal{
		OutboxId: outbox.Id, ExpectedVersion: claimed.Version, LeaseTokenHMAC: claimed.LeaseTokenHMAC, AttemptCount: 1,
		Result: ManagedProviderFileDeletionResultDeleted, EvidenceDigest: prematureCompletionEvent.EvidenceDigest,
		Event: prematureCompletionEvent,
	})
	assert.ErrorIs(t, err, ErrManagedProviderFileLifecycleStateConflict)

	dispatchEvent := managedProviderFileEvent(5, ManagedProviderFileLifecycleEventDeletionDispatched,
		ManagedProviderFileLifecycleStateDeletionPending, ManagedProviderFileLifecycleStateDeletionPending, 1, "", "f")
	dispatchEvent.LifecycleId = created.Id
	dispatchEvent.PreviousEventHMAC = claimEvent.EventHMAC
	_, err = MarkManagedProviderFileDeletionDispatched(context.Background(), ManagedProviderFileDeletionDispatch{
		OutboxID: claimed.Id, LifecycleID: created.Id, ExpectedVersion: claimed.Version + 1,
		LeaseTokenHMAC: claimed.LeaseTokenHMAC, AttemptCount: claimed.AttemptCount,
		DispatchedAt: time.Now().UTC(), Event: dispatchEvent,
	})
	assert.ErrorIs(t, err, ErrManagedProviderFileDeletionLeaseLost)
	dispatched, err := MarkManagedProviderFileDeletionDispatched(context.Background(), ManagedProviderFileDeletionDispatch{
		OutboxID: claimed.Id, LifecycleID: created.Id, ExpectedVersion: claimed.Version,
		LeaseTokenHMAC: claimed.LeaseTokenHMAC, AttemptCount: claimed.AttemptCount,
		DispatchedAt: time.Now().UTC(), Event: dispatchEvent,
	})
	require.NoError(t, err)

	completionEvent := managedProviderFileEvent(6, ManagedProviderFileLifecycleEventDeletionCompleted,
		ManagedProviderFileLifecycleStateDeletionPending, ManagedProviderFileLifecycleStateDeleted, 1,
		ManagedProviderFileDeletionResultDeleted, "9")
	completionEvent.LifecycleId = created.Id
	completionEvent.PreviousEventHMAC = dispatchEvent.EventHMAC
	err = CompleteManagedProviderFileDeletion(context.Background(), ManagedProviderFileDeletionTerminal{
		OutboxId: outbox.Id, ExpectedVersion: dispatched.Version + 1, LeaseTokenHMAC: claimed.LeaseTokenHMAC, AttemptCount: 1,
		Result: ManagedProviderFileDeletionResultDeleted, EvidenceDigest: completionEvent.EvidenceDigest,
		Event: completionEvent,
	})
	assert.ErrorIs(t, err, ErrManagedProviderFileDeletionLeaseLost)
	require.NoError(t, CompleteManagedProviderFileDeletion(context.Background(), ManagedProviderFileDeletionTerminal{
		OutboxId: outbox.Id, ExpectedVersion: dispatched.Version, LeaseTokenHMAC: managedProviderFileDigest("9"), AttemptCount: 1,
		Result: ManagedProviderFileDeletionResultDeleted, EvidenceDigest: completionEvent.EvidenceDigest,
		Event: completionEvent,
	}))

	var deleted ManagedProviderFileLifecycle
	require.NoError(t, DB.First(&deleted, created.Id).Error)
	require.NoError(t, deleted.Validate())
	assert.Equal(t, ManagedProviderFileLifecycleStateDeleted, deleted.State)
	assert.Empty(t, deleted.ProviderPayload)
	require.NotNil(t, deleted.DeletedAt)

	var completed ManagedProviderFileDeletionOutbox
	require.NoError(t, DB.First(&completed, outbox.Id).Error)
	require.NoError(t, completed.Validate())
	assert.Equal(t, ManagedProviderFileDeletionOutboxStateCompleted, completed.State)
	assert.Equal(t, ManagedProviderFileDeletionResultDeleted, completed.TerminalResult)

	var events []ManagedProviderFileLifecycleEvent
	require.NoError(t, DB.Where("lifecycle_id = ?", created.Id).Order("sequence asc").Find(&events).Error)
	require.Len(t, events, 6)
	assert.Equal(t, []int64{1, 2, 3, 4, 5, 6}, []int64{events[0].Sequence, events[1].Sequence, events[2].Sequence, events[3].Sequence, events[4].Sequence, events[5].Sequence})
	for index := 1; index < len(events); index++ {
		assert.Equal(t, events[index-1].EventHMAC, events[index].PreviousEventHMAC)
	}
	assert.Equal(t, events[len(events)-1].EventHMAC, deleted.LastEventHMAC)
}

func TestManagedProviderFileVerificationFailurePreservesEncryptedRecoveryReference(t *testing.T) {
	prepareManagedProviderFileModelTest(t)
	intent := managedProviderFileIntent()
	created, _, err := CreateManagedProviderFileLifecycleIntent(context.Background(), intent,
		managedProviderFileEvent(1, ManagedProviderFileLifecycleEventIntentCreated, "", ManagedProviderFileLifecycleStateIntent, 0, "", "b"))
	require.NoError(t, err)
	dispatchedEvent := managedProviderFileEvent(2, ManagedProviderFileLifecycleEventUploadDispatched,
		ManagedProviderFileLifecycleStateIntent, ManagedProviderFileLifecycleStateUploadDispatched, 0, "", "c")
	dispatchedEvent.LifecycleId = created.Id
	dispatchedEvent.PreviousEventHMAC = created.LastEventHMAC
	require.NoError(t, AdvanceManagedProviderFileUploadState(context.Background(), ManagedProviderFileUploadTransition{
		LifecycleId: created.Id, ExpectedVersion: created.Version, RequestFingerprint: intent.RequestFingerprint,
		ExpectedState: ManagedProviderFileLifecycleStateIntent, NextState: ManagedProviderFileLifecycleStateUploadDispatched, Event: dispatchedEvent,
	}))
	created.Version++
	created.LastEventSequence++
	created.LastEventHMAC = dispatchedEvent.EventHMAC
	verificationEvent := managedProviderFileEvent(3, ManagedProviderFileLifecycleEventVerificationFailed,
		ManagedProviderFileLifecycleStateUploadDispatched, ManagedProviderFileLifecycleStateVerificationFailed, 0, "metadata_unverified", "d")
	verificationEvent.LifecycleId = created.Id
	verificationEvent.PreviousEventHMAC = created.LastEventHMAC
	providerCreatedAt := time.Now().UTC().Add(-time.Minute).Truncate(time.Second)
	require.NoError(t, RecordManagedProviderFileVerificationFailure(context.Background(), ManagedProviderFileVerificationFailure{
		LifecycleId: created.Id, ExpectedVersion: created.Version, RequestFingerprint: intent.RequestFingerprint,
		ProviderLookupHMAC: managedProviderFileDigest("e"), ProviderPayload: []byte("encrypted-reference"), ProviderBytes: 17,
		ProviderCreatedAt: providerCreatedAt, ExpiresAt: providerCreatedAt.Add(time.Hour), ReasonCode: "metadata_unverified",
		DeletionOperationHMAC: managedProviderFileDigest("f"), DeletionNextAttemptAt: time.Now().UTC(), MaxDeletionAttempts: 3,
		Event: verificationEvent,
	}))
	var stored ManagedProviderFileLifecycle
	require.NoError(t, DB.First(&stored, "id = ?", created.Id).Error)
	require.NoError(t, stored.Validate())
	assert.Equal(t, ManagedProviderFileLifecycleStateVerificationFailed, stored.State)
	assert.NotEmpty(t, stored.ProviderPayload)
	assert.Nil(t, stored.MetadataVerifiedAt)
	assert.Nil(t, stored.ActivatedAt)
	var recoveryOutbox ManagedProviderFileDeletionOutbox
	require.NoError(t, DB.First(&recoveryOutbox, "lifecycle_id = ?", stored.Id).Error)
	assert.Equal(t, ManagedProviderFileDeletionOutboxStatePending, recoveryOutbox.State)
}

func TestManagedProviderFileDeletionRejectsStaleLeaseAndBoundsRetries(t *testing.T) {
	prepareManagedProviderFileModelTest(t)
	outbox := ManagedProviderFileDeletionOutbox{
		LifecycleId: 1, OperationHMAC: managedProviderFileDigest("1"),
		State: ManagedProviderFileDeletionOutboxStateRetryWait, Version: 1,
		AttemptCount: 3, MaxAttempts: 3, NextAttemptAt: time.Now().UTC().Add(-time.Minute),
		LastErrorCode: "provider_unavailable", LastAttemptAt: func() *time.Time { value := time.Now().UTC(); return &value }(),
	}
	require.Error(t, outbox.Validate())

	outbox.AttemptCount = 2
	require.NoError(t, outbox.Validate())

	retryEvent := managedProviderFileEvent(4, ManagedProviderFileLifecycleEventDeletionRetryScheduled,
		ManagedProviderFileLifecycleStateDeletionPending, ManagedProviderFileLifecycleStateDeletionPending, 2, "provider_unavailable", "b")
	retryEvent.LifecycleId = 1
	err := RetryManagedProviderFileDeletion(context.Background(), ManagedProviderFileDeletionRetry{
		OutboxId: 999999, ExpectedVersion: 1, LeaseTokenHMAC: managedProviderFileDigest("c"), AttemptCount: 2,
		NextAttemptAt: time.Now().UTC().Add(time.Minute), ErrorCode: "provider_unavailable", Event: retryEvent,
	})
	assert.Error(t, err)
}
