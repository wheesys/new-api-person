package model

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/constant"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const (
	ManagedProviderFileProviderOpenAI  = "openai"
	ManagedProviderFilePurposeUserData = "user_data"

	ManagedProviderFileLifecycleStateIntent           = "intent"
	ManagedProviderFileLifecycleStateUploadDispatched = "upload_dispatched"
	ManagedProviderFileLifecycleStateUploadFailed     = "upload_failed"
	ManagedProviderFileLifecycleStateUploadUnknown    = "upload_unknown"
	ManagedProviderFileLifecycleStateActive           = "active"
	ManagedProviderFileLifecycleStateDeletionPending  = "deletion_pending"
	ManagedProviderFileLifecycleStateDeleted          = "deleted"
	ManagedProviderFileLifecycleStateDeletionFailed   = "deletion_failed"

	ManagedProviderFileDeletionOutboxStatePending        = "pending"
	ManagedProviderFileDeletionOutboxStateInProgress     = "in_progress"
	ManagedProviderFileDeletionOutboxStateRetryWait      = "retry_wait"
	ManagedProviderFileDeletionOutboxStateCompleted      = "completed"
	ManagedProviderFileDeletionOutboxStateTerminalFailed = "terminal_failed"

	ManagedProviderFileDeletionResultDeleted  = "deleted"
	ManagedProviderFileDeletionResultNotFound = "not_found"
	ManagedProviderFileDeletionResultFailed   = "failed"

	ManagedProviderFileLifecycleEventIntentCreated          = "intent_created"
	ManagedProviderFileLifecycleEventUploadDispatched       = "upload_dispatched"
	ManagedProviderFileLifecycleEventUploadFailed           = "upload_failed"
	ManagedProviderFileLifecycleEventUploadUnknown          = "upload_unknown"
	ManagedProviderFileLifecycleEventActivated              = "activated"
	ManagedProviderFileLifecycleEventDeletionStarted        = "deletion_started"
	ManagedProviderFileLifecycleEventDeletionAttemptStarted = "deletion_attempt_started"
	ManagedProviderFileLifecycleEventDeletionRetryScheduled = "deletion_retry_scheduled"
	ManagedProviderFileLifecycleEventDeletionCompleted      = "deletion_completed"
	ManagedProviderFileLifecycleEventDeletionTerminalFailed = "deletion_terminal_failed"

	maxManagedProviderFileDeletionAttempts = 100
)

var (
	ErrManagedProviderFileLifecycleConflict       = errors.New("managed provider file lifecycle conflict")
	ErrManagedProviderFileLifecycleStateConflict  = errors.New("managed provider file lifecycle state conflict")
	ErrManagedProviderFileLifecycleLookupConflict = errors.New("managed provider file lifecycle lookup conflict")
	ErrManagedProviderFileDeletionLeaseLost       = errors.New("managed provider file deletion lease lost")
	ErrManagedProviderFileEventAppendOnly         = errors.New("managed provider file lifecycle events are append-only")
)

// ManagedProviderFileLifecycle is the durable ownership and exact-credential authority for one gateway-uploaded file.
type ManagedProviderFileLifecycle struct {
	Id                              int64      `json:"id" gorm:"primaryKey;autoIncrement;comment:Managed provider file lifecycle numeric identifier"`
	UploadIntentHMAC                string     `json:"-" gorm:"type:varchar(64);not null;uniqueIndex:idx_managed_provider_file_upload_intent;comment:Versioned HMAC uniquely identifying the gateway upload intent"`
	HandleLookupHMAC                string     `json:"-" gorm:"type:varchar(64);not null;uniqueIndex:idx_managed_provider_file_handle_lookup;comment:Versioned owner-bound HMAC for the opaque gateway handle"`
	ProviderLookupHMAC              *string    `json:"-" gorm:"type:varchar(64);uniqueIndex:idx_managed_provider_file_provider_lookup;comment:Versioned owner-scoped HMAC for the provider file reference"`
	OwnerHMAC                       string     `json:"-" gorm:"type:varchar(64);not null;index;comment:HMAC identifying user token and endpoint ownership"`
	LookupKeyVersion                string     `json:"-" gorm:"type:varchar(64);not null;index;comment:Key version used for lookup HMAC values and encrypted payloads"`
	RequestFingerprint              string     `json:"-" gorm:"type:varchar(64);not null;comment:Digest binding immutable upload intent inputs"`
	Provider                        string     `json:"provider" gorm:"type:varchar(32);not null;index;comment:Canonical native provider identifier"`
	State                           string     `json:"state" gorm:"type:varchar(32);not null;index;comment:Durable provider file lifecycle state"`
	Version                         int64      `json:"version" gorm:"not null;comment:Monotonic lifecycle compare-and-swap version"`
	LastEventSequence               int64      `json:"last_event_sequence" gorm:"not null;comment:Sequence of the latest committed lifecycle event"`
	LastEventHMAC                   string     `json:"-" gorm:"type:varchar(64);not null;comment:HMAC of the latest committed lifecycle event"`
	ChannelId                       int        `json:"channel_id" gorm:"not null;index;comment:Exact upstream channel used to create the provider file"`
	ChannelType                     int        `json:"channel_type" gorm:"not null;comment:Exact native channel type used to create the provider file"`
	ChannelIsMultiKey               bool       `json:"channel_is_multi_key" gorm:"not null;comment:Whether the creating channel used multiple credential slots"`
	ChannelMultiKeyIndex            int        `json:"channel_multi_key_index" gorm:"not null;comment:Exact credential slot index used to create the provider file"`
	CredentialFingerprint           string     `json:"-" gorm:"type:varchar(64);not null;comment:HMAC fingerprint of the exact creating credential"`
	CredentialFingerprintKeyVersion string     `json:"-" gorm:"type:varchar(64);not null;comment:Key version used for the credential fingerprint"`
	EndpointFingerprint             string     `json:"-" gorm:"type:varchar(64);not null;comment:Digest binding the canonical provider endpoint"`
	ProviderScopeFingerprint        string     `json:"-" gorm:"type:varchar(64);not null;comment:Digest binding provider organization or project scope"`
	ProviderPayload                 []byte     `json:"-" gorm:"comment:AEAD envelope containing the original provider file reference and recovery data"`
	PayloadKeyVersion               string     `json:"-" gorm:"type:varchar(64);not null;comment:Key version used for the provider reference AEAD envelope"`
	Purpose                         string     `json:"purpose" gorm:"type:varchar(64);not null;comment:Provider file purpose frozen at upload intent creation"`
	ProviderBytes                   int64      `json:"provider_bytes" gorm:"not null;comment:Authoritative provider-reported file size in bytes"`
	ProviderCreatedAt               *time.Time `json:"provider_created_at,omitempty" gorm:"comment:Authoritative provider file creation timestamp"`
	MetadataVerifiedAt              *time.Time `json:"metadata_verified_at,omitempty" gorm:"comment:Timestamp when authoritative provider metadata was verified"`
	ExpiresAt                       *time.Time `json:"expires_at,omitempty" gorm:"index;comment:Authoritative provider file expiry timestamp"`
	CreatedAt                       time.Time  `json:"created_at" gorm:"not null;comment:Timestamp when the upload intent was created"`
	UpdatedAt                       time.Time  `json:"updated_at" gorm:"not null;comment:Timestamp when the lifecycle last changed"`
	ActivatedAt                     *time.Time `json:"activated_at,omitempty" gorm:"comment:Timestamp when provider ownership and expiry were activated"`
	UploadDispatchedAt              *time.Time `json:"upload_dispatched_at,omitempty" gorm:"comment:Timestamp committed before provider upload dispatch"`
	UploadFailedAt                  *time.Time `json:"upload_failed_at,omitempty" gorm:"comment:Timestamp when provider upload reached a known failed terminal state"`
	UploadUnknownAt                 *time.Time `json:"upload_unknown_at,omitempty" gorm:"comment:Timestamp when provider upload outcome became unknown"`
	TerminalReasonCode              string     `json:"terminal_reason_code,omitempty" gorm:"type:varchar(64);not null;comment:Bounded non-sensitive terminal reason code"`
	DeletionStartedAt               *time.Time `json:"deletion_started_at,omitempty" gorm:"comment:Timestamp when the first deletion attempt was claimed"`
	DeletedAt                       *time.Time `json:"deleted_at,omitempty" gorm:"comment:Timestamp when provider deletion reached a successful terminal state"`
	DeletionFailedAt                *time.Time `json:"deletion_failed_at,omitempty" gorm:"comment:Timestamp when provider deletion reached a failed terminal state"`
}

