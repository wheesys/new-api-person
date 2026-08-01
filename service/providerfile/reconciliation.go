package providerfile

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/QuantumNous/new-api/model"
	openai "github.com/QuantumNous/new-api/relay/channel/openai"
	"github.com/QuantumNous/new-api/service/contextconsensus"
	"github.com/QuantumNous/new-api/setting/model_setting"
	"gorm.io/gorm"
)

const (
	providerFileReconciliationMaximumPages      = 100
	providerFileReconciliationMaximumObjects    = 10000
	providerFileReconciliationMaximumBytes      = 8 * 1024 * 1024
	providerFileReconciliationMaximumDuration   = 2 * time.Minute
	providerFileReconciliationVisibilityGuard   = 5 * time.Minute
	providerFileReconciliationMinimumQuarantine = 24 * time.Hour
)

var ErrReconciliationUnavailable = errors.New("managed provider file reconciliation is unavailable")

type ReconciliationOptions struct {
	Runtime    *contextconsensus.ManagedConsensusRuntime
	Settings   *model_setting.SmartRoutingSettings
	HTTPClient *http.Client
	Now        func() time.Time
}

type ReconciliationSummary struct {
	ScanID         int64  `json:"scan_id"`
	State          string `json:"state"`
	PageCount      int    `json:"page_count"`
	ObjectCount    int    `json:"object_count"`
	CandidateCount int    `json:"candidate_count"`
	FailureCode    string `json:"failure_code,omitempty"`
}

