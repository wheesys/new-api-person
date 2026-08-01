package providerfile

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	openai "github.com/QuantumNous/new-api/relay/channel/openai"
	"github.com/QuantumNous/new-api/service/contextconsensus"
	"github.com/QuantumNous/new-api/setting/model_setting"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type deletionWorkerFixture struct {
	runtime     *contextconsensus.ManagedConsensusRuntime
	settings    *model_setting.SmartRoutingSettings
	httpClient  *http.Client
	lifecycle   model.ManagedProviderFileLifecycle
	outbox      model.ManagedProviderFileDeletionOutbox
	deleteCount int
}

func prepareDeletionWorkerFixture(t *testing.T, deleteStatus int, deleteBody string, verificationFailure bool) *deletionWorkerFixture {
	t.Helper()
	runtime := prepareProviderFileLifecycleTest(t)
	require.NoError(t, model.DB.AutoMigrate(&model.Channel{}))
	organization := "org-exclusive"
	project := "proj-exclusive"
	channel := model.Channel{
		Id: 41, Type: constant.ChannelTypeOpenAI, Status: common.ChannelStatusEnabled,
		Key: "sk-provider-secret", Name: "provider-file-cleanup", OpenAIOrganization: &organization, OpenAIProject: &project,
	}
	require.NoError(t, model.DB.Model(&model.Channel{}).Where("id = ?", channel.Id).Updates(&channel).Error)

	storage, contentType := managedProviderFileMultipart(t, "user_data", "facts.txt", []byte("deletion worker content"), "")
	t.Cleanup(func() { _ = storage.Close() })
	body, err := ParseUploadBody(storage, contentType)
	require.NoError(t, err)
	now := time.Now().UTC().Truncate(time.Second)
	providerFileID := "file-deletion-worker"
	metadataBody, err := common.Marshal(map[string]any{
		"id": providerFileID, "object": "file", "bytes": body.SizeBytes, "created_at": now.Add(-time.Minute).Unix(),
		"expires_at": now.Add(time.Hour).Unix(), "filename": body.Filename, "purpose": "user_data",
	})
	require.NoError(t, err)
	verificationFailureBody, err := common.Marshal(map[string]any{
		"id": providerFileID, "object": "file", "bytes": body.SizeBytes, "created_at": now.Add(-time.Minute).Unix(),
		"expires_at": now.Add(time.Hour).Unix(), "filename": "mismatched.txt", "purpose": "user_data",
	})
	require.NoError(t, err)
	fixture := &deletionWorkerFixture{runtime: runtime}
	fixture.httpClient = &http.Client{Transport: providerFileRoundTripFunc(func(request *http.Request) (*http.Response, error) {
		responseBody := metadataBody
		statusCode := http.StatusOK
		if request.Method == http.MethodPost {
			_, copyErr := io.Copy(io.Discard, request.Body)
			require.NoError(t, copyErr)
		}
		if request.Method == http.MethodDelete {
			fixture.deleteCount++
			statusCode = deleteStatus
			responseBody = []byte(deleteBody)
		}
		if request.Method == http.MethodGet && verificationFailure {
			responseBody = verificationFailureBody
		}
		return &http.Response{
			StatusCode: statusCode, Header: http.Header{"Content-Type": []string{"application/json"}},
			Body: io.NopCloser(strings.NewReader(string(responseBody))), Request: request,
		}, nil
	})}
	client, err := openai.NewProviderFileClient(fixture.httpClient, openai.OpenAIProviderFileOrigin, channel.Key, organization, project)
	require.NoError(t, err)
	target := &Target{
		ChannelID: channel.Id, ChannelType: channel.Type, Endpoint: openai.OpenAIProviderFileOrigin,
		Organization: organization, Project: project, credential: channel.Key, client: client,
	}
	settings := &model_setting.SmartRoutingSettings{
		ProviderFileLifecycleEnabled: true, ProviderFileOpenAIChannelID: channel.Id, ProviderFileExpirationSeconds: 3600,
		ProviderFileMetadataVerifyTTLSeconds: 0, ProviderFileDeletionLeadSeconds: 60, ProviderFileDeletionBatchSize: 10,
		ProviderFileDeletionMaxAttempts: 3, ProviderFileDeletionTimeoutSeconds: 5,
		ProviderFileExclusiveProjectAttested: true, ProviderFileSandboxContractVerified: true,
	}
	fixture.settings = settings
	upload, uploadErr := Upload(context.Background(), UploadRequest{
		Owner: NewOwner(7, 9), IdempotencyKey: "deletion-worker-idempotency", Body: body,
		Settings: settings, Runtime: runtime, Target: target,
	})
	if verificationFailure {
		require.ErrorIs(t, uploadErr, ErrLifecycleUnavailable)
	} else {
		require.NoError(t, uploadErr)
		assert.NotEmpty(t, upload.ID)
	}
	require.NoError(t, model.DB.First(&fixture.lifecycle).Error)
	require.NoError(t, model.DB.First(&fixture.outbox, "lifecycle_id = ?", fixture.lifecycle.Id).Error)
	require.NoError(t, model.DB.Model(&model.Channel{}).Where("id = ?", channel.Id).Update("status", common.ChannelStatusManuallyDisabled).Error)
	require.NoError(t, model.DB.Model(&model.ManagedProviderFileDeletionOutbox{}).
		Where("id = ?", fixture.outbox.Id).Update("next_attempt_at", time.Now().UTC().Add(-time.Minute)).Error)
	require.NoError(t, model.DB.First(&fixture.outbox, "id = ?", fixture.outbox.Id).Error)
	return fixture
}