// ManagedProviderFileDeletionOutbox schedules recoverable, bounded provider deletion attempts.
type ManagedProviderFileDeletionOutbox struct {
	Id             int64      `json:"id" gorm:"primaryKey;autoIncrement;comment:Managed provider file deletion outbox numeric identifier"`
	LifecycleId    int64      `json:"lifecycle_id" gorm:"not null;uniqueIndex:idx_managed_provider_file_deletion_lifecycle;comment:Lifecycle record scheduled for deletion without a database foreign key"`
	OperationHMAC  string     `json:"-" gorm:"type:varchar(64);not null;uniqueIndex:idx_managed_provider_file_deletion_operation;comment:Versioned HMAC uniquely identifying the deletion operation"`
	State          string     `json:"state" gorm:"type:varchar(32);not null;index:idx_managed_provider_file_deletion_ready,priority:1;comment:Durable deletion delivery state"`
	Version        int64      `json:"version" gorm:"not null;comment:Monotonic deletion outbox compare-and-swap version"`
	AttemptCount   int        `json:"attempt_count" gorm:"not null;comment:Number of deletion attempts already claimed"`
	MaxAttempts    int        `json:"max_attempts" gorm:"not null;comment:Hard upper bound for deletion attempts"`
	NextAttemptAt  time.Time  `json:"next_attempt_at" gorm:"not null;index:idx_managed_provider_file_deletion_ready,priority:2;comment:Earliest timestamp when another deletion attempt may be claimed"`
	LeaseTokenHMAC string     `json:"-" gorm:"type:varchar(64);not null;comment:HMAC fencing token owning the current deletion lease"`
	LeaseExpiresAt *time.Time `json:"lease_expires_at,omitempty" gorm:"index;comment:Expiry timestamp of the current deletion lease"`
	LastErrorCode  string     `json:"last_error_code,omitempty" gorm:"type:varchar(64);not null;comment:Bounded non-sensitive reason code from the latest failed attempt"`
	TerminalResult string     `json:"terminal_result,omitempty" gorm:"type:varchar(32);not null;comment:Bounded deleted not-found or failed terminal result"`
	CreatedAt      time.Time  `json:"created_at" gorm:"not null;comment:Timestamp when expiry deletion was scheduled"`
	UpdatedAt      time.Time  `json:"updated_at" gorm:"not null;comment:Timestamp when deletion delivery last changed"`
	LastAttemptAt  *time.Time `json:"last_attempt_at,omitempty" gorm:"comment:Timestamp when the latest deletion attempt was claimed"`
	CompletedAt    *time.Time `json:"completed_at,omitempty" gorm:"comment:Timestamp when deletion delivery reached a terminal state"`
}

// ManagedProviderFileLifecycleEvent is append-only evidence for lifecycle and deletion state changes.
type ManagedProviderFileLifecycleEvent struct {
	Id                int64     `json:"id" gorm:"primaryKey;autoIncrement;comment:Managed provider file lifecycle event numeric identifier"`
	LifecycleId       int64     `json:"lifecycle_id" gorm:"not null;uniqueIndex:idx_managed_provider_file_event_sequence,priority:1;index;comment:Lifecycle record described by this append-only event without a database foreign key"`
	Sequence          int64     `json:"sequence" gorm:"not null;uniqueIndex:idx_managed_provider_file_event_sequence,priority:2;comment:Monotonic event sequence within one lifecycle"`
	PreviousEventHMAC string    `json:"-" gorm:"type:varchar(64);not null;comment:HMAC of the previous event or empty for the first event"`
	EventHMAC         string    `json:"-" gorm:"type:varchar(64);not null;uniqueIndex:idx_managed_provider_file_event_hmac;comment:Versioned HMAC uniquely identifying this event"`
	EventType         string    `json:"event_type" gorm:"type:varchar(48);not null;index;comment:Bounded lifecycle event type"`
	FromState         string    `json:"from_state" gorm:"type:varchar(32);not null;comment:Lifecycle state before the event"`
	ToState           string    `json:"to_state" gorm:"type:varchar(32);not null;comment:Lifecycle state after the event"`
	AttemptCount      int       `json:"attempt_count" gorm:"not null;comment:Deletion attempt count associated with this event"`
	ResultCode        string    `json:"result_code,omitempty" gorm:"type:varchar(64);not null;comment:Bounded non-sensitive event result code"`
	EvidenceDigest    string    `json:"-" gorm:"type:varchar(64);not null;comment:Digest of provider or policy evidence without raw file data"`
	KeyVersion        string    `json:"-" gorm:"type:varchar(64);not null;comment:Key version used to derive the event identity"`
	CreatedAt         time.Time `json:"created_at" gorm:"not null;comment:Timestamp when this append-only event committed"`
}

type ManagedProviderFileLifecycleLookupCandidate struct {
	HandleLookupHMAC string
	OwnerHMAC        string
	KeyVersion       string
}

type ManagedProviderFileLifecycleActivation struct {
	LifecycleId           int64
	ExpectedVersion       int64
	RequestFingerprint    string
	ProviderLookupHMAC    string
	ProviderPayload       []byte
	ProviderBytes         int64
	ProviderCreatedAt     time.Time
	MetadataVerifiedAt    time.Time
	ExpiresAt             time.Time
	DeletionOperationHMAC string
	MaxDeletionAttempts   int
	Event                 ManagedProviderFileLifecycleEvent
}

type ManagedProviderFileDeletionClaim struct {
	OutboxId             int64
	ExpectedVersion      int64
	LeaseTokenHMAC       string
	LeaseExpiresAt       time.Time
	ExpectedState        string
	ExpectedAttemptCount int
	Event                ManagedProviderFileLifecycleEvent
}

type ManagedProviderFileDeletionRetry struct {
	OutboxId        int64
	ExpectedVersion int64
	LeaseTokenHMAC  string
	AttemptCount    int
	NextAttemptAt   time.Time
	ErrorCode       string
	Event           ManagedProviderFileLifecycleEvent
}

