package providerfile

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	openai "github.com/QuantumNous/new-api/relay/channel/openai"
	"github.com/QuantumNous/new-api/service/contextconsensus"
	"github.com/QuantumNous/new-api/setting/model_setting"
	"gorm.io/gorm"
)

const (
	minimumDeletionWorkerTimeout = time.Second
	maximumDeletionWorkerTimeout = 120 * time.Second
	maximumDeletionRetryDelay    = 15 * time.Minute
)

type DeletionWorkerOptions struct {
	Runtime    *contextconsensus.ManagedConsensusRuntime
	Settings   *model_setting.SmartRoutingSettings
	HTTPClient *http.Client
	BatchSize  int
	Timeout    time.Duration
	Now        func() time.Time
	Alert      func(resultCode string, outboxID, lifecycleID int64)
}

type DeletionSummary struct {
	Due     int `json:"due"`
	Claimed int `json:"claimed"`
	Deleted int `json:"deleted"`
	Retried int `json:"retried"`
	Failed  int `json:"failed"`
	Skipped int `json:"skipped"`
	Errors  int `json:"errors"`
}

func RunDeletionBatch(ctx context.Context, options DeletionWorkerOptions) (DeletionSummary, error) {
	summary := DeletionSummary{}
	if ctx == nil || options.Runtime == nil || options.Runtime.KeyDeriver == nil || options.BatchSize <= 0 || options.BatchSize > 100 ||
		options.Timeout < minimumDeletionWorkerTimeout || options.Timeout > maximumDeletionWorkerTimeout {
		return summary, fmt.Errorf("managed provider file deletion worker configuration is invalid")
	}
	if options.Now == nil {
		options.Now = time.Now
	}
	now := options.Now().UTC()
	if err := VerifyDeletionReadiness(ctx, options.Settings, options.Runtime, options.HTTPClient, now); err != nil {
		return summary, fmt.Errorf("managed provider file deletion readiness is unavailable")
	}
	outboxes, err := model.ListDueManagedProviderFileDeletions(ctx, now, options.BatchSize)
	if err != nil {
		return summary, err
	}
	summary.Due = len(outboxes)
	for index := range outboxes {
		if err := ctx.Err(); err != nil {
			return summary, err
		}
		if err := processDeletionOutbox(ctx, options, &summary, &outboxes[index]); err != nil {
			summary.Errors++
			if options.Alert != nil {
				options.Alert("worker_error", outboxes[index].Id, outboxes[index].LifecycleId)
			}
		}
	}
	if summary.Errors > 0 {
		return summary, fmt.Errorf("managed provider file deletion batch contains %d isolated errors", summary.Errors)
	}
	return summary, nil
}

