package contextconsensus

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/relaykit/types"
)

const ManagedConsensusStateVersion = 1

var (
	ErrManagedConsensusNotFound         = errors.New("managed consensus state not found")
	ErrManagedConsensusRevisionConflict = errors.New("managed consensus revision conflict")
	ErrManagedConsensusLeaseHeld        = errors.New("managed consensus lease is already held")
	ErrManagedConsensusLeaseInvalid     = errors.New("managed consensus lease is invalid or expired")
	ErrProviderStateBindingNotFound     = errors.New("provider state binding not found")
	ErrProviderStateBindingConflict     = errors.New("provider state binding conflict")
)

type ManagedConsensusState struct {
	Version         int                           `json:"version"`
	Revision        uint64                        `json:"revision"`
	Mode            string                        `json:"mode"`
	TaskConsensus   ConsensusSummary              `json:"task_consensus"`
	ProviderBinding *ManagedProviderTargetBinding `json:"provider_binding,omitempty"`
	SourceDigest    string                        `json:"source_digest"`
	PolicyVersion   string                        `json:"policy_version"`
	CreatedAtUnix   int64                         `json:"created_at_unix"`
	UpdatedAtUnix   int64                         `json:"updated_at_unix"`
}

func (state ManagedConsensusState) Validate() error {
	if state.Version != ManagedConsensusStateVersion {
		return fmt.Errorf("managed consensus state version must be %d", ManagedConsensusStateVersion)
	}
	if state.Revision == 0 {
		return fmt.Errorf("managed consensus state revision must be positive")
	}
	if state.Mode != "managed_consensus" {
		return fmt.Errorf("managed consensus state mode must be managed_consensus")
	}
	if state.TaskConsensus.Version != ConsensusSummaryVersion {
		return fmt.Errorf("managed task consensus version must be %d", ConsensusSummaryVersion)
	}
	if strings.TrimSpace(state.SourceDigest) == "" {
		return fmt.Errorf("managed consensus source digest is required")
	}
	if strings.TrimSpace(state.PolicyVersion) == "" {
		return fmt.Errorf("managed consensus policy version is required")
	}
	if state.CreatedAtUnix <= 0 {
		return fmt.Errorf("managed consensus creation time is required")
	}
	if state.UpdatedAtUnix < state.CreatedAtUnix {
		return fmt.Errorf("managed consensus update time must not precede creation")
	}
	if state.ProviderBinding != nil {
		if err := state.ProviderBinding.Validate(); err != nil {
			return err
		}
	}
	return nil
}

type ManagedProviderTargetBinding struct {
	BindingLevel          BindingLevel      `json:"binding_level"`
	RelayFormat           types.RelayFormat `json:"relay_format"`
	ChannelID             int               `json:"channel_id"`
	ChannelType           int               `json:"channel_type"`
	UpstreamModel         string            `json:"upstream_model"`
	MultiKeyIndex         int               `json:"multi_key_index"`
	CredentialFingerprint string            `json:"credential_fingerprint"`
	ReasonCodes           []string          `json:"reason_codes"`
}

func (binding ManagedProviderTargetBinding) Validate() error {
	if binding.BindingLevel == "" || binding.BindingLevel == BindingLevelNone {
		return fmt.Errorf("provider state binding level is required")
	}
	if binding.RelayFormat == "" {
		return fmt.Errorf("provider state relay format is required")
	}
	if binding.ChannelID <= 0 {
		return fmt.Errorf("provider state channel ID must be positive")
	}
	if binding.ChannelType <= 0 {
		return fmt.Errorf("provider state channel type must be positive")
	}
	if strings.TrimSpace(binding.UpstreamModel) == "" {
		return fmt.Errorf("provider state upstream model is required")
	}
	if binding.MultiKeyIndex < 0 {
		return fmt.Errorf("provider state multi-key index must not be negative")
	}
	if binding.BindingLevel == BindingLevelCredential && strings.TrimSpace(binding.CredentialFingerprint) == "" {
		return fmt.Errorf("provider state credential fingerprint is required")
	}
	if len(binding.ReasonCodes) == 0 {
		return fmt.Errorf("provider state binding reason codes are required")
	}
	for _, reasonCode := range binding.ReasonCodes {
		if strings.TrimSpace(reasonCode) == "" {
			return fmt.Errorf("provider state binding contains an empty reason code")
		}
	}
	return nil
}