type ManagedProviderFileDeletionTerminal struct {
	OutboxId        int64
	ExpectedVersion int64
	LeaseTokenHMAC  string
	AttemptCount    int
	Result          string
	ErrorCode       string
	EvidenceDigest  string
	Event           ManagedProviderFileLifecycleEvent
}

type ManagedProviderFileUploadTransition struct {
	LifecycleId        int64
	ExpectedVersion    int64
	RequestFingerprint string
	ExpectedState      string
	NextState          string
	ReasonCode         string
	Event              ManagedProviderFileLifecycleEvent
}

func (lifecycle ManagedProviderFileLifecycle) Validate() error {
	if !validManagedProviderFileDigest(lifecycle.UploadIntentHMAC) || !validManagedProviderFileDigest(lifecycle.HandleLookupHMAC) || !validManagedProviderFileDigest(lifecycle.OwnerHMAC) ||
		!validManagedProviderFileDigest(lifecycle.RequestFingerprint) || !validManagedProviderFileKeyVersion(lifecycle.LookupKeyVersion) ||
		!validManagedProviderFileKeyVersion(lifecycle.PayloadKeyVersion) ||
		lifecycle.Provider != ManagedProviderFileProviderOpenAI || lifecycle.ChannelId <= 0 || lifecycle.ChannelType != constant.ChannelTypeOpenAI ||
		lifecycle.ChannelIsMultiKey || lifecycle.ChannelMultiKeyIndex != 0 ||
		!validManagedProviderFileDigest(lifecycle.CredentialFingerprint) || !validManagedProviderFileKeyVersion(lifecycle.CredentialFingerprintKeyVersion) ||
		!validManagedProviderFileDigest(lifecycle.EndpointFingerprint) ||
		!validManagedProviderFileDigest(lifecycle.ProviderScopeFingerprint) || lifecycle.Purpose != ManagedProviderFilePurposeUserData ||
		lifecycle.Version <= 0 || lifecycle.ProviderBytes < 0 || len(lifecycle.TerminalReasonCode) > 64 {
		return fmt.Errorf("managed provider file lifecycle identity is invalid")
	}
	if lifecycle.LastEventSequence == 0 {
		if lifecycle.Id != 0 || lifecycle.LastEventHMAC != "" {
			return fmt.Errorf("managed provider file lifecycle event head is invalid")
		}
	} else if lifecycle.LastEventSequence < 0 || !validManagedProviderFileDigest(lifecycle.LastEventHMAC) {
		return fmt.Errorf("managed provider file lifecycle event head is invalid")
	}
	hasBinding := lifecycle.ProviderLookupHMAC != nil || len(lifecycle.ProviderPayload) != 0 || lifecycle.ProviderBytes != 0 || lifecycle.ProviderCreatedAt != nil || lifecycle.MetadataVerifiedAt != nil || lifecycle.ExpiresAt != nil || lifecycle.ActivatedAt != nil
	switch lifecycle.State {
	case ManagedProviderFileLifecycleStateIntent:
		if hasBinding || lifecycle.UploadDispatchedAt != nil || lifecycle.UploadFailedAt != nil || lifecycle.UploadUnknownAt != nil ||
			lifecycle.DeletionStartedAt != nil || lifecycle.DeletedAt != nil || lifecycle.DeletionFailedAt != nil || lifecycle.TerminalReasonCode != "" {
			return fmt.Errorf("managed provider file intent state is invalid")
		}
	case ManagedProviderFileLifecycleStateUploadDispatched:
		if hasBinding || lifecycle.UploadDispatchedAt == nil || lifecycle.UploadDispatchedAt.IsZero() || lifecycle.UploadFailedAt != nil || lifecycle.UploadUnknownAt != nil || lifecycle.TerminalReasonCode != "" {
			return fmt.Errorf("managed provider file upload-dispatched state is invalid")
		}
	case ManagedProviderFileLifecycleStateUploadFailed:
		if hasBinding || lifecycle.UploadFailedAt == nil || lifecycle.UploadFailedAt.IsZero() || strings.TrimSpace(lifecycle.TerminalReasonCode) == "" {
			return fmt.Errorf("managed provider file upload-failed state is invalid")
		}
	case ManagedProviderFileLifecycleStateUploadUnknown:
		if hasBinding || lifecycle.UploadDispatchedAt == nil || lifecycle.UploadUnknownAt == nil || lifecycle.UploadUnknownAt.IsZero() || strings.TrimSpace(lifecycle.TerminalReasonCode) == "" {
			return fmt.Errorf("managed provider file upload-unknown state is invalid")
		}
	case ManagedProviderFileLifecycleStateActive, ManagedProviderFileLifecycleStateDeletionPending, ManagedProviderFileLifecycleStateDeleted, ManagedProviderFileLifecycleStateDeletionFailed:
		if lifecycle.ProviderLookupHMAC == nil || !validManagedProviderFileDigest(*lifecycle.ProviderLookupHMAC) ||
			lifecycle.ProviderBytes <= 0 || lifecycle.ProviderCreatedAt == nil || lifecycle.ProviderCreatedAt.IsZero() || lifecycle.MetadataVerifiedAt == nil || lifecycle.MetadataVerifiedAt.IsZero() ||
			lifecycle.ExpiresAt == nil || lifecycle.ExpiresAt.IsZero() || !lifecycle.ExpiresAt.After(*lifecycle.ProviderCreatedAt) || lifecycle.ActivatedAt == nil {
			return fmt.Errorf("managed provider file active binding is invalid")
		}
		if lifecycle.State != ManagedProviderFileLifecycleStateDeleted && len(lifecycle.ProviderPayload) == 0 {
			return fmt.Errorf("managed provider file active payload is invalid")
		}
		if lifecycle.State == ManagedProviderFileLifecycleStateActive && (lifecycle.DeletionStartedAt != nil || lifecycle.DeletedAt != nil || lifecycle.DeletionFailedAt != nil || lifecycle.TerminalReasonCode != "") {
			return fmt.Errorf("managed provider file active state is invalid")
		}
		if lifecycle.State == ManagedProviderFileLifecycleStateDeletionPending && (lifecycle.DeletionStartedAt == nil || lifecycle.DeletedAt != nil || lifecycle.DeletionFailedAt != nil) {
			return fmt.Errorf("managed provider file deletion-pending state is invalid")
		}
		if lifecycle.State == ManagedProviderFileLifecycleStateDeleted && (lifecycle.DeletionStartedAt == nil || lifecycle.DeletedAt == nil || lifecycle.DeletionFailedAt != nil || len(lifecycle.ProviderPayload) != 0) {
			return fmt.Errorf("managed provider file deleted state is invalid")
		}
		if lifecycle.State == ManagedProviderFileLifecycleStateDeletionFailed && (lifecycle.DeletionStartedAt == nil || lifecycle.DeletedAt != nil || lifecycle.DeletionFailedAt == nil || strings.TrimSpace(lifecycle.TerminalReasonCode) == "") {
			return fmt.Errorf("managed provider file deletion failure state is invalid")
		}
	default:
		return fmt.Errorf("managed provider file lifecycle state is invalid")
	}
	return nil
}