func (fixture *deletionWorkerFixture) options() DeletionWorkerOptions {
	return DeletionWorkerOptions{
		Runtime: fixture.runtime, Settings: fixture.settings, HTTPClient: fixture.httpClient,
		BatchSize: 10, Timeout: 5 * time.Second, Now: time.Now,
	}
}

func TestDeletionWorkerCompletesStrictReceiptAndScrubsPayload(t *testing.T) {
	fixture := prepareDeletionWorkerFixture(t, http.StatusOK, `{"id":"file-deletion-worker","object":"file","deleted":true}`, false)
	summary, err := RunDeletionBatch(context.Background(), fixture.options())
	require.NoError(t, err)
	assert.Equal(t, DeletionSummary{Due: 1, Claimed: 1, Deleted: 1}, summary)
	assert.Equal(t, 1, fixture.deleteCount)

	var lifecycle model.ManagedProviderFileLifecycle
	require.NoError(t, model.DB.First(&lifecycle, "id = ?", fixture.lifecycle.Id).Error)
	assert.Equal(t, model.ManagedProviderFileLifecycleStateDeleted, lifecycle.State)
	assert.Empty(t, lifecycle.ProviderPayload)
	var events []model.ManagedProviderFileLifecycleEvent
	require.NoError(t, model.DB.Where("lifecycle_id = ?", lifecycle.Id).Order("sequence asc").Find(&events).Error)
	assert.Equal(t, model.ManagedProviderFileLifecycleEventDeletionDispatched, events[len(events)-2].EventType)
	assert.Equal(t, model.ManagedProviderFileLifecycleEventDeletionCompleted, events[len(events)-1].EventType)
}

func TestDeletionWorkerDoesNotRedispatchExpiredDispatchedLease(t *testing.T) {
	fixture := prepareDeletionWorkerFixture(t, http.StatusOK, `{"id":"file-deletion-worker","object":"file","deleted":true}`, false)
	now := time.Now().UTC()
	leaseHMAC, err := fixture.runtime.KeyDeriver.DeriveProviderFileDeletionLeaseHMAC(fixture.outbox.Id, "expired-dispatch-nonce")
	require.NoError(t, err)
	claimEvent, err := providerFileEventWithAttempt(fixture.runtime, fixture.lifecycle.UploadIntentHMAC, fixture.lifecycle.Id,
		fixture.lifecycle.LastEventSequence+1, fixture.lifecycle.LastEventHMAC, fixture.lifecycle.State,
		model.ManagedProviderFileLifecycleEventDeletionStarted, fixture.lifecycle.State, model.ManagedProviderFileLifecycleStateDeletionPending,
		1, "", fixture.outbox.OperationHMAC, now)
	require.NoError(t, err)
	claimed, claimedNow, err := model.ClaimManagedProviderFileDeletion(context.Background(), model.ManagedProviderFileDeletionClaim{
		OutboxId: fixture.outbox.Id, ExpectedVersion: fixture.outbox.Version, LeaseTokenHMAC: leaseHMAC,
		LeaseExpiresAt: now.Add(time.Minute), ExpectedState: fixture.outbox.State, ExpectedAttemptCount: 0, Event: claimEvent,
	})
	require.NoError(t, err)
	require.True(t, claimedNow)
	dispatchEvent, err := providerFileEventWithAttempt(fixture.runtime, fixture.lifecycle.UploadIntentHMAC, fixture.lifecycle.Id,
		claimEvent.Sequence+1, claimEvent.EventHMAC, model.ManagedProviderFileLifecycleStateDeletionPending,
		model.ManagedProviderFileLifecycleEventDeletionDispatched, model.ManagedProviderFileLifecycleStateDeletionPending,
		model.ManagedProviderFileLifecycleStateDeletionPending, 1, "", fixture.outbox.OperationHMAC, now.Add(time.Second))
	require.NoError(t, err)
	_, err = model.MarkManagedProviderFileDeletionDispatched(context.Background(), model.ManagedProviderFileDeletionDispatch{
		OutboxID: claimed.Id, LifecycleID: fixture.lifecycle.Id, ExpectedVersion: claimed.Version,
		LeaseTokenHMAC: claimed.LeaseTokenHMAC, AttemptCount: claimed.AttemptCount,
		DispatchedAt: now.Add(time.Second), Event: dispatchEvent,
	})
	require.NoError(t, err)
	require.NoError(t, model.DB.Model(&model.ManagedProviderFileDeletionOutbox{}).
		Where("id = ?", fixture.outbox.Id).Update("lease_expires_at", now.Add(-time.Minute)).Error)

	summary, err := RunDeletionBatch(context.Background(), fixture.options())
	require.NoError(t, err)
	assert.Equal(t, 0, fixture.deleteCount)
	assert.Equal(t, 1, summary.Failed)
	var lifecycle model.ManagedProviderFileLifecycle
	require.NoError(t, model.DB.First(&lifecycle, "id = ?", fixture.lifecycle.Id).Error)
	assert.Equal(t, model.ManagedProviderFileLifecycleStateDeletionFailed, lifecycle.State)
	assert.Equal(t, "delete_outcome_unknown", lifecycle.TerminalReasonCode)
	assert.NotEmpty(t, lifecycle.ProviderPayload)
}