type ManagedProviderStateBinding struct {
	Version            int                          `json:"version"`
	OwnerHMAC          string                       `json:"owner_hmac"`
	StateReferenceHMAC string                       `json:"state_reference_hmac"`
	Target             ManagedProviderTargetBinding `json:"target"`
	CreatedAtUnix      int64                        `json:"created_at_unix"`
	ExpiresAtUnix      int64                        `json:"expires_at_unix"`
}

func (binding ManagedProviderStateBinding) Validate() error {
	if binding.Version != ManagedConsensusStateVersion {
		return fmt.Errorf("provider state binding version must be %d", ManagedConsensusStateVersion)
	}
	if strings.TrimSpace(binding.OwnerHMAC) == "" {
		return fmt.Errorf("provider state binding owner HMAC is required")
	}
	if strings.TrimSpace(binding.StateReferenceHMAC) == "" {
		return fmt.Errorf("provider state reference HMAC is required")
	}
	if err := binding.Target.Validate(); err != nil {
		return err
	}
	if binding.CreatedAtUnix <= 0 {
		return fmt.Errorf("provider state binding creation time is required")
	}
	if binding.ExpiresAtUnix <= binding.CreatedAtUnix {
		return fmt.Errorf("provider state binding expiration must be after creation")
	}
	return nil
}

type ManagedConsensusRecord struct {
	Revision     uint64
	FencingToken uint64
	Payload      ManagedEncryptedEnvelope
	ExpiresAt    time.Time
}

type ManagedConsensusLease struct {
	RepositoryKey string
	HolderID      string
	FencingToken  uint64
	ExpiresAt     time.Time
}

type ManagedProviderStateRecord struct {
	BindingDigest string
	Payload       ManagedEncryptedEnvelope
	ExpiresAt     time.Time
}

// ManagedConsensusRepository is the multi-instance persistence contract.
// Implementations must execute lease and CAS operations atomically and must
// keep fencing tokens monotonically increasing beyond any active lease.
type ManagedConsensusRepository interface {
	LoadConsensus(ctx context.Context, key ManagedConversationStorageKey) (ManagedConsensusRecord, error)
	AcquireConsensusLease(ctx context.Context, key ManagedConversationStorageKey, holderID string, ttl time.Duration) (ManagedConsensusLease, error)
	RenewConsensusLease(ctx context.Context, lease ManagedConsensusLease, ttl time.Duration) (ManagedConsensusLease, error)
	ReleaseConsensusLease(ctx context.Context, lease ManagedConsensusLease) error
	CompareAndSwapConsensus(ctx context.Context, key ManagedConversationStorageKey, expectedRevision uint64, lease ManagedConsensusLease, payload ManagedEncryptedEnvelope, ttl time.Duration) (ManagedConsensusRecord, error)
	DeleteConsensus(ctx context.Context, key ManagedConversationStorageKey, expectedRevision uint64, lease ManagedConsensusLease) error

	LoadProviderStateBinding(ctx context.Context, key ManagedProviderStateStorageKey) (ManagedProviderStateRecord, error)
	RegisterProviderStateBinding(ctx context.Context, key ManagedProviderStateStorageKey, bindingDigest string, payload ManagedEncryptedEnvelope, ttl time.Duration) (ManagedProviderStateRecord, error)
	DeleteProviderStateBinding(ctx context.Context, key ManagedProviderStateStorageKey, expectedBindingDigest string) error
}