func RunReconciliationScan(ctx context.Context, options ReconciliationOptions) (ReconciliationSummary, error) {
	summary := ReconciliationSummary{}
	if ctx == nil || options.Runtime == nil || options.Runtime.KeyDeriver == nil || options.Runtime.Cipher == nil || options.Settings == nil ||
		!options.Settings.ProviderFileReconciliationEnabled {
		return summary, ErrReconciliationUnavailable
	}
	if options.Now == nil {
		options.Now = time.Now
	}
	startedAt := options.Now().UTC().Truncate(time.Second)
	target, err := loadMaintenanceTarget(ctx, options.Settings, options.Runtime, options.HTTPClient, startedAt)
	if err != nil {
		return summary, ErrReconciliationUnavailable
	}
	readinessEvidence, err := GetVerifiedReadinessEvidence(ctx, options.Settings, options.Runtime, target, startedAt)
	if err != nil {
		return summary, ErrReconciliationUnavailable
	}
	targetFingerprint, err := options.Runtime.KeyDeriver.DeriveProviderFileTargetFingerprint(target.identity())
	if err != nil {
		return summary, ErrReconciliationUnavailable
	}
	scopeFingerprint, err := options.Runtime.KeyDeriver.DeriveProviderFileScopeFingerprint(target.Organization, target.Project)
	if err != nil {
		return summary, ErrReconciliationUnavailable
	}
	scan := &model.ManagedProviderFileReconciliationScan{
		TargetFingerprint: targetFingerprint, ScopeFingerprint: scopeFingerprint, KeyVersion: options.Runtime.KeyDeriver.KeyVersion(),
		State: model.ManagedProviderFileReconciliationScanStateScanning, Version: 1, StartedAt: startedAt,
		CutoffAt: startedAt.Add(-providerFileReconciliationVisibilityGuard),
	}
	if err := model.CreateManagedProviderFileReconciliationScan(ctx, scan); err != nil {
		return summary, ErrReconciliationUnavailable
	}
	summary.ScanID = scan.Id

	scanContext, cancel := context.WithTimeout(ctx, providerFileReconciliationMaximumDuration)
	defer cancel()
	seenProviderFileIDs := make(map[string]struct{})
	seenCursors := make(map[string]struct{})
	observations := make([]model.ManagedProviderFileReconciliationObservation, 0)
	after := ""
	lastCursorHMAC := ""
	totalResponseBytes := 0
	for {
		if err := scanContext.Err(); err != nil {
			return finishIncompleteReconciliationScan(ctx, scan, &summary, "scan_deadline", lastCursorHMAC, observations)
		}
		page, listErr := target.client.List(scanContext, after)
		if listErr != nil {
			return finishIncompleteReconciliationScan(ctx, scan, &summary, "provider_list_failed", lastCursorHMAC, observations)
		}
		summary.PageCount++
		totalResponseBytes += page.ResponseBytes
		if totalResponseBytes > providerFileReconciliationMaximumBytes {
			return finishIncompleteReconciliationScan(ctx, scan, &summary, "byte_cap_reached", lastCursorHMAC, observations)
		}
		for _, metadata := range page.Files {
			if summary.ObjectCount >= providerFileReconciliationMaximumObjects {
				return finishIncompleteReconciliationScan(ctx, scan, &summary, "object_cap_reached", lastCursorHMAC, observations)
			}
			summary.ObjectCount++
			if _, duplicate := seenProviderFileIDs[metadata.ProviderFileID]; duplicate {
				return finishIncompleteReconciliationScan(ctx, scan, &summary, "duplicate_provider_id", lastCursorHMAC, observations)
			}
			seenProviderFileIDs[metadata.ProviderFileID] = struct{}{}
			if time.Unix(metadata.CreatedAtUnix, 0).UTC().After(scan.CutoffAt) {
				continue
			}
			observation, observationErr := buildReconciliationObservation(scanContext, options.Runtime, targetFingerprint, readinessEvidence, metadata, startedAt)
			if observationErr != nil {
				return finishIncompleteReconciliationScan(ctx, scan, &summary, "candidate_evidence_failed", lastCursorHMAC, observations)
			}
			observations = append(observations, observation)
		}
		if !page.HasMore {
			break
		}
		if len(page.Files) == 0 || page.LastID == "" || page.LastID == after {
			return finishIncompleteReconciliationScan(ctx, scan, &summary, "cursor_no_progress", lastCursorHMAC, observations)
		}
		if summary.PageCount >= providerFileReconciliationMaximumPages {
			return finishIncompleteReconciliationScan(ctx, scan, &summary, "page_cap_reached", lastCursorHMAC, observations)
		}
		if _, duplicate := seenCursors[page.LastID]; duplicate {
			return finishIncompleteReconciliationScan(ctx, scan, &summary, "cursor_loop", lastCursorHMAC, observations)
		}
		seenCursors[page.LastID] = struct{}{}
		lastCursorHMAC, err = options.Runtime.KeyDeriver.DeriveProviderFileReconciliationCursorHMAC(targetFingerprint, page.LastID)
		if err != nil {
			return finishIncompleteReconciliationScan(ctx, scan, &summary, "cursor_evidence_failed", "", observations)
		}
		after = page.LastID
	}

	currentTarget, err := loadMaintenanceTarget(ctx, options.Settings, options.Runtime, options.HTTPClient, options.Now().UTC())
	if err != nil {
		return finishIncompleteReconciliationScan(ctx, scan, &summary, "target_changed", lastCursorHMAC, observations)
	}
	currentTargetFingerprint, err := options.Runtime.KeyDeriver.DeriveProviderFileTargetFingerprint(currentTarget.identity())
	if err != nil || currentTargetFingerprint != targetFingerprint {
		return finishIncompleteReconciliationScan(ctx, scan, &summary, "target_changed", lastCursorHMAC, observations)
	}
	completedAt := options.Now().UTC().Truncate(time.Second)
	if err := model.FinishManagedProviderFileReconciliationScan(ctx, scan, model.ManagedProviderFileReconciliationScanStateComplete, "", lastCursorHMAC,
		summary.PageCount, summary.ObjectCount, observations, completedAt); err != nil {
		return summary, ErrReconciliationUnavailable
	}
	summary.State = model.ManagedProviderFileReconciliationScanStateComplete
	summary.CandidateCount = len(observations)
	return summary, nil
}