func TestDeletionWorkerReadinessGatePreventsProviderCall(t *testing.T) {
	fixture := prepareDeletionWorkerFixture(t, http.StatusOK, `{"id":"file-deletion-worker","object":"file","deleted":true}`, false)
	options := fixture.options()
	options.Settings.ProviderFileSandboxContractVerified = false
	_, err := RunDeletionBatch(context.Background(), options)
	require.ErrorContains(t, err, "readiness is unavailable")
	assert.Equal(t, 0, fixture.deleteCount)
}

func TestDeletionWorkerContinuesAfterNewUploadsAreDisabled(t *testing.T) {
	fixture := prepareDeletionWorkerFixture(t, http.StatusOK, `{"id":"file-deletion-worker","object":"file","deleted":true}`, false)
	fixture.settings.ProviderFileLifecycleEnabled = false

	summary, err := RunDeletionBatch(context.Background(), fixture.options())
	require.NoError(t, err)
	assert.Equal(t, DeletionSummary{Due: 1, Claimed: 1, Deleted: 1}, summary)
	assert.Equal(t, 1, fixture.deleteCount)
}

func TestDeletionWorkerRetriesExplicitRateLimitWithoutExceedingAttemptBound(t *testing.T) {
	fixture := prepareDeletionWorkerFixture(t, http.StatusTooManyRequests, `{}`, false)
	summary, err := RunDeletionBatch(context.Background(), fixture.options())
	require.NoError(t, err)
	assert.Equal(t, 1, summary.Retried)
	assert.Equal(t, 1, fixture.deleteCount)
	var outbox model.ManagedProviderFileDeletionOutbox
	require.NoError(t, model.DB.First(&outbox, "id = ?", fixture.outbox.Id).Error)
	require.NoError(t, outbox.Validate())
	assert.Equal(t, model.ManagedProviderFileDeletionOutboxStateRetryWait, outbox.State)
	assert.Nil(t, outbox.DispatchedAt)
	assert.Equal(t, 1, outbox.AttemptCount)

	for expectedAttempt := 2; expectedAttempt <= outbox.MaxAttempts; expectedAttempt++ {
		require.NoError(t, model.DB.Model(&model.ManagedProviderFileDeletionOutbox{}).
			Where("id = ?", outbox.Id).Update("next_attempt_at", time.Now().UTC().Add(-time.Minute)).Error)
		summary, err = RunDeletionBatch(context.Background(), fixture.options())
		require.NoError(t, err)
		require.NoError(t, model.DB.First(&outbox, "id = ?", outbox.Id).Error)
		assert.Equal(t, expectedAttempt, outbox.AttemptCount)
		if expectedAttempt < outbox.MaxAttempts {
			assert.Equal(t, 1, summary.Retried)
			assert.Equal(t, model.ManagedProviderFileDeletionOutboxStateRetryWait, outbox.State)
		} else {
			assert.Equal(t, 1, summary.Failed)
			assert.Equal(t, model.ManagedProviderFileDeletionOutboxStateTerminalFailed, outbox.State)
		}
	}
	assert.Equal(t, outbox.MaxAttempts, fixture.deleteCount)
	summary, err = RunDeletionBatch(context.Background(), fixture.options())
	require.NoError(t, err)
	assert.Zero(t, summary.Due)
	assert.Equal(t, outbox.MaxAttempts, fixture.deleteCount)
}

