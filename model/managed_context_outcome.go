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
	ManagedContextOutcomePhaseIntent               = "intent"
	ManagedContextOutcomePhaseMainDispatched       = "main_dispatched"
	ManagedContextOutcomePhaseMainSettled          = "main_settled"
	ManagedContextOutcomePhaseSummaryDispatched    = "summary_dispatched"
	ManagedContextOutcomePhaseSettledPendingCommit = "settled_pending_commit"
	ManagedContextOutcomePhaseCommitted            = "committed"
	ManagedContextOutcomePhaseTerminalFailed       = "terminal_failed"
	ManagedContextOutcomePhaseExpired              = "expired"
)

var (
	ErrManagedContextOutcomeNotFound       = errors.New("managed context outcome not found")
	ErrManagedContextOutcomeConflict       = errors.New("managed context outcome conflict")
	ErrManagedContextOutcomeUnknown        = errors.New("managed context outcome is unknown")
	ErrManagedContextOutcomeExpired        = errors.New("managed context outcome expired")
	ErrManagedContextOutcomePhaseConflict  = errors.New("managed context outcome phase conflict")
	ErrManagedContextOutcomeLookupConflict = errors.New("managed context outcome key rotation conflict")
)

// ManagedContextOutcome persists the resumable result of one managed revision intent.
type ManagedContextOutcome struct {
	Id                        int64      `json:"id" gorm:"primaryKey;autoIncrement;comment:Managed context outcome numeric identifier"`
	LookupHMAC                string     `json:"-" gorm:"type:varchar(64);not null;uniqueIndex;comment:Versioned HMAC for the scoped client idempotency key"`
	RevisionIntentHMAC        string     `json:"-" gorm:"type:varchar(64);not null;uniqueIndex;comment:Versioned HMAC uniquely binding the managed revision intent"`
	OwnerHMAC                 string     `json:"-" gorm:"type:varchar(64);not null;index;comment:HMAC identifying user token and endpoint ownership"`
	ConversationHMAC          string     `json:"-" gorm:"type:varchar(64);not null;index;comment:HMAC identifying the managed conversation"`
	LookupKeyVersion          string     `json:"-" gorm:"type:varchar(64);not null;index;comment:Key version used for lookup and encrypted payloads"`
	ExpectedRevision          uint64     `json:"expected_revision" gorm:"not null;index;comment:Conversation revision consumed by this intent"`
	RequestFingerprint        string     `json:"-" gorm:"type:varchar(64);not null;comment:Digest binding immutable request intent inputs"`
	Phase                     string     `json:"phase" gorm:"type:varchar(32);not null;index;comment:Durable managed outcome lifecycle phase"`
	ResponseStatus            int        `json:"response_status" gorm:"not null;comment:Frozen downstream HTTP response status"`
	ResponseContentType       string     `json:"response_content_type" gorm:"type:varchar(128);not null;comment:Frozen safe downstream content type"`
	ResponsePayload           []byte     `json:"-" gorm:"comment:AEAD envelope containing the frozen response body"`
	AssistantPayload          []byte     `json:"-" gorm:"comment:AEAD envelope containing normalized assistant output"`
	SummaryExecutionPayload   []byte     `json:"-" gorm:"comment:AEAD envelope containing the frozen summary execution snapshot"`
	SummaryResultPayload      []byte     `json:"-" gorm:"comment:AEAD envelope containing the validated summary result"`
	NextStatePayload          []byte     `json:"-" gorm:"comment:AEAD envelope containing the exact next managed consensus state"`
	MainBillingOperationId    int64      `json:"main_billing_operation_id" gorm:"not null;index;comment:Main billing operation numeric identifier when settled"`
	SummaryBillingOperationId int64      `json:"summary_billing_operation_id" gorm:"not null;index;comment:Summary billing operation numeric identifier when settled"`
	ExpiresAt                 time.Time  `json:"expires_at" gorm:"not null;index;comment:Hard expiry copied from managed context state TTL"`
	CreatedAt                 time.Time  `json:"created_at" gorm:"not null;comment:Timestamp when the revision intent was created"`
	UpdatedAt                 time.Time  `json:"updated_at" gorm:"not null;comment:Timestamp when the outcome last changed"`
	MainDispatchedAt          *time.Time `json:"main_dispatched_at,omitempty" gorm:"comment:Timestamp before the main upstream dispatch"`
	MainSettledAt             *time.Time `json:"main_settled_at,omitempty" gorm:"comment:Timestamp when main settlement and checkpoint committed"`
	SummaryDispatchedAt       *time.Time `json:"summary_dispatched_at,omitempty" gorm:"comment:Timestamp before the summary upstream dispatch"`
	SummarySettledAt          *time.Time `json:"summary_settled_at,omitempty" gorm:"comment:Timestamp when summary settlement and checkpoint committed"`
	CommittedAt               *time.Time `json:"committed_at,omitempty" gorm:"comment:Timestamp when Redis revision commit was confirmed"`
	ExpiredAt                 *time.Time `json:"expired_at,omitempty" gorm:"comment:Timestamp when expired payloads were scrubbed"`
}

