package model

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const (
	ManagedProviderFileReconciliationScanStateScanning   = "scanning"
	ManagedProviderFileReconciliationScanStateComplete   = "complete"
	ManagedProviderFileReconciliationScanStateIncomplete = "incomplete"
	ManagedProviderFileReconciliationScanStateFailed     = "failed"

	ManagedProviderFileReconciliationCandidateManaged                = "managed"
	ManagedProviderFileReconciliationCandidateExcludedPreAttestation = "excluded_pre_attestation"
	ManagedProviderFileReconciliationCandidateQuarantined            = "quarantined"
	ManagedProviderFileReconciliationCandidateAwaitExpiry            = "await_expiry"
	ManagedProviderFileReconciliationCandidateAmbiguous              = "ambiguous"
)

var ErrManagedProviderFileReconciliationConflict = errors.New("managed provider file reconciliation conflict")

type ManagedProviderFileReconciliationScan struct {
	Id                int64      `json:"id" gorm:"primaryKey;autoIncrement;comment:Provider file reconciliation scan numeric identifier"`
	TargetFingerprint string     `json:"-" gorm:"type:varchar(64);not null;index:idx_provider_file_scan_target_started,priority:1;comment:HMAC binding the exact scanned provider target"`
	ScopeFingerprint  string     `json:"-" gorm:"type:varchar(64);not null;comment:HMAC binding the exact scanned provider organization and project"`
	KeyVersion        string     `json:"-" gorm:"type:varchar(64);not null;comment:Managed consensus key version used for scan identities"`
	State             string     `json:"state" gorm:"type:varchar(24);not null;index;comment:Durable bounded scan state"`
	Version           int64      `json:"version" gorm:"not null;comment:Monotonic reconciliation scan compare-and-swap version"`
	StartedAt         time.Time  `json:"started_at" gorm:"not null;index:idx_provider_file_scan_target_started,priority:2;comment:Timestamp captured before the first provider list request"`
	CutoffAt          time.Time  `json:"cutoff_at" gorm:"not null;comment:Files created after this safety cutoff are excluded from this scan"`
	CompletedAt       *time.Time `json:"completed_at,omitempty" gorm:"comment:Timestamp when the bounded scan reached a terminal state"`
	PageCount         int        `json:"page_count" gorm:"not null;comment:Number of provider list pages accepted by this scan"`
	ObjectCount       int        `json:"object_count" gorm:"not null;comment:Number of unique provider file objects accepted by this scan"`
	CandidateCount    int        `json:"candidate_count" gorm:"not null;comment:Number of candidate records advanced by a complete scan"`
	LastCursorHMAC    string     `json:"-" gorm:"type:varchar(64);not null;comment:HMAC of the last raw provider pagination cursor or empty"`
	FailureCode       string     `json:"failure_code,omitempty" gorm:"type:varchar(64);not null;comment:Bounded non-sensitive terminal scan failure code"`
	CreatedAt         time.Time  `json:"created_at" gorm:"not null;comment:Timestamp when the reconciliation scan record was created"`
	UpdatedAt         time.Time  `json:"updated_at" gorm:"not null;comment:Timestamp when the reconciliation scan record last changed"`
}

type ManagedProviderFileReconciliationCandidate struct {
	Id                       int64      `json:"id" gorm:"primaryKey;autoIncrement;comment:Provider file reconciliation candidate numeric identifier"`
	TargetFingerprint        string     `json:"-" gorm:"type:varchar(64);not null;index;comment:HMAC binding the exact provider target"`
	TargetProviderLookupHMAC string     `json:"-" gorm:"type:varchar(64);not null;uniqueIndex:idx_provider_file_candidate_target_lookup;comment:Target-scoped HMAC of the provider file identifier"`
	MetadataHMAC             string     `json:"-" gorm:"type:varchar(64);not null;comment:HMAC binding stable provider file metadata without storing plaintext"`
	ProviderPayload          []byte     `json:"-" gorm:"not null;comment:AEAD envelope containing the provider file identifier and filename"`
	PayloadKeyVersion        string     `json:"-" gorm:"type:varchar(64);not null;comment:Managed consensus key version encrypting the candidate payload"`
	State                    string     `json:"state" gorm:"type:varchar(40);not null;index;comment:Fail-closed reconciliation candidate state"`
	Version                  int64      `json:"version" gorm:"not null;comment:Monotonic candidate compare-and-swap version"`
	FirstCompleteScanId      int64      `json:"first_complete_scan_id" gorm:"not null;comment:First complete scan that observed this candidate without a database foreign key"`
	LastCompleteScanId       int64      `json:"last_complete_scan_id" gorm:"not null;index;comment:Latest complete scan that observed this candidate without a database foreign key"`
	CompleteObservationCount int        `json:"complete_observation_count" gorm:"not null;comment:Number of complete scans with stable metadata"`
	ProviderBytes            int64      `json:"provider_bytes" gorm:"not null;comment:Provider-reported file size used only for bounded reconciliation"`
	ProviderCreatedAt        time.Time  `json:"provider_created_at" gorm:"not null;comment:Provider-reported file creation timestamp"`
	ExpiresAt                *time.Time `json:"expires_at,omitempty" gorm:"index;comment:Provider-reported expiry timestamp when present"`
	FirstObservedAt          time.Time  `json:"first_observed_at" gorm:"not null;comment:Timestamp of the first complete scan observation"`
	LastObservedAt           time.Time  `json:"last_observed_at" gorm:"not null;comment:Timestamp of the latest complete scan observation"`
	QuarantineUntil          *time.Time `json:"quarantine_until,omitempty" gorm:"index;comment:Earliest time this candidate could be separately evaluated for authorization"`
	CreatedAt                time.Time  `json:"created_at" gorm:"not null;comment:Timestamp when the candidate record was created"`
	UpdatedAt                time.Time  `json:"updated_at" gorm:"not null;comment:Timestamp when the candidate record last changed"`
}