func (outbox ManagedProviderFileDeletionOutbox) Validate() error {
	if outbox.LifecycleId <= 0 || !validManagedProviderFileDigest(outbox.OperationHMAC) || outbox.Version <= 0 || outbox.AttemptCount < 0 ||
		outbox.MaxAttempts <= 0 || outbox.MaxAttempts > maxManagedProviderFileDeletionAttempts || outbox.AttemptCount > outbox.MaxAttempts || outbox.NextAttemptAt.IsZero() ||
		len(outbox.LastErrorCode) > 64 {
		return fmt.Errorf("managed provider file deletion outbox is invalid")
	}
	switch outbox.State {
	case ManagedProviderFileDeletionOutboxStatePending:
		if outbox.AttemptCount != 0 || outbox.LeaseTokenHMAC != "" || outbox.LeaseExpiresAt != nil || outbox.LastAttemptAt != nil || outbox.CompletedAt != nil || outbox.TerminalResult != "" {
			return fmt.Errorf("managed provider file deletion pending state is invalid")
		}
	case ManagedProviderFileDeletionOutboxStateInProgress:
		if outbox.AttemptCount <= 0 || !validManagedProviderFileDigest(outbox.LeaseTokenHMAC) || outbox.LeaseExpiresAt == nil || outbox.LeaseExpiresAt.IsZero() || outbox.LastAttemptAt == nil || outbox.CompletedAt != nil || outbox.TerminalResult != "" {
			return fmt.Errorf("managed provider file deletion in-progress state is invalid")
		}
	case ManagedProviderFileDeletionOutboxStateRetryWait:
		if outbox.AttemptCount <= 0 || outbox.AttemptCount >= outbox.MaxAttempts || outbox.LeaseTokenHMAC != "" || outbox.LeaseExpiresAt != nil || outbox.LastAttemptAt == nil ||
			strings.TrimSpace(outbox.LastErrorCode) == "" || outbox.CompletedAt != nil || outbox.TerminalResult != "" {
			return fmt.Errorf("managed provider file deletion retry state is invalid")
		}
	case ManagedProviderFileDeletionOutboxStateCompleted:
		if outbox.AttemptCount <= 0 || outbox.LeaseTokenHMAC != "" || outbox.LeaseExpiresAt != nil || outbox.CompletedAt == nil ||
			(outbox.TerminalResult != ManagedProviderFileDeletionResultDeleted && outbox.TerminalResult != ManagedProviderFileDeletionResultNotFound) {
			return fmt.Errorf("managed provider file deletion completed state is invalid")
		}
	case ManagedProviderFileDeletionOutboxStateTerminalFailed:
		if outbox.AttemptCount <= 0 || outbox.LeaseTokenHMAC != "" || outbox.LeaseExpiresAt != nil || outbox.CompletedAt == nil ||
			outbox.TerminalResult != ManagedProviderFileDeletionResultFailed || strings.TrimSpace(outbox.LastErrorCode) == "" {
			return fmt.Errorf("managed provider file deletion terminal failure state is invalid")
		}
	default:
		return fmt.Errorf("managed provider file deletion outbox state is invalid")
	}
	return nil
}

func (event ManagedProviderFileLifecycleEvent) Validate() error {
	if event.LifecycleId <= 0 || event.Sequence <= 0 || !validManagedProviderFileDigest(event.EventHMAC) ||
		!validManagedProviderFileDigest(event.EvidenceDigest) || !validManagedProviderFileKeyVersion(event.KeyVersion) ||
		event.AttemptCount < 0 || len(event.ResultCode) > 64 || (event.Sequence == 1 && event.PreviousEventHMAC != "") ||
		(event.Sequence > 1 && !validManagedProviderFileDigest(event.PreviousEventHMAC)) {
		return fmt.Errorf("managed provider file lifecycle event is invalid")
	}
	validTransition := false
	switch event.EventType {
	case ManagedProviderFileLifecycleEventIntentCreated:
		validTransition = event.Sequence == 1 && event.FromState == "" && event.ToState == ManagedProviderFileLifecycleStateIntent && event.AttemptCount == 0 && event.ResultCode == ""
	case ManagedProviderFileLifecycleEventUploadDispatched:
		validTransition = event.FromState == ManagedProviderFileLifecycleStateIntent && event.ToState == ManagedProviderFileLifecycleStateUploadDispatched && event.AttemptCount == 0 && event.ResultCode == ""
	case ManagedProviderFileLifecycleEventUploadFailed:
		validTransition = (event.FromState == ManagedProviderFileLifecycleStateIntent || event.FromState == ManagedProviderFileLifecycleStateUploadDispatched) && event.ToState == ManagedProviderFileLifecycleStateUploadFailed && event.AttemptCount == 0 && strings.TrimSpace(event.ResultCode) != ""
	case ManagedProviderFileLifecycleEventUploadUnknown:
		validTransition = event.FromState == ManagedProviderFileLifecycleStateUploadDispatched && event.ToState == ManagedProviderFileLifecycleStateUploadUnknown && event.AttemptCount == 0 && strings.TrimSpace(event.ResultCode) != ""
	case ManagedProviderFileLifecycleEventActivated:
		validTransition = event.FromState == ManagedProviderFileLifecycleStateUploadDispatched && event.ToState == ManagedProviderFileLifecycleStateActive && event.AttemptCount == 0 && event.ResultCode == ""
	case ManagedProviderFileLifecycleEventDeletionStarted:
		validTransition = event.FromState == ManagedProviderFileLifecycleStateActive && event.ToState == ManagedProviderFileLifecycleStateDeletionPending && event.AttemptCount == 1 && event.ResultCode == ""
	case ManagedProviderFileLifecycleEventDeletionAttemptStarted:
		validTransition = event.FromState == ManagedProviderFileLifecycleStateDeletionPending && event.ToState == ManagedProviderFileLifecycleStateDeletionPending && event.AttemptCount > 1 && event.ResultCode == ""
	case ManagedProviderFileLifecycleEventDeletionRetryScheduled:
		validTransition = event.FromState == ManagedProviderFileLifecycleStateDeletionPending && event.ToState == ManagedProviderFileLifecycleStateDeletionPending && event.AttemptCount > 0 && strings.TrimSpace(event.ResultCode) != ""
	case ManagedProviderFileLifecycleEventDeletionCompleted:
		validTransition = event.FromState == ManagedProviderFileLifecycleStateDeletionPending && event.ToState == ManagedProviderFileLifecycleStateDeleted && event.AttemptCount > 0 &&
			(event.ResultCode == ManagedProviderFileDeletionResultDeleted || event.ResultCode == ManagedProviderFileDeletionResultNotFound)
	case ManagedProviderFileLifecycleEventDeletionTerminalFailed:
		validTransition = event.FromState == ManagedProviderFileLifecycleStateDeletionPending && event.ToState == ManagedProviderFileLifecycleStateDeletionFailed && event.AttemptCount > 0 && strings.TrimSpace(event.ResultCode) != ""
	}
	if !validTransition {
		return fmt.Errorf("managed provider file lifecycle event transition is invalid")
	}
	return nil
}

func (event *ManagedProviderFileLifecycleEvent) BeforeUpdate(_ *gorm.DB) error {
	return ErrManagedProviderFileEventAppendOnly
}

func (event *ManagedProviderFileLifecycleEvent) BeforeDelete(_ *gorm.DB) error {
	return ErrManagedProviderFileEventAppendOnly
}