type ManagedContextOutcomeLookupCandidate struct {
	LookupHMAC         string
	RevisionIntentHMAC string
	OwnerHMAC          string
	ConversationHMAC   string
	KeyVersion         string
}

type ManagedContextOutcomeIdentity struct {
	Candidates         []ManagedContextOutcomeLookupCandidate
	ExpectedRevision   uint64
	RequestFingerprint string
}

type ManagedContextOutcomeCheckpoint struct {
	OutcomeId               int64
	RequestFingerprint      string
	ExpectedPhase           string
	NextPhase               string
	ResponseStatus          int
	ResponseContentType     string
	ResponsePayload         []byte
	AssistantPayload        []byte
	SummaryExecutionPayload []byte
	SummaryResultPayload    []byte
	NextStatePayload        []byte
}

func validateManagedContextOutcomeIdentity(identity ManagedContextOutcomeIdentity) error {
	if len(identity.Candidates) == 0 || len(identity.Candidates) > 5 || strings.TrimSpace(identity.RequestFingerprint) == "" || len(identity.RequestFingerprint) > 64 {
		return fmt.Errorf("managed context outcome identity is invalid")
	}
	seenLookup := make(map[string]struct{}, len(identity.Candidates))
	seenRevision := make(map[string]struct{}, len(identity.Candidates))
	for _, candidate := range identity.Candidates {
		if strings.TrimSpace(candidate.LookupHMAC) == "" || len(candidate.LookupHMAC) > 64 ||
			strings.TrimSpace(candidate.RevisionIntentHMAC) == "" || len(candidate.RevisionIntentHMAC) > 64 ||
			strings.TrimSpace(candidate.OwnerHMAC) == "" || len(candidate.OwnerHMAC) > 64 ||
			strings.TrimSpace(candidate.ConversationHMAC) == "" || len(candidate.ConversationHMAC) > 64 ||
			strings.TrimSpace(candidate.KeyVersion) == "" || len(candidate.KeyVersion) > 64 {
			return fmt.Errorf("managed context outcome lookup candidate is invalid")
		}
		if _, exists := seenLookup[candidate.LookupHMAC]; exists {
			return fmt.Errorf("managed context outcome lookup candidates contain duplicates")
		}
		if _, exists := seenRevision[candidate.RevisionIntentHMAC]; exists {
			return fmt.Errorf("managed context outcome revision candidates contain duplicates")
		}
		seenLookup[candidate.LookupHMAC] = struct{}{}
		seenRevision[candidate.RevisionIntentHMAC] = struct{}{}
	}
	return nil
}