func TestDeletionWorkerTreatsMalformedReceiptAsUnknownWithoutRetry(t *testing.T) {
	fixture := prepareDeletionWorkerFixture(t, http.StatusOK, `{"id":"file-deletion-worker","object":"file","deleted":false}`, false)
	summary, err := RunDeletionBatch(context.Background(), fixture.options())
	require.NoError(t, err)
	assert.Equal(t, 1, summary.Failed)
	assert.Equal(t, 1, fixture.deleteCount)

	summary, err = RunDeletionBatch(context.Background(), fixture.options())
	require.NoError(t, err)
	assert.Zero(t, summary.Due)
	assert.Equal(t, 1, fixture.deleteCount)
	var lifecycle model.ManagedProviderFileLifecycle
	require.NoError(t, model.DB.First(&lifecycle, "id = ?", fixture.lifecycle.Id).Error)
	assert.Equal(t, "delete_outcome_unknown", lifecycle.TerminalReasonCode)
	assert.NotEmpty(t, lifecycle.ProviderPayload)
}

func TestDeletionWorkerDoesNotTreatNotFoundAsDeleted(t *testing.T) {
	fixture := prepareDeletionWorkerFixture(t, http.StatusNotFound, `{}`, false)
	summary, err := RunDeletionBatch(context.Background(), fixture.options())
	require.NoError(t, err)
	assert.Equal(t, 1, summary.Failed)
	assert.Equal(t, 1, fixture.deleteCount)

	var lifecycle model.ManagedProviderFileLifecycle
	require.NoError(t, model.DB.First(&lifecycle, "id = ?", fixture.lifecycle.Id).Error)
	assert.Equal(t, model.ManagedProviderFileLifecycleStateDeletionFailed, lifecycle.State)
	assert.Equal(t, "provider_not_found_unverified", lifecycle.TerminalReasonCode)
	assert.NotEmpty(t, lifecycle.ProviderPayload)
	summary, err = RunDeletionBatch(context.Background(), fixture.options())
	require.NoError(t, err)
	assert.Zero(t, summary.Due)
	assert.Equal(t, 1, fixture.deleteCount)
}

func TestDeletionWorkerIsolatesPoisonedOutboxWithinBatch(t *testing.T) {
	fixture := prepareDeletionWorkerFixture(t, http.StatusOK, `{"id":"file-deletion-worker","object":"file","deleted":true}`, false)
	poisoned := model.ManagedProviderFileDeletionOutbox{
		LifecycleId: 999999, OperationHMAC: strings.Repeat("f", 64), State: model.ManagedProviderFileDeletionOutboxStatePending,
		Version: 1, MaxAttempts: 3, NextAttemptAt: time.Now().UTC().Add(-2 * time.Minute),
	}
	require.NoError(t, poisoned.Validate())
	require.NoError(t, model.DB.Create(&poisoned).Error)

	options := fixture.options()
	options.BatchSize = 1
	summary, err := RunDeletionBatch(context.Background(), options)
	require.NoError(t, err)
	assert.Equal(t, 1, summary.Due)
	assert.Equal(t, 1, summary.Failed)
	assert.Equal(t, 0, fixture.deleteCount)
	require.NoError(t, model.DB.First(&poisoned, "id = ?", poisoned.Id).Error)
	require.NoError(t, poisoned.Validate())
	assert.Equal(t, model.ManagedProviderFileDeletionOutboxStateTerminalFailed, poisoned.State)
	assert.Equal(t, "lifecycle_missing", poisoned.LastErrorCode)

	summary, err = RunDeletionBatch(context.Background(), options)
	require.NoError(t, err)
	assert.Equal(t, 1, summary.Due)
	assert.Equal(t, 1, summary.Deleted)
	assert.Equal(t, 1, fixture.deleteCount)
}

func TestDeletionWorkerCleansVerificationFailureWithoutActivatingIt(t *testing.T) {
	fixture := prepareDeletionWorkerFixture(t, http.StatusOK, `{"id":"file-deletion-worker","object":"file","deleted":true}`, true)
	assert.Equal(t, model.ManagedProviderFileLifecycleStateVerificationFailed, fixture.lifecycle.State)
	assert.Nil(t, fixture.lifecycle.MetadataVerifiedAt)
	assert.Nil(t, fixture.lifecycle.ActivatedAt)

	summary, err := RunDeletionBatch(context.Background(), fixture.options())
	require.NoError(t, err)
	assert.Equal(t, 1, summary.Deleted)
	assert.Equal(t, 1, fixture.deleteCount)
	var lifecycle model.ManagedProviderFileLifecycle
	require.NoError(t, model.DB.First(&lifecycle, "id = ?", fixture.lifecycle.Id).Error)
	require.NoError(t, lifecycle.Validate())
	assert.Equal(t, model.ManagedProviderFileLifecycleStateDeleted, lifecycle.State)
	assert.NotNil(t, lifecycle.VerificationFailedAt)
	assert.Empty(t, lifecycle.ProviderPayload)
}