func CreateManagedProviderFileLifecycleIntent(ctx context.Context, lifecycle ManagedProviderFileLifecycle, event ManagedProviderFileLifecycleEvent) (*ManagedProviderFileLifecycle, bool, error) {
	if lifecycle.Id != 0 || lifecycle.State != ManagedProviderFileLifecycleStateIntent || event.Id != 0 || event.LifecycleId != 0 {
		return nil, false, fmt.Errorf("managed provider file upload intent is invalid")
	}
	lifecycle.Version = 1
	lifecycle.LastEventSequence = 0
	lifecycle.LastEventHMAC = ""
	event.LifecycleId = 1
	if err := lifecycle.Validate(); err != nil {
		return nil, false, err
	}
	if err := event.Validate(); err != nil {
		return nil, false, err
	}
	event.LifecycleId = 0
	var createdLifecycle *ManagedProviderFileLifecycle
	created := false
	err := DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var existing ManagedProviderFileLifecycle
		err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&existing,
			"upload_intent_hmac = ? OR handle_lookup_hmac = ?", lifecycle.UploadIntentHMAC, lifecycle.HandleLookupHMAC).Error
		if err == nil {
			if !sameManagedProviderFileIntent(existing, lifecycle) {
				return ErrManagedProviderFileLifecycleConflict
			}
			createdLifecycle = &existing
			return nil
		}
		if !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
		if err := tx.Create(&lifecycle).Error; err != nil {
			return err
		}
		event.LifecycleId = lifecycle.Id
		if err := appendManagedProviderFileLifecycleEvent(tx, &event); err != nil {
			return err
		}
		headResult := tx.Model(&ManagedProviderFileLifecycle{}).
			Where("id = ? AND version = ? AND last_event_sequence = ?", lifecycle.Id, lifecycle.Version, 0).
			Updates(map[string]interface{}{"last_event_sequence": event.Sequence, "last_event_hmac": event.EventHMAC})
		if headResult.Error != nil {
			return headResult.Error
		}
		if headResult.RowsAffected != 1 {
			return ErrManagedProviderFileLifecycleStateConflict
		}
		lifecycle.LastEventSequence = event.Sequence
		lifecycle.LastEventHMAC = event.EventHMAC
		createdLifecycle = &lifecycle
		created = true
		return nil
	})
	if err == nil {
		return createdLifecycle, created, nil
	}
	var existing ManagedProviderFileLifecycle
	if lookupErr := DB.WithContext(ctx).First(&existing,
		"upload_intent_hmac = ? OR handle_lookup_hmac = ?", lifecycle.UploadIntentHMAC, lifecycle.HandleLookupHMAC).Error; lookupErr == nil {
		if sameManagedProviderFileIntent(existing, lifecycle) {
			return &existing, false, nil
		}
		return nil, false, ErrManagedProviderFileLifecycleConflict
	}
	return nil, false, err
}

func AdvanceManagedProviderFileUploadState(ctx context.Context, transition ManagedProviderFileUploadTransition) error {
	if transition.LifecycleId <= 0 || transition.ExpectedVersion <= 0 || !validManagedProviderFileDigest(transition.RequestFingerprint) || len(transition.ReasonCode) > 64 {
		return fmt.Errorf("managed provider file upload transition is invalid")
	}
	validTransition := transition.ExpectedState == ManagedProviderFileLifecycleStateIntent && transition.NextState == ManagedProviderFileLifecycleStateUploadDispatched && transition.ReasonCode == ""
	validTransition = validTransition || ((transition.ExpectedState == ManagedProviderFileLifecycleStateIntent || transition.ExpectedState == ManagedProviderFileLifecycleStateUploadDispatched) &&
		transition.NextState == ManagedProviderFileLifecycleStateUploadFailed && strings.TrimSpace(transition.ReasonCode) != "")
	validTransition = validTransition || (transition.ExpectedState == ManagedProviderFileLifecycleStateUploadDispatched &&
		transition.NextState == ManagedProviderFileLifecycleStateUploadUnknown && strings.TrimSpace(transition.ReasonCode) != "")
	if !validTransition {
		return fmt.Errorf("managed provider file upload transition is invalid")
	}
	now := time.Now().UTC()
	return DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var lifecycle ManagedProviderFileLifecycle
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&lifecycle, "id = ?", transition.LifecycleId).Error; err != nil {
			return err
		}
		if lifecycle.Version != transition.ExpectedVersion || lifecycle.State != transition.ExpectedState || lifecycle.RequestFingerprint != transition.RequestFingerprint {
			return ErrManagedProviderFileLifecycleStateConflict
		}
		updates := map[string]interface{}{"state": transition.NextState, "terminal_reason_code": transition.ReasonCode}
		switch transition.NextState {
		case ManagedProviderFileLifecycleStateUploadDispatched:
			updates["upload_dispatched_at"] = now
		case ManagedProviderFileLifecycleStateUploadFailed:
			updates["upload_failed_at"] = now
		case ManagedProviderFileLifecycleStateUploadUnknown:
			updates["upload_unknown_at"] = now
		}
		return appendEventAndAdvanceManagedProviderFileLifecycle(tx, &lifecycle, transition.ExpectedVersion, updates, &transition.Event)
	})
}