func findManagedContextOutcome(tx *gorm.DB, identity ManagedContextOutcomeIdentity, lock bool) (*ManagedContextOutcome, error) {
	lookupValues := make([]string, 0, len(identity.Candidates))
	revisionValues := make([]string, 0, len(identity.Candidates))
	for _, candidate := range identity.Candidates {
		lookupValues = append(lookupValues, candidate.LookupHMAC)
		revisionValues = append(revisionValues, candidate.RevisionIntentHMAC)
	}
	query := tx.Where("lookup_hmac IN ? OR revision_intent_hmac IN ?", lookupValues, revisionValues)
	if lock {
		query = query.Clauses(clause.Locking{Strength: "UPDATE"})
	}
	var outcomes []ManagedContextOutcome
	if err := query.Limit(2).Find(&outcomes).Error; err != nil {
		return nil, err
	}
	if len(outcomes) == 0 {
		return nil, gorm.ErrRecordNotFound
	}
	if len(outcomes) > 1 {
		return nil, ErrManagedContextOutcomeLookupConflict
	}
	outcome := outcomes[0]
	matched := false
	for _, candidate := range identity.Candidates {
		if outcome.LookupHMAC == candidate.LookupHMAC && outcome.RevisionIntentHMAC == candidate.RevisionIntentHMAC &&
			outcome.OwnerHMAC == candidate.OwnerHMAC && outcome.ConversationHMAC == candidate.ConversationHMAC &&
			outcome.LookupKeyVersion == candidate.KeyVersion {
			matched = true
			break
		}
	}
	if !matched || outcome.ExpectedRevision != identity.ExpectedRevision || outcome.RequestFingerprint != identity.RequestFingerprint {
		return nil, ErrManagedContextOutcomeConflict
	}
	return &outcome, nil
}

func ReserveManagedContextOutcome(ctx context.Context, identity ManagedContextOutcomeIdentity, expiresAt time.Time) (*ManagedContextOutcome, bool, error) {
	if err := validateManagedContextOutcomeIdentity(identity); err != nil {
		return nil, false, err
	}
	if expiresAt.IsZero() || !expiresAt.After(time.Now()) {
		return nil, false, fmt.Errorf("managed context outcome expiry is invalid")
	}
	var outcome *ManagedContextOutcome
	created := false
	err := DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		existing, err := findManagedContextOutcome(tx, identity, true)
		if err == nil {
			outcome = existing
			return nil
		}
		if !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
		candidate := identity.Candidates[0]
		outcome = &ManagedContextOutcome{
			LookupHMAC: candidate.LookupHMAC, RevisionIntentHMAC: candidate.RevisionIntentHMAC,
			OwnerHMAC: candidate.OwnerHMAC, ConversationHMAC: candidate.ConversationHMAC,
			LookupKeyVersion: candidate.KeyVersion, ExpectedRevision: identity.ExpectedRevision,
			RequestFingerprint: identity.RequestFingerprint, Phase: ManagedContextOutcomePhaseIntent,
			ExpiresAt: expiresAt,
		}
		if err := tx.Create(outcome).Error; err != nil {
			return err
		}
		created = true
		return nil
	})
	if err == nil {
		if !outcome.ExpiresAt.After(time.Now()) || outcome.Phase == ManagedContextOutcomePhaseExpired {
			if outcome.Phase != ManagedContextOutcomePhaseExpired {
				if scrubErr := scrubExpiredManagedContextOutcome(ctx, outcome.Id, outcome.Phase); scrubErr != nil {
					return nil, false, errors.Join(ErrManagedContextOutcomeExpired, scrubErr)
				}
			}
			return nil, false, ErrManagedContextOutcomeExpired
		}
		return outcome, created, nil
	}
	existing, lookupErr := findManagedContextOutcome(DB.WithContext(ctx), identity, false)
	if lookupErr == nil {
		if !existing.ExpiresAt.After(time.Now()) || existing.Phase == ManagedContextOutcomePhaseExpired {
			if existing.Phase != ManagedContextOutcomePhaseExpired {
				if scrubErr := scrubExpiredManagedContextOutcome(ctx, existing.Id, existing.Phase); scrubErr != nil {
					return nil, false, errors.Join(ErrManagedContextOutcomeExpired, scrubErr)
				}
			}
			return nil, false, ErrManagedContextOutcomeExpired
		}
		return existing, false, nil
	}
	return nil, false, errors.Join(err, lookupErr)
}