func processDeletionOutbox(ctx context.Context, options DeletionWorkerOptions, summary *DeletionSummary, candidate *model.ManagedProviderFileDeletionOutbox) error {
	lifecycle, err := model.GetManagedProviderFileDeletionLifecycle(ctx, candidate.LifecycleId)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			quarantined, quarantineErr := model.QuarantineOrphanedManagedProviderFileDeletion(ctx, candidate.Id, candidate.Version)
			if quarantineErr != nil {
				return quarantineErr
			}
			if !quarantined {
				summary.Skipped++
				return nil
			}
			summary.Failed++
			if options.Alert != nil {
				options.Alert("lifecycle_missing", candidate.Id, candidate.LifecycleId)
			}
			return nil
		}
		return err
	}
	nonce, err := common.GenerateRandomCharsKey(32)
	if err != nil {
		return err
	}
	leaseHMAC, err := options.Runtime.KeyDeriver.DeriveProviderFileDeletionLeaseHMAC(candidate.Id, nonce)
	if err != nil {
		return err
	}
	now := options.Now().UTC()
	nextAttempt := candidate.AttemptCount + 1
	eventType := model.ManagedProviderFileLifecycleEventDeletionAttemptStarted
	possiblyDispatched := candidate.DispatchedAt != nil
	if candidate.State == model.ManagedProviderFileDeletionOutboxStateInProgress {
		nextAttempt = candidate.AttemptCount
		eventType = model.ManagedProviderFileLifecycleEventDeletionRecoveryStarted
	} else if lifecycle.State == model.ManagedProviderFileLifecycleStateActive || lifecycle.State == model.ManagedProviderFileLifecycleStateVerificationFailed {
		eventType = model.ManagedProviderFileLifecycleEventDeletionStarted
	}
	claimEvent, err := providerFileEventWithAttempt(options.Runtime, lifecycle.UploadIntentHMAC, lifecycle.Id,
		lifecycle.LastEventSequence+1, lifecycle.LastEventHMAC, lifecycle.State, eventType, lifecycle.State,
		model.ManagedProviderFileLifecycleStateDeletionPending, nextAttempt, "", candidate.OperationHMAC, now)
	if err != nil {
		return err
	}
	claimed, claimedNow, err := model.ClaimManagedProviderFileDeletion(ctx, model.ManagedProviderFileDeletionClaim{
		OutboxId: candidate.Id, ExpectedVersion: candidate.Version, LeaseTokenHMAC: leaseHMAC,
		LeaseExpiresAt: now.Add(options.Timeout + 30*time.Second), ExpectedState: candidate.State,
		ExpectedAttemptCount: candidate.AttemptCount, Event: claimEvent,
	})
	if err != nil {
		return err
	}
	if !claimedNow {
		summary.Skipped++
		return nil
	}
	summary.Claimed++
	lifecycle.State = model.ManagedProviderFileLifecycleStateDeletionPending
	lifecycle.Version++
	lifecycle.LastEventSequence = claimEvent.Sequence
	lifecycle.LastEventHMAC = claimEvent.EventHMAC
	if possiblyDispatched {
		return finishDeletionFailure(ctx, options, summary, lifecycle, claimed, "delete_outcome_unknown")
	}

	target, err := loadDeletionTarget(lifecycle.ChannelId, options.HTTPClient)
	if err != nil {
		return scheduleDeletionRetry(ctx, options, summary, lifecycle, claimed, "target_unavailable")
	}
	targetBindings, err := options.Runtime.ProviderFileTargetBindings(target.identity(), target.credential)
	if err != nil || !matchesLifecycleTarget(lifecycle, target, targetBindings) {
		return finishDeletionFailure(ctx, options, summary, lifecycle, claimed, "target_binding_changed")
	}
	repositoryKey, err := contextconsensus.ManagedProviderFileRepositoryKey(lifecycle.OwnerHMAC, lifecycle.UploadIntentHMAC)
	if err != nil {
		return scheduleDeletionRetry(ctx, options, summary, lifecycle, claimed, "reference_unavailable")
	}
	payload, err := options.Runtime.DecryptProviderFileReference(ctx, repositoryKey, lifecycle.ProviderPayload)
	if err != nil {
		return scheduleDeletionRetry(ctx, options, summary, lifecycle, claimed, "reference_unavailable")
	}
	dispatchEvent, err := providerFileEventWithAttempt(options.Runtime, lifecycle.UploadIntentHMAC, lifecycle.Id,
		lifecycle.LastEventSequence+1, lifecycle.LastEventHMAC, lifecycle.State,
		model.ManagedProviderFileLifecycleEventDeletionDispatched, lifecycle.State, lifecycle.State,
		claimed.AttemptCount, "", claimed.OperationHMAC, options.Now().UTC())
	if err != nil {
		return err
	}
	dispatched, err := model.MarkManagedProviderFileDeletionDispatched(ctx, model.ManagedProviderFileDeletionDispatch{
		OutboxID: claimed.Id, LifecycleID: lifecycle.Id, ExpectedVersion: claimed.Version,
		LeaseTokenHMAC: claimed.LeaseTokenHMAC, AttemptCount: claimed.AttemptCount,
		DispatchedAt: options.Now().UTC(), Event: dispatchEvent,
	})
	if err != nil {
		return err
	}
	lifecycle.Version++
	lifecycle.LastEventSequence = dispatchEvent.Sequence
	lifecycle.LastEventHMAC = dispatchEvent.EventHMAC
	deleteContext, cancel := context.WithTimeout(ctx, options.Timeout)
	deleteErr := target.client.Delete(deleteContext, payload.ProviderFileID)
	cancel()
	if deleteErr != nil {
		resultCode := "provider_delete_failed"
		var providerError openai.ProviderFileDeleteError
		if errors.As(deleteErr, &providerError) {
			switch providerError.Code {
			case openai.ProviderFileDeleteFailureRateLimited:
				return scheduleDeletionRetry(ctx, options, summary, lifecycle, dispatched, "provider_rate_limited")
			case openai.ProviderFileDeleteFailureTimeout, openai.ProviderFileDeleteFailureTransport,
				openai.ProviderFileDeleteFailureResponseTooLarge, openai.ProviderFileDeleteFailureInvalidResponse,
				openai.ProviderFileDeleteFailureUpstreamServer:
				resultCode = "delete_outcome_unknown"
			case openai.ProviderFileDeleteFailureNotFound:
				resultCode = "provider_not_found_unverified"
			default:
				resultCode = "provider_" + string(providerError.Code)
			}
		}
		return finishDeletionFailure(ctx, options, summary, lifecycle, dispatched, resultCode)
	}
	return finishDeletionSuccess(ctx, options, summary, lifecycle, dispatched)
}