func ActivateManagedProviderFileLifecycle(ctx context.Context, activation ManagedProviderFileLifecycleActivation) (*ManagedProviderFileLifecycle, *ManagedProviderFileDeletionOutbox, bool, error) {
	activation.ProviderCreatedAt = activation.ProviderCreatedAt.UTC().Truncate(time.Second)
	activation.MetadataVerifiedAt = activation.MetadataVerifiedAt.UTC().Truncate(time.Second)
	activation.ExpiresAt = activation.ExpiresAt.UTC().Truncate(time.Second)
	if activation.LifecycleId <= 0 || activation.ExpectedVersion <= 0 || !validManagedProviderFileDigest(activation.RequestFingerprint) || !validManagedProviderFileDigest(activation.ProviderLookupHMAC) ||
		len(activation.ProviderPayload) == 0 || activation.ProviderBytes <= 0 || activation.ProviderCreatedAt.IsZero() || activation.MetadataVerifiedAt.IsZero() ||
		activation.ExpiresAt.IsZero() || !activation.ExpiresAt.After(time.Now()) || !activation.ExpiresAt.After(activation.ProviderCreatedAt) ||
		!validManagedProviderFileDigest(activation.DeletionOperationHMAC) || activation.MaxDeletionAttempts <= 0 || activation.MaxDeletionAttempts > maxManagedProviderFileDeletionAttempts {
		return nil, nil, false, fmt.Errorf("managed provider file activation is invalid")
	}
	var lifecycle ManagedProviderFileLifecycle
	var outbox ManagedProviderFileDeletionOutbox
	activated := false
	err := DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&lifecycle, "id = ?", activation.LifecycleId).Error; err != nil {
			return err
		}
		if lifecycle.RequestFingerprint != activation.RequestFingerprint {
			return ErrManagedProviderFileLifecycleConflict
		}
		if lifecycle.State == ManagedProviderFileLifecycleStateActive {
			if !sameManagedProviderFileActivation(lifecycle, activation) {
				return ErrManagedProviderFileLifecycleConflict
			}
			if err := tx.First(&outbox, "lifecycle_id = ?", lifecycle.Id).Error; err != nil {
				return err
			}
			if outbox.OperationHMAC != activation.DeletionOperationHMAC || outbox.MaxAttempts != activation.MaxDeletionAttempts {
				return ErrManagedProviderFileLifecycleConflict
			}
			return nil
		}
		if lifecycle.State != ManagedProviderFileLifecycleStateUploadDispatched || lifecycle.Version != activation.ExpectedVersion {
			return ErrManagedProviderFileLifecycleStateConflict
		}
		providerLookupHMAC := activation.ProviderLookupHMAC
		now := time.Now().UTC()
		outbox = ManagedProviderFileDeletionOutbox{
			LifecycleId: lifecycle.Id, OperationHMAC: activation.DeletionOperationHMAC,
			State: ManagedProviderFileDeletionOutboxStatePending, Version: 1, MaxAttempts: activation.MaxDeletionAttempts,
			NextAttemptAt: activation.ExpiresAt,
		}
		if err := outbox.Validate(); err != nil {
			return err
		}
		if err := tx.Create(&outbox).Error; err != nil {
			return err
		}
		if activation.Event.LifecycleId != lifecycle.Id || activation.Event.EventType != ManagedProviderFileLifecycleEventActivated {
			return fmt.Errorf("managed provider file activation event is invalid")
		}
		if err := appendEventAndAdvanceManagedProviderFileLifecycle(tx, &lifecycle, activation.ExpectedVersion, map[string]interface{}{
			"provider_lookup_hmac": providerLookupHMAC, "provider_payload": activation.ProviderPayload,
			"provider_bytes": activation.ProviderBytes, "provider_created_at": activation.ProviderCreatedAt.UTC(),
			"metadata_verified_at": activation.MetadataVerifiedAt.UTC(), "expires_at": activation.ExpiresAt.UTC(),
			"state": ManagedProviderFileLifecycleStateActive, "activated_at": now,
		}, &activation.Event); err != nil {
			return err
		}
		lifecycle.ProviderLookupHMAC = &providerLookupHMAC
		lifecycle.ProviderPayload = activation.ProviderPayload
		lifecycle.ProviderBytes = activation.ProviderBytes
		lifecycle.ProviderCreatedAt = &activation.ProviderCreatedAt
		lifecycle.MetadataVerifiedAt = &activation.MetadataVerifiedAt
		lifecycle.ExpiresAt = &activation.ExpiresAt
		lifecycle.State = ManagedProviderFileLifecycleStateActive
		lifecycle.Version++
		lifecycle.LastEventSequence = activation.Event.Sequence
		lifecycle.LastEventHMAC = activation.Event.EventHMAC
		lifecycle.ActivatedAt = &now
		activated = true
		return nil
	})
	return &lifecycle, &outbox, activated, err
}

func FindManagedProviderFileLifecycle(ctx context.Context, candidates []ManagedProviderFileLifecycleLookupCandidate) (*ManagedProviderFileLifecycle, error) {
	if len(candidates) == 0 || len(candidates) > 5 {
		return nil, fmt.Errorf("managed provider file lookup candidates are invalid")
	}
	lookupHMACs := make([]string, 0, len(candidates))
	seen := make(map[string]struct{}, len(candidates))
	for _, candidate := range candidates {
		if !validManagedProviderFileDigest(candidate.HandleLookupHMAC) || !validManagedProviderFileDigest(candidate.OwnerHMAC) ||
			!validManagedProviderFileKeyVersion(candidate.KeyVersion) {
			return nil, fmt.Errorf("managed provider file lookup candidate is invalid")
		}
		if _, exists := seen[candidate.HandleLookupHMAC]; exists {
			return nil, fmt.Errorf("managed provider file lookup candidates contain duplicates")
		}
		seen[candidate.HandleLookupHMAC] = struct{}{}
		lookupHMACs = append(lookupHMACs, candidate.HandleLookupHMAC)
	}
	var lifecycles []ManagedProviderFileLifecycle
	if err := DB.WithContext(ctx).Where("handle_lookup_hmac IN ?", lookupHMACs).Limit(2).Find(&lifecycles).Error; err != nil {
		return nil, err
	}
	if len(lifecycles) == 0 {
		return nil, gorm.ErrRecordNotFound
	}
	if len(lifecycles) > 1 {
		return nil, ErrManagedProviderFileLifecycleLookupConflict
	}
	lifecycle := lifecycles[0]
	for _, candidate := range candidates {
		if lifecycle.HandleLookupHMAC == candidate.HandleLookupHMAC &&
			lifecycle.OwnerHMAC == candidate.OwnerHMAC && lifecycle.LookupKeyVersion == candidate.KeyVersion {
			return &lifecycle, nil
		}
	}
	return nil, ErrManagedProviderFileLifecycleConflict
}

func ListDueManagedProviderFileDeletions(ctx context.Context, now time.Time, limit int) ([]ManagedProviderFileDeletionOutbox, error) {
	if now.IsZero() {
		return nil, fmt.Errorf("managed provider file deletion query time is invalid")
	}
	if limit <= 0 {
		limit = 1
	}
	if limit > 100 {
		limit = 100
	}
	var outboxes []ManagedProviderFileDeletionOutbox
	err := DB.WithContext(ctx).
		Where("attempt_count < max_attempts").
		Where("((state IN ? AND next_attempt_at <= ? AND (lease_expires_at IS NULL OR lease_expires_at < ?)) OR (state = ? AND lease_expires_at < ?))",
			[]string{ManagedProviderFileDeletionOutboxStatePending, ManagedProviderFileDeletionOutboxStateRetryWait}, now, now,
			ManagedProviderFileDeletionOutboxStateInProgress, now).
		Order("next_attempt_at asc").Order("id asc").Limit(limit).Find(&outboxes).Error
	return outboxes, err
}