func AdvanceManagedContextOutcomePhase(ctx context.Context, outcomeId int64, fingerprint, expectedPhase, nextPhase string) error {
	if outcomeId <= 0 || strings.TrimSpace(fingerprint) == "" || expectedPhase == "" || nextPhase == "" {
		return fmt.Errorf("managed context outcome transition is invalid")
	}
	now := time.Now()
	updates := map[string]interface{}{"phase": nextPhase}
	switch nextPhase {
	case ManagedContextOutcomePhaseMainDispatched:
		updates["main_dispatched_at"] = now
	case ManagedContextOutcomePhaseSummaryDispatched:
		updates["summary_dispatched_at"] = now
	case ManagedContextOutcomePhaseCommitted:
		updates["committed_at"] = now
	case ManagedContextOutcomePhaseTerminalFailed, ManagedContextOutcomePhaseExpired:
	default:
		return fmt.Errorf("managed context outcome transition target is invalid")
	}
	result := DB.WithContext(ctx).Model(&ManagedContextOutcome{}).
		Where("id = ? AND request_fingerprint = ? AND phase = ? AND expires_at > ?", outcomeId, fingerprint, expectedPhase, now).
		Updates(updates)
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected != 1 {
		return ErrManagedContextOutcomePhaseConflict
	}
	return nil
}

func applyManagedContextOutcomeCheckpoint(tx *gorm.DB, billingOperationId int64, checkpoint *ManagedContextOutcomeCheckpoint) error {
	if checkpoint == nil {
		return nil
	}
	if checkpoint.OutcomeId <= 0 || strings.TrimSpace(checkpoint.RequestFingerprint) == "" || billingOperationId <= 0 {
		return fmt.Errorf("managed context outcome checkpoint is invalid")
	}
	now := time.Now()
	updates := map[string]interface{}{"phase": checkpoint.NextPhase}
	switch checkpoint.NextPhase {
	case ManagedContextOutcomePhaseMainSettled:
		if checkpoint.ExpectedPhase != ManagedContextOutcomePhaseMainDispatched || checkpoint.ResponseStatus < 200 || checkpoint.ResponseStatus >= 300 ||
			len(checkpoint.ResponsePayload) == 0 || len(checkpoint.AssistantPayload) == 0 || len(checkpoint.SummaryExecutionPayload) == 0 || strings.TrimSpace(checkpoint.ResponseContentType) == "" {
			return fmt.Errorf("managed context main checkpoint is invalid")
		}
		updates["response_status"] = checkpoint.ResponseStatus
		updates["response_content_type"] = checkpoint.ResponseContentType
		updates["response_payload"] = checkpoint.ResponsePayload
		updates["assistant_payload"] = checkpoint.AssistantPayload
		updates["summary_execution_payload"] = checkpoint.SummaryExecutionPayload
		updates["main_billing_operation_id"] = billingOperationId
		updates["main_settled_at"] = now
	case ManagedContextOutcomePhaseSettledPendingCommit:
		if checkpoint.ExpectedPhase != ManagedContextOutcomePhaseSummaryDispatched || len(checkpoint.SummaryResultPayload) == 0 || len(checkpoint.NextStatePayload) == 0 {
			return fmt.Errorf("managed context summary checkpoint is invalid")
		}
		updates["summary_result_payload"] = checkpoint.SummaryResultPayload
		updates["next_state_payload"] = checkpoint.NextStatePayload
		updates["summary_billing_operation_id"] = billingOperationId
		updates["summary_settled_at"] = now
	default:
		return fmt.Errorf("managed context outcome checkpoint target is invalid")
	}
	result := tx.Model(&ManagedContextOutcome{}).
		Where("id = ? AND request_fingerprint = ? AND phase = ? AND expires_at > ?", checkpoint.OutcomeId, checkpoint.RequestFingerprint, checkpoint.ExpectedPhase, now).
		Updates(updates)
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected != 1 {
		return ErrManagedContextOutcomePhaseConflict
	}
	return nil
}

func GetManagedContextOutcome(ctx context.Context, outcomeId int64, fingerprint string) (*ManagedContextOutcome, error) {
	var outcome ManagedContextOutcome
	if err := DB.WithContext(ctx).First(&outcome, "id = ? AND request_fingerprint = ?", outcomeId, fingerprint).Error; err != nil {
		return nil, err
	}
	if !outcome.ExpiresAt.After(time.Now()) || outcome.Phase == ManagedContextOutcomePhaseExpired {
		if outcome.Phase != ManagedContextOutcomePhaseExpired {
			if scrubErr := scrubExpiredManagedContextOutcome(ctx, outcome.Id, outcome.Phase); scrubErr != nil {
				return nil, errors.Join(ErrManagedContextOutcomeExpired, scrubErr)
			}
		}
		return nil, ErrManagedContextOutcomeExpired
	}
	return &outcome, nil
}