type ManagedProviderFileReconciliationObservation struct {
	TargetFingerprint        string
	TargetProviderLookupHMAC string
	MetadataHMAC             string
	ProviderPayload          []byte
	PayloadKeyVersion        string
	State                    string
	ProviderBytes            int64
	ProviderCreatedAt        time.Time
	ExpiresAt                *time.Time
	QuarantineUntil          *time.Time
}

func CreateManagedProviderFileReconciliationScan(ctx context.Context, scan *ManagedProviderFileReconciliationScan) error {
	if ctx == nil || scan == nil || !validManagedProviderFileDigest(scan.TargetFingerprint) || !validManagedProviderFileDigest(scan.ScopeFingerprint) ||
		!validManagedProviderFileKeyVersion(scan.KeyVersion) || scan.State != ManagedProviderFileReconciliationScanStateScanning || scan.Version != 1 ||
		scan.StartedAt.IsZero() || scan.CutoffAt.IsZero() || scan.CutoffAt.After(scan.StartedAt) || scan.CompletedAt != nil || scan.PageCount != 0 ||
		scan.ObjectCount != 0 || scan.CandidateCount != 0 || scan.LastCursorHMAC != "" || scan.FailureCode != "" {
		return ErrManagedProviderFileReconciliationConflict
	}
	return DB.WithContext(ctx).Create(scan).Error
}

func FinishManagedProviderFileReconciliationScan(ctx context.Context, scan *ManagedProviderFileReconciliationScan, state, failureCode, lastCursorHMAC string, pageCount, objectCount int, observations []ManagedProviderFileReconciliationObservation, completedAt time.Time) error {
	if ctx == nil || scan == nil || scan.Id <= 0 || scan.Version <= 0 || scan.State != ManagedProviderFileReconciliationScanStateScanning ||
		(state != ManagedProviderFileReconciliationScanStateComplete && state != ManagedProviderFileReconciliationScanStateIncomplete && state != ManagedProviderFileReconciliationScanStateFailed) ||
		pageCount < 0 || pageCount > 100 || objectCount < 0 || objectCount > 10000 || completedAt.IsZero() ||
		(lastCursorHMAC != "" && !validManagedProviderFileDigest(lastCursorHMAC)) || len(failureCode) > 64 ||
		(state == ManagedProviderFileReconciliationScanStateComplete && failureCode != "") || (state != ManagedProviderFileReconciliationScanStateComplete && strings.TrimSpace(failureCode) == "") ||
		(state != ManagedProviderFileReconciliationScanStateComplete && len(observations) != 0) ||
		(state == ManagedProviderFileReconciliationScanStateComplete && len(observations) > objectCount) {
		return ErrManagedProviderFileReconciliationConflict
	}
	completedAt = completedAt.UTC().Truncate(time.Second)
	return DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		candidateCount := 0
		if state == ManagedProviderFileReconciliationScanStateComplete {
			for index := range observations {
				if err := upsertManagedProviderFileReconciliationCandidate(tx, scan, &observations[index], completedAt); err != nil {
					return err
				}
				candidateCount++
			}
		}
		result := tx.Model(&ManagedProviderFileReconciliationScan{}).Where("id = ? AND version = ? AND state = ?", scan.Id, scan.Version, scan.State).Updates(map[string]interface{}{
			"state": state, "version": scan.Version + 1, "completed_at": completedAt, "page_count": pageCount, "object_count": objectCount,
			"candidate_count": candidateCount, "last_cursor_hmac": lastCursorHMAC, "failure_code": failureCode,
		})
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != 1 {
			return ErrManagedProviderFileReconciliationConflict
		}
		return nil
	})
}