func ClaimManagedProviderFileDeletion(ctx context.Context, claim ManagedProviderFileDeletionClaim) (*ManagedProviderFileDeletionOutbox, bool, error) {
	now := time.Now()
	if claim.OutboxId <= 0 || claim.ExpectedVersion <= 0 || !validManagedProviderFileDigest(claim.LeaseTokenHMAC) || !claim.LeaseExpiresAt.After(now) ||
		claim.ExpectedAttemptCount < 0 || (claim.ExpectedState != ManagedProviderFileDeletionOutboxStatePending &&
		claim.ExpectedState != ManagedProviderFileDeletionOutboxStateRetryWait && claim.ExpectedState != ManagedProviderFileDeletionOutboxStateInProgress) {
		return nil, false, fmt.Errorf("managed provider file deletion claim is invalid")
	}
	var outbox ManagedProviderFileDeletionOutbox
	claimed := false
	err := DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&outbox, "id = ?", claim.OutboxId).Error; err != nil {
			return err
		}
		if outbox.State != claim.ExpectedState || outbox.Version != claim.ExpectedVersion || outbox.AttemptCount != claim.ExpectedAttemptCount || outbox.AttemptCount >= outbox.MaxAttempts || outbox.NextAttemptAt.After(now) ||
			(outbox.LeaseExpiresAt != nil && !outbox.LeaseExpiresAt.Before(now)) {
			return nil
		}
		var lifecycle ManagedProviderFileLifecycle
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&lifecycle, "id = ?", outbox.LifecycleId).Error; err != nil {
			return err
		}
		nextAttemptCount := outbox.AttemptCount + 1
		expectedEventType := ManagedProviderFileLifecycleEventDeletionAttemptStarted
		if lifecycle.State == ManagedProviderFileLifecycleStateActive {
			expectedEventType = ManagedProviderFileLifecycleEventDeletionStarted
		} else if lifecycle.State != ManagedProviderFileLifecycleStateDeletionPending {
			return ErrManagedProviderFileLifecycleStateConflict
		}
		if claim.Event.LifecycleId != lifecycle.Id || claim.Event.EventType != expectedEventType || claim.Event.AttemptCount != nextAttemptCount {
			return fmt.Errorf("managed provider file deletion claim event is invalid")
		}
		result := tx.Model(&ManagedProviderFileDeletionOutbox{}).
			Where("id = ? AND version = ? AND state = ? AND attempt_count = ?", outbox.Id, claim.ExpectedVersion, claim.ExpectedState, claim.ExpectedAttemptCount).
			Where("lease_expires_at IS NULL OR lease_expires_at < ?", now).
			Updates(map[string]interface{}{
				"state": ManagedProviderFileDeletionOutboxStateInProgress, "version": outbox.Version + 1, "attempt_count": nextAttemptCount,
				"lease_token_hmac": claim.LeaseTokenHMAC, "lease_expires_at": claim.LeaseExpiresAt.UTC(), "last_attempt_at": now.UTC(),
			})
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != 1 {
			return nil
		}
		lifecycleUpdates := map[string]interface{}{}
		if lifecycle.State == ManagedProviderFileLifecycleStateActive {
			lifecycleUpdates["state"] = ManagedProviderFileLifecycleStateDeletionPending
			lifecycleUpdates["deletion_started_at"] = now.UTC()
		}
		if err := appendEventAndAdvanceManagedProviderFileLifecycle(tx, &lifecycle, lifecycle.Version, lifecycleUpdates, &claim.Event); err != nil {
			return err
		}
		outbox.State = ManagedProviderFileDeletionOutboxStateInProgress
		outbox.Version++
		outbox.AttemptCount = nextAttemptCount
		outbox.LeaseTokenHMAC = claim.LeaseTokenHMAC
		outbox.LeaseExpiresAt = &claim.LeaseExpiresAt
		outbox.LastAttemptAt = &now
		claimed = true
		return nil
	})
	return &outbox, claimed, err
}

func RetryManagedProviderFileDeletion(ctx context.Context, retry ManagedProviderFileDeletionRetry) error {
	if retry.OutboxId <= 0 || retry.ExpectedVersion <= 0 || !validManagedProviderFileDigest(retry.LeaseTokenHMAC) || retry.AttemptCount <= 0 || !retry.NextAttemptAt.After(time.Now()) ||
		strings.TrimSpace(retry.ErrorCode) == "" || len(retry.ErrorCode) > 64 {
		return fmt.Errorf("managed provider file deletion retry is invalid")
	}
	return DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var outbox ManagedProviderFileDeletionOutbox
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&outbox, "id = ?", retry.OutboxId).Error; err != nil {
			return err
		}
		if outbox.State != ManagedProviderFileDeletionOutboxStateInProgress || outbox.Version != retry.ExpectedVersion || outbox.LeaseTokenHMAC != retry.LeaseTokenHMAC || outbox.AttemptCount != retry.AttemptCount ||
			outbox.LeaseExpiresAt == nil || outbox.LeaseExpiresAt.Before(time.Now()) {
			return ErrManagedProviderFileDeletionLeaseLost
		}
		if outbox.AttemptCount >= outbox.MaxAttempts {
			return ErrManagedProviderFileLifecycleStateConflict
		}
		if retry.Event.LifecycleId != outbox.LifecycleId || retry.Event.EventType != ManagedProviderFileLifecycleEventDeletionRetryScheduled ||
			retry.Event.AttemptCount != outbox.AttemptCount || retry.Event.ResultCode != retry.ErrorCode {
			return fmt.Errorf("managed provider file deletion retry event is invalid")
		}
		var lifecycle ManagedProviderFileLifecycle
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&lifecycle, "id = ?", outbox.LifecycleId).Error; err != nil {
			return err
		}
		result := tx.Model(&ManagedProviderFileDeletionOutbox{}).
			Where("id = ? AND version = ? AND state = ? AND lease_token_hmac = ? AND attempt_count = ?", outbox.Id, retry.ExpectedVersion, ManagedProviderFileDeletionOutboxStateInProgress, retry.LeaseTokenHMAC, retry.AttemptCount).
			Updates(map[string]interface{}{
				"state": ManagedProviderFileDeletionOutboxStateRetryWait, "version": outbox.Version + 1, "next_attempt_at": retry.NextAttemptAt.UTC(),
				"lease_token_hmac": "", "lease_expires_at": nil, "last_error_code": retry.ErrorCode,
			})
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != 1 {
			return ErrManagedProviderFileDeletionLeaseLost
		}
		return appendEventAndAdvanceManagedProviderFileLifecycle(tx, &lifecycle, lifecycle.Version, nil, &retry.Event)
	})
}

func CompleteManagedProviderFileDeletion(ctx context.Context, terminal ManagedProviderFileDeletionTerminal) error {
	if terminal.Result != ManagedProviderFileDeletionResultDeleted && terminal.Result != ManagedProviderFileDeletionResultNotFound {
		return fmt.Errorf("managed provider file deletion completion result is invalid")
	}
	return finishManagedProviderFileDeletion(ctx, terminal, ManagedProviderFileDeletionOutboxStateCompleted, ManagedProviderFileLifecycleStateDeleted, ManagedProviderFileLifecycleEventDeletionCompleted)
}

func FailManagedProviderFileDeletion(ctx context.Context, terminal ManagedProviderFileDeletionTerminal) error {
	if terminal.Result != ManagedProviderFileDeletionResultFailed || strings.TrimSpace(terminal.ErrorCode) == "" || len(terminal.ErrorCode) > 64 {
		return fmt.Errorf("managed provider file deletion failure is invalid")
	}
	return finishManagedProviderFileDeletion(ctx, terminal, ManagedProviderFileDeletionOutboxStateTerminalFailed, ManagedProviderFileLifecycleStateDeletionFailed, ManagedProviderFileLifecycleEventDeletionTerminalFailed)
}