func scrubExpiredManagedContextOutcome(ctx context.Context, outcomeId int64, phase string) error {
	now := time.Now()
	return DB.WithContext(ctx).Model(&ManagedContextOutcome{}).
		Where("id = ? AND phase = ? AND expires_at <= ?", outcomeId, phase, now).
		Updates(map[string]interface{}{
			"phase": ManagedContextOutcomePhaseExpired, "response_status": 0, "response_content_type": "",
			"response_payload": []byte(nil), "assistant_payload": []byte(nil), "summary_execution_payload": []byte(nil),
			"summary_result_payload": []byte(nil), "next_state_payload": []byte(nil), "expired_at": now,
		}).Error
}

type ManagedContextOutcomeMigration struct {
	OutcomeId                 int64
	RequestFingerprint        string
	Previous                  ManagedContextOutcomeLookupCandidate
	Active                    ManagedContextOutcomeLookupCandidate
	ResponsePayload           []byte
	AssistantPayload          []byte
	SummaryExecutionPayload   []byte
	SummaryResultPayload      []byte
	NextStatePayload          []byte
	PreviousBillingLookupHMAC string
	ActiveBillingLookupHMAC   string
}

func MigrateManagedContextOutcome(ctx context.Context, migration ManagedContextOutcomeMigration) error {
	if migration.OutcomeId <= 0 || migration.RequestFingerprint == "" || migration.Previous.KeyVersion == migration.Active.KeyVersion {
		return fmt.Errorf("managed context outcome migration is invalid")
	}
	return DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var outcome ManagedContextOutcome
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&outcome, "id = ?", migration.OutcomeId).Error; err != nil {
			return err
		}
		if outcome.RequestFingerprint != migration.RequestFingerprint || outcome.LookupHMAC != migration.Previous.LookupHMAC ||
			outcome.RevisionIntentHMAC != migration.Previous.RevisionIntentHMAC || outcome.LookupKeyVersion != migration.Previous.KeyVersion {
			return ErrManagedContextOutcomeConflict
		}
		updates := map[string]interface{}{
			"lookup_hmac": migration.Active.LookupHMAC, "revision_intent_hmac": migration.Active.RevisionIntentHMAC,
			"owner_hmac": migration.Active.OwnerHMAC, "conversation_hmac": migration.Active.ConversationHMAC,
			"lookup_key_version": migration.Active.KeyVersion,
			"response_payload":   migration.ResponsePayload, "assistant_payload": migration.AssistantPayload,
			"summary_execution_payload": migration.SummaryExecutionPayload, "summary_result_payload": migration.SummaryResultPayload,
			"next_state_payload": migration.NextStatePayload,
		}
		if err := tx.Model(&ManagedContextOutcome{}).Where("id = ?", outcome.Id).Updates(updates).Error; err != nil {
			return ErrManagedContextOutcomeLookupConflict
		}
		if strings.TrimSpace(migration.PreviousBillingLookupHMAC) == "" || strings.TrimSpace(migration.ActiveBillingLookupHMAC) == "" {
			return fmt.Errorf("managed context outcome billing migration is invalid")
		}
		if err := tx.Model(&BillingOperation{}).
			Where("lookup_hmac = ? AND owner_hmac = ? AND conversation_hmac = ? AND expected_revision = ?", migration.PreviousBillingLookupHMAC, migration.Previous.OwnerHMAC, migration.Previous.ConversationHMAC, outcome.ExpectedRevision).
			Updates(map[string]interface{}{
				"lookup_hmac":        migration.ActiveBillingLookupHMAC,
				"owner_hmac":         migration.Active.OwnerHMAC,
				"conversation_hmac":  migration.Active.ConversationHMAC,
				"lookup_key_version": migration.Active.KeyVersion,
			}).Error; err != nil {
			return ErrManagedContextOutcomeLookupConflict
		}
		return nil
	})
}