func GetManagedProviderFileLifecycleByTargetLookup(ctx context.Context, targetLookupHMAC string) (*ManagedProviderFileLifecycle, error) {
	if ctx == nil || !validManagedProviderFileDigest(targetLookupHMAC) {
		return nil, gorm.ErrRecordNotFound
	}
	var lifecycle ManagedProviderFileLifecycle
	if err := DB.WithContext(ctx).First(&lifecycle, "target_provider_lookup_hmac = ?", targetLookupHMAC).Error; err != nil {
		return nil, err
	}
	return &lifecycle, nil
}

func upsertManagedProviderFileReconciliationCandidate(tx *gorm.DB, scan *ManagedProviderFileReconciliationScan, observation *ManagedProviderFileReconciliationObservation, observedAt time.Time) error {
	if observation == nil || observation.TargetFingerprint != scan.TargetFingerprint || !validManagedProviderFileDigest(observation.TargetProviderLookupHMAC) ||
		!validManagedProviderFileDigest(observation.MetadataHMAC) || len(observation.ProviderPayload) == 0 || !validManagedProviderFileKeyVersion(observation.PayloadKeyVersion) ||
		observation.ProviderBytes < 0 || observation.ProviderCreatedAt.IsZero() || (observation.ExpiresAt != nil && !observation.ExpiresAt.After(observation.ProviderCreatedAt)) ||
		!validManagedProviderFileReconciliationCandidateState(observation.State) {
		return ErrManagedProviderFileReconciliationConflict
	}
	var existing ManagedProviderFileReconciliationCandidate
	err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&existing, "target_provider_lookup_hmac = ?", observation.TargetProviderLookupHMAC).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		candidate := ManagedProviderFileReconciliationCandidate{
			TargetFingerprint: observation.TargetFingerprint, TargetProviderLookupHMAC: observation.TargetProviderLookupHMAC,
			MetadataHMAC: observation.MetadataHMAC, ProviderPayload: append([]byte(nil), observation.ProviderPayload...), PayloadKeyVersion: observation.PayloadKeyVersion,
			State: observation.State, Version: 1, FirstCompleteScanId: scan.Id, LastCompleteScanId: scan.Id, CompleteObservationCount: 1,
			ProviderBytes: observation.ProviderBytes, ProviderCreatedAt: observation.ProviderCreatedAt.UTC(), ExpiresAt: observation.ExpiresAt,
			FirstObservedAt: observedAt, LastObservedAt: observedAt, QuarantineUntil: observation.QuarantineUntil,
		}
		return tx.Create(&candidate).Error
	}
	if err != nil {
		return err
	}
	if existing.TargetFingerprint != observation.TargetFingerprint {
		return ErrManagedProviderFileReconciliationConflict
	}
	state := observation.State
	observationCount := existing.CompleteObservationCount + 1
	quarantineUntil := observation.QuarantineUntil
	if existing.State == ManagedProviderFileReconciliationCandidateManaged || existing.State == ManagedProviderFileReconciliationCandidateExcludedPreAttestation {
		state = existing.State
		quarantineUntil = existing.QuarantineUntil
	} else if existing.MetadataHMAC != observation.MetadataHMAC {
		state = ManagedProviderFileReconciliationCandidateAmbiguous
		observationCount = 1
	} else if existing.QuarantineUntil != nil {
		quarantineUntil = existing.QuarantineUntil
	}
	result := tx.Model(&ManagedProviderFileReconciliationCandidate{}).Where("id = ? AND version = ?", existing.Id, existing.Version).Updates(map[string]interface{}{
		"metadata_hmac": observation.MetadataHMAC, "provider_payload": append([]byte(nil), observation.ProviderPayload...), "payload_key_version": observation.PayloadKeyVersion,
		"state": state, "version": existing.Version + 1, "last_complete_scan_id": scan.Id, "complete_observation_count": observationCount,
		"provider_bytes": observation.ProviderBytes, "provider_created_at": observation.ProviderCreatedAt.UTC(), "expires_at": observation.ExpiresAt,
		"last_observed_at": observedAt, "quarantine_until": quarantineUntil,
	})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected != 1 {
		return ErrManagedProviderFileReconciliationConflict
	}
	return nil
}

func validManagedProviderFileReconciliationCandidateState(state string) bool {
	switch state {
	case ManagedProviderFileReconciliationCandidateManaged, ManagedProviderFileReconciliationCandidateExcludedPreAttestation,
		ManagedProviderFileReconciliationCandidateQuarantined, ManagedProviderFileReconciliationCandidateAwaitExpiry,
		ManagedProviderFileReconciliationCandidateAmbiguous:
		return true
	default:
		return false
	}
}

func (scan ManagedProviderFileReconciliationScan) String() string {
	return fmt.Sprintf("ManagedProviderFileReconciliationScan{Id:%d,State:%s}", scan.Id, scan.State)
}