func scheduleDeletionRetry(ctx context.Context, options DeletionWorkerOptions, summary *DeletionSummary, lifecycle *model.ManagedProviderFileLifecycle, outbox *model.ManagedProviderFileDeletionOutbox, resultCode string) error {
	if outbox.AttemptCount >= outbox.MaxAttempts {
		return finishDeletionFailure(ctx, options, summary, lifecycle, outbox, resultCode)
	}
	now := options.Now().UTC()
	retryEvent, err := providerFileEventWithAttempt(options.Runtime, lifecycle.UploadIntentHMAC, lifecycle.Id,
		lifecycle.LastEventSequence+1, lifecycle.LastEventHMAC, lifecycle.State,
		model.ManagedProviderFileLifecycleEventDeletionRetryScheduled, lifecycle.State, lifecycle.State,
		outbox.AttemptCount, resultCode, outbox.OperationHMAC, now)
	if err != nil {
		return err
	}
	delay := 15 * time.Second
	for attempt := 1; attempt < outbox.AttemptCount && delay < maximumDeletionRetryDelay; attempt++ {
		delay *= 2
	}
	if delay > maximumDeletionRetryDelay {
		delay = maximumDeletionRetryDelay
	}
	if err := model.RetryManagedProviderFileDeletion(ctx, model.ManagedProviderFileDeletionRetry{
		OutboxId: outbox.Id, ExpectedVersion: outbox.Version, LeaseTokenHMAC: outbox.LeaseTokenHMAC,
		AttemptCount: outbox.AttemptCount, NextAttemptAt: now.Add(delay), ErrorCode: resultCode, Event: retryEvent,
	}); err != nil {
		return err
	}
	summary.Retried++
	return nil
}

func finishDeletionSuccess(ctx context.Context, options DeletionWorkerOptions, summary *DeletionSummary, lifecycle *model.ManagedProviderFileLifecycle, outbox *model.ManagedProviderFileDeletionOutbox) error {
	const resultCode = model.ManagedProviderFileDeletionResultDeleted
	evidenceHMAC, err := options.Runtime.KeyDeriver.DeriveProviderFileDeletionEvidenceHMAC(outbox.OperationHMAC, outbox.AttemptCount, resultCode)
	if err != nil {
		return err
	}
	event, err := providerFileEventWithAttempt(options.Runtime, lifecycle.UploadIntentHMAC, lifecycle.Id,
		lifecycle.LastEventSequence+1, lifecycle.LastEventHMAC, lifecycle.State,
		model.ManagedProviderFileLifecycleEventDeletionCompleted, lifecycle.State, model.ManagedProviderFileLifecycleStateDeleted,
		outbox.AttemptCount, resultCode, evidenceHMAC, options.Now().UTC())
	if err != nil {
		return err
	}
	if err := model.CompleteManagedProviderFileDeletion(ctx, model.ManagedProviderFileDeletionTerminal{
		OutboxId: outbox.Id, ExpectedVersion: outbox.Version, LeaseTokenHMAC: outbox.LeaseTokenHMAC,
		AttemptCount: outbox.AttemptCount, Result: resultCode, EvidenceDigest: evidenceHMAC, Event: event,
	}); err != nil {
		return err
	}
	summary.Deleted++
	return nil
}

func finishDeletionFailure(ctx context.Context, options DeletionWorkerOptions, summary *DeletionSummary, lifecycle *model.ManagedProviderFileLifecycle, outbox *model.ManagedProviderFileDeletionOutbox, resultCode string) error {
	evidenceHMAC, err := options.Runtime.KeyDeriver.DeriveProviderFileDeletionEvidenceHMAC(outbox.OperationHMAC, outbox.AttemptCount, resultCode)
	if err != nil {
		return err
	}
	event, err := providerFileEventWithAttempt(options.Runtime, lifecycle.UploadIntentHMAC, lifecycle.Id,
		lifecycle.LastEventSequence+1, lifecycle.LastEventHMAC, lifecycle.State,
		model.ManagedProviderFileLifecycleEventDeletionTerminalFailed, lifecycle.State, model.ManagedProviderFileLifecycleStateDeletionFailed,
		outbox.AttemptCount, resultCode, evidenceHMAC, options.Now().UTC())
	if err != nil {
		return err
	}
	if err := model.FailManagedProviderFileDeletion(ctx, model.ManagedProviderFileDeletionTerminal{
		OutboxId: outbox.Id, ExpectedVersion: outbox.Version, LeaseTokenHMAC: outbox.LeaseTokenHMAC,
		AttemptCount: outbox.AttemptCount, Result: model.ManagedProviderFileDeletionResultFailed,
		ErrorCode: resultCode, EvidenceDigest: evidenceHMAC, Event: event,
	}); err != nil {
		return err
	}
	summary.Failed++
	if options.Alert != nil {
		options.Alert(resultCode, outbox.Id, lifecycle.Id)
	}
	return nil
}