func buildReconciliationObservation(ctx context.Context, runtime *contextconsensus.ManagedConsensusRuntime, targetFingerprint string,
	readinessEvidence *model.ManagedProviderFileReadinessEvidence, metadata openai.ProviderFileMetadata, observedAt time.Time) (model.ManagedProviderFileReconciliationObservation, error) {
	if readinessEvidence == nil {
		return model.ManagedProviderFileReconciliationObservation{}, ErrReconciliationUnavailable
	}
	targetLookupHMAC, err := runtime.KeyDeriver.DeriveProviderFileTargetReferenceHMAC(targetFingerprint, metadata.ProviderFileID)
	if err != nil {
		return model.ManagedProviderFileReconciliationObservation{}, err
	}
	metadataHMAC, err := runtime.KeyDeriver.DeriveProviderFileReconciliationMetadataHMAC(targetLookupHMAC, metadata.Filename, metadata.Bytes, metadata.CreatedAtUnix, metadata.ExpiresAtUnix)
	if err != nil {
		return model.ManagedProviderFileReconciliationObservation{}, err
	}
	payload, payloadKeyVersion, err := runtime.EncryptProviderFileReconciliationReference(ctx, targetLookupHMAC, contextconsensus.ManagedProviderFileReconciliationPayload{
		ProviderFileID: metadata.ProviderFileID, Filename: metadata.Filename,
	})
	if err != nil {
		return model.ManagedProviderFileReconciliationObservation{}, err
	}
	providerCreatedAt := time.Unix(metadata.CreatedAtUnix, 0).UTC()
	var expiresAt *time.Time
	if metadata.ExpiresAtUnix > 0 {
		expiry := time.Unix(metadata.ExpiresAtUnix, 0).UTC()
		expiresAt = &expiry
	}
	state, quarantineUntil, err := reconciliationCandidateState(ctx, targetLookupHMAC, readinessEvidence, providerCreatedAt, expiresAt, observedAt)
	if err != nil {
		return model.ManagedProviderFileReconciliationObservation{}, err
	}
	return model.ManagedProviderFileReconciliationObservation{
		TargetFingerprint: targetFingerprint, TargetProviderLookupHMAC: targetLookupHMAC, MetadataHMAC: metadataHMAC,
		ProviderPayload: payload, PayloadKeyVersion: payloadKeyVersion, State: state, ProviderBytes: metadata.Bytes,
		ProviderCreatedAt: providerCreatedAt, ExpiresAt: expiresAt, QuarantineUntil: quarantineUntil,
	}, nil
}

func finishIncompleteReconciliationScan(ctx context.Context, scan *model.ManagedProviderFileReconciliationScan, summary *ReconciliationSummary, failureCode, lastCursorHMAC string,
	observations []model.ManagedProviderFileReconciliationObservation) (ReconciliationSummary, error) {
	if summary == nil {
		return ReconciliationSummary{}, ErrReconciliationUnavailable
	}
	completedAt := time.Now().UTC().Truncate(time.Second)
	finishContext := context.WithoutCancel(ctx)
	finishErr := model.FinishManagedProviderFileReconciliationScan(finishContext, scan, model.ManagedProviderFileReconciliationScanStateIncomplete, failureCode,
		lastCursorHMAC, summary.PageCount, summary.ObjectCount, nil, completedAt)
	summary.State = model.ManagedProviderFileReconciliationScanStateIncomplete
	summary.FailureCode = failureCode
	summary.CandidateCount = 0
	if finishErr != nil {
		return *summary, ErrReconciliationUnavailable
	}
	return *summary, ErrReconciliationUnavailable
}

func reconciliationCandidateState(ctx context.Context, targetLookupHMAC string, readinessEvidence *model.ManagedProviderFileReadinessEvidence,
	providerCreatedAt time.Time, expiresAt *time.Time, observedAt time.Time) (string, *time.Time, error) {
	_, err := model.GetManagedProviderFileLifecycleByTargetLookup(ctx, targetLookupHMAC)
	if err == nil {
		return model.ManagedProviderFileReconciliationCandidateManaged, nil, nil
	}
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		return "", nil, err
	}
	if providerCreatedAt.Before(readinessEvidence.AttestedAt) {
		return model.ManagedProviderFileReconciliationCandidateExcludedPreAttestation, nil, nil
	}
	if expiresAt == nil {
		return model.ManagedProviderFileReconciliationCandidateAmbiguous, nil, nil
	}
	quarantineUntil := observedAt.Add(providerFileReconciliationMinimumQuarantine)
	if !expiresAt.After(quarantineUntil) {
		return model.ManagedProviderFileReconciliationCandidateAwaitExpiry, nil, nil
	}
	return model.ManagedProviderFileReconciliationCandidateQuarantined, &quarantineUntil, nil
}