func finishManagedProviderFileDeletion(ctx context.Context, terminal ManagedProviderFileDeletionTerminal, outboxState, lifecycleState, eventType string) error {
	if terminal.OutboxId <= 0 || terminal.ExpectedVersion <= 0 || !validManagedProviderFileDigest(terminal.LeaseTokenHMAC) || terminal.AttemptCount <= 0 || !validManagedProviderFileDigest(terminal.EvidenceDigest) {
		return fmt.Errorf("managed provider file deletion terminal transition is invalid")
	}
	now := time.Now()
	return DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var outbox ManagedProviderFileDeletionOutbox
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&outbox, "id = ?", terminal.OutboxId).Error; err != nil {
			return err
		}
		if outbox.State != ManagedProviderFileDeletionOutboxStateInProgress || outbox.Version != terminal.ExpectedVersion || outbox.LeaseTokenHMAC != terminal.LeaseTokenHMAC || outbox.AttemptCount != terminal.AttemptCount ||
			outbox.LeaseExpiresAt == nil || outbox.LeaseExpiresAt.Before(now) {
			return ErrManagedProviderFileDeletionLeaseLost
		}
		if terminal.Event.LifecycleId != outbox.LifecycleId || terminal.Event.EventType != eventType || terminal.Event.AttemptCount != outbox.AttemptCount ||
			terminal.Event.EvidenceDigest != terminal.EvidenceDigest {
			return fmt.Errorf("managed provider file deletion terminal event is invalid")
		}
		if lifecycleState == ManagedProviderFileLifecycleStateDeleted && terminal.Event.ResultCode != terminal.Result {
			return fmt.Errorf("managed provider file deletion completion event is invalid")
		}
		if lifecycleState == ManagedProviderFileLifecycleStateDeletionFailed && terminal.Event.ResultCode != terminal.ErrorCode {
			return fmt.Errorf("managed provider file deletion failure event is invalid")
		}
		var lifecycle ManagedProviderFileLifecycle
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&lifecycle, "id = ?", outbox.LifecycleId).Error; err != nil {
			return err
		}
		outboxResult := tx.Model(&ManagedProviderFileDeletionOutbox{}).
			Where("id = ? AND version = ? AND state = ? AND lease_token_hmac = ? AND attempt_count = ?", outbox.Id, terminal.ExpectedVersion, ManagedProviderFileDeletionOutboxStateInProgress, terminal.LeaseTokenHMAC, terminal.AttemptCount).
			Updates(map[string]interface{}{
				"state": outboxState, "version": outbox.Version + 1, "lease_token_hmac": "", "lease_expires_at": nil, "last_error_code": terminal.ErrorCode,
				"terminal_result": terminal.Result, "completed_at": now,
			})
		if outboxResult.Error != nil {
			return outboxResult.Error
		}
		if outboxResult.RowsAffected != 1 {
			return ErrManagedProviderFileDeletionLeaseLost
		}
		lifecycleUpdates := map[string]interface{}{"state": lifecycleState}
		if lifecycleState == ManagedProviderFileLifecycleStateDeleted {
			lifecycleUpdates["deleted_at"] = now
			lifecycleUpdates["provider_payload"] = []byte(nil)
		} else {
			lifecycleUpdates["deletion_failed_at"] = now
			lifecycleUpdates["terminal_reason_code"] = terminal.ErrorCode
		}
		return appendEventAndAdvanceManagedProviderFileLifecycle(tx, &lifecycle, lifecycle.Version, lifecycleUpdates, &terminal.Event)
	})
}

func appendEventAndAdvanceManagedProviderFileLifecycle(tx *gorm.DB, lifecycle *ManagedProviderFileLifecycle, expectedVersion int64, updates map[string]interface{}, event *ManagedProviderFileLifecycleEvent) error {
	if lifecycle == nil || event == nil || lifecycle.Version != expectedVersion || expectedVersion <= 0 {
		return ErrManagedProviderFileLifecycleStateConflict
	}
	if event.LifecycleId != lifecycle.Id || event.Sequence != lifecycle.LastEventSequence+1 || event.PreviousEventHMAC != lifecycle.LastEventHMAC || event.FromState != lifecycle.State {
		return ErrManagedProviderFileLifecycleStateConflict
	}
	if err := appendManagedProviderFileLifecycleEvent(tx, event); err != nil {
		return err
	}
	if updates == nil {
		updates = make(map[string]interface{})
	}
	updates["version"] = expectedVersion + 1
	updates["last_event_sequence"] = event.Sequence
	updates["last_event_hmac"] = event.EventHMAC
	result := tx.Model(&ManagedProviderFileLifecycle{}).
		Where("id = ? AND version = ? AND state = ? AND last_event_sequence = ? AND last_event_hmac = ?", lifecycle.Id, expectedVersion, lifecycle.State, lifecycle.LastEventSequence, lifecycle.LastEventHMAC).
		Updates(updates)
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected != 1 {
		return ErrManagedProviderFileLifecycleStateConflict
	}
	return nil
}

func appendManagedProviderFileLifecycleEvent(tx *gorm.DB, event *ManagedProviderFileLifecycleEvent) error {
	if event == nil {
		return fmt.Errorf("managed provider file lifecycle event is required")
	}
	var latest ManagedProviderFileLifecycleEvent
	err := tx.Where("lifecycle_id = ?", event.LifecycleId).Order("sequence desc").First(&latest).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		if event.Sequence != 1 || event.PreviousEventHMAC != "" {
			return ErrManagedProviderFileLifecycleStateConflict
		}
	} else if err != nil {
		return err
	} else if event.Sequence != latest.Sequence+1 || event.FromState != latest.ToState || event.PreviousEventHMAC != latest.EventHMAC {
		return ErrManagedProviderFileLifecycleStateConflict
	}
	if err := event.Validate(); err != nil {
		return err
	}
	return tx.Create(event).Error
}

func sameManagedProviderFileIntent(existing, requested ManagedProviderFileLifecycle) bool {
	return existing.UploadIntentHMAC == requested.UploadIntentHMAC && existing.HandleLookupHMAC == requested.HandleLookupHMAC && existing.OwnerHMAC == requested.OwnerHMAC &&
		existing.LookupKeyVersion == requested.LookupKeyVersion && existing.RequestFingerprint == requested.RequestFingerprint &&
		existing.Provider == requested.Provider && existing.ChannelId == requested.ChannelId && existing.ChannelType == requested.ChannelType &&
		existing.ChannelIsMultiKey == requested.ChannelIsMultiKey && existing.ChannelMultiKeyIndex == requested.ChannelMultiKeyIndex &&
		existing.CredentialFingerprint == requested.CredentialFingerprint && existing.CredentialFingerprintKeyVersion == requested.CredentialFingerprintKeyVersion &&
		existing.EndpointFingerprint == requested.EndpointFingerprint && existing.ProviderScopeFingerprint == requested.ProviderScopeFingerprint &&
		existing.PayloadKeyVersion == requested.PayloadKeyVersion && existing.Purpose == requested.Purpose
}

func sameManagedProviderFileActivation(lifecycle ManagedProviderFileLifecycle, activation ManagedProviderFileLifecycleActivation) bool {
	return lifecycle.ProviderLookupHMAC != nil && *lifecycle.ProviderLookupHMAC == activation.ProviderLookupHMAC &&
		string(lifecycle.ProviderPayload) == string(activation.ProviderPayload) && lifecycle.ProviderBytes == activation.ProviderBytes &&
		lifecycle.ProviderCreatedAt != nil && lifecycle.ProviderCreatedAt.Equal(activation.ProviderCreatedAt) &&
		lifecycle.MetadataVerifiedAt != nil && lifecycle.MetadataVerifiedAt.Equal(activation.MetadataVerifiedAt) &&
		lifecycle.ExpiresAt != nil && lifecycle.ExpiresAt.Equal(activation.ExpiresAt)
}

func validManagedProviderFileDigest(value string) bool {
	if len(value) != 64 || value != strings.ToLower(value) {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil
}

func validManagedProviderFileKeyVersion(value string) bool {
	return strings.TrimSpace(value) != "" && value == strings.TrimSpace(value) && len(value) <= 64
}
