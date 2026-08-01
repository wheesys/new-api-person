package contextconsensus

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
)

const (
	managedConsensusRepositoryKeyPrefix  = "new-api:context_consensus:v1"
	managedProviderStateRepositoryPrefix = "new-api:context_consensus:provider_state:v1"
	managedOutcomeRepositoryKeyPrefix    = "new-api:context_consensus:outcome:v1"
	managedProviderFileRepositoryPrefix  = "new-api:context_consensus:provider_file:v1"
)

type ManagedConsensusOwner struct {
	UserID         int
	TokenID        int
	EndpointFamily string
}

type ManagedConversationStorageKey struct {
	RepositoryKey    string
	OwnerHMAC        string
	ConversationHMAC string
	KeyVersion       string
}

type ManagedProviderStateStorageKey struct {
	RepositoryKey      string
	OwnerHMAC          string
	StateReferenceHMAC string
	KeyVersion         string
}

type ManagedOutcomeStorageKey struct {
	RepositoryKey      string
	LookupHMAC         string
	RevisionIntentHMAC string
	OwnerHMAC          string
	ConversationHMAC   string
	KeyVersion         string
	BillingLookupHMAC  string
}

type ManagedProviderFileStorageKey struct {
	RepositoryKey    string
	OwnerHMAC        string
	HandleHMAC       string
	UploadIntentHMAC string
	KeyVersion       string
}

type ManagedProviderFileUploadIntentKey struct {
	RepositoryKey    string
	OwnerHMAC        string
	UploadIntentHMAC string
	KeyVersion       string
}

type ManagedProviderFileHandleKey struct {
	RepositoryKey string
	OwnerHMAC     string
	HandleHMAC    string
	KeyVersion    string
}

type ManagedProviderFileTargetIdentity struct {
	ChannelID         int
	ChannelType       int
	MultiKeyIndex     int
	ChannelIsMultiKey bool
	Endpoint          string
	Organization      string
	Project           string
}

type ManagedProviderFileUploadFingerprintIdentity struct {
	OwnerHMAC         string
	ContentDigest     string
	TargetFingerprint string
	Purpose           string
	ExpirationSeconds int
}

type ManagedProviderFileEventIdentity struct {
	LifecycleHMAC     string
	Sequence          int64
	PreviousEventHMAC string
	EventType         string
	FromState         string
	ToState           string
	AttemptCount      int
	ResultCode        string
	EvidenceDigest    string
	CreatedAtUnix     int64
}

type ManagedProviderFileReadinessEvidenceIdentity struct {
	TargetFingerprint          string
	ScopeFingerprint           string
	CredentialFingerprint      string
	ProjectEvidenceHMAC        string
	SandboxEvidenceHMAC        string
	ImmutableAuditEvidenceHMAC string
	DatabaseMatrixEvidenceHMAC string
	ProjectAttestationVersion  string
	SandboxContractVersion     string
	ImmutableAuditVersion      string
	DatabaseMatrixVersion      string
	AttestedAtUnix             int64
	ExpiresAtUnix              int64
}

type ManagedConsensusKeyDeriver struct {
	key        []byte
	keyVersion string
}

func (deriver *ManagedConsensusKeyDeriver) KeyVersion() string {
	if deriver == nil {
		return ""
	}
	return deriver.keyVersion
}

func NewManagedConsensusKeyDeriver(key []byte, keyVersion string) (*ManagedConsensusKeyDeriver, error) {
	if len(key) < 32 {
		return nil, fmt.Errorf("managed consensus HMAC key must contain at least 32 bytes")
	}
	if strings.TrimSpace(keyVersion) == "" {
		return nil, fmt.Errorf("managed consensus HMAC key version is required")
	}
	return &ManagedConsensusKeyDeriver{
		key:        append([]byte(nil), key...),
		keyVersion: strings.TrimSpace(keyVersion),
	}, nil
}

func (deriver *ManagedConsensusKeyDeriver) DeriveConversationStorageKey(owner ManagedConsensusOwner, externalContextID string) (ManagedConversationStorageKey, error) {
	ownerHMAC, err := deriver.deriveOwnerHMAC(owner)
	if err != nil {
		return ManagedConversationStorageKey{}, err
	}
	if strings.TrimSpace(externalContextID) == "" {
		return ManagedConversationStorageKey{}, fmt.Errorf("external context ID is required")
	}
	conversationHMAC, err := deriver.calculateHMAC("conversation", struct {
		OwnerHMAC         string `json:"owner_hmac"`
		ExternalContextID string `json:"external_context_id"`
	}{
		OwnerHMAC:         ownerHMAC,
		ExternalContextID: externalContextID,
	})
	if err != nil {
		return ManagedConversationStorageKey{}, err
	}
	return ManagedConversationStorageKey{
		RepositoryKey:    managedConsensusRepositoryKeyPrefix + ":" + ownerHMAC + ":" + conversationHMAC,
		OwnerHMAC:        ownerHMAC,
		ConversationHMAC: conversationHMAC,
		KeyVersion:       deriver.keyVersion,
	}, nil
}

func (deriver *ManagedConsensusKeyDeriver) DeriveProviderStateStorageKey(owner ManagedConsensusOwner, providerStateReference string) (ManagedProviderStateStorageKey, error) {
	ownerHMAC, err := deriver.deriveOwnerHMAC(owner)
	if err != nil {
		return ManagedProviderStateStorageKey{}, err
	}
	if strings.TrimSpace(providerStateReference) == "" {
		return ManagedProviderStateStorageKey{}, fmt.Errorf("provider state reference is required")
	}
	stateReferenceHMAC, err := deriver.calculateHMAC("provider_state", struct {
		OwnerHMAC              string `json:"owner_hmac"`
		ProviderStateReference string `json:"provider_state_reference"`
	}{
		OwnerHMAC:              ownerHMAC,
		ProviderStateReference: providerStateReference,
	})
	if err != nil {
		return ManagedProviderStateStorageKey{}, err
	}
	return ManagedProviderStateStorageKey{
		RepositoryKey:      managedProviderStateRepositoryPrefix + ":" + ownerHMAC + ":" + stateReferenceHMAC,
		OwnerHMAC:          ownerHMAC,
		StateReferenceHMAC: stateReferenceHMAC,
		KeyVersion:         deriver.keyVersion,
	}, nil
}

func (deriver *ManagedConsensusKeyDeriver) DeriveOutcomeStorageKey(owner ManagedConsensusOwner, externalContextID, idempotencyKey string, expectedRevision uint64) (ManagedOutcomeStorageKey, error) {
	conversationKey, err := deriver.DeriveConversationStorageKey(owner, externalContextID)
	if err != nil {
		return ManagedOutcomeStorageKey{}, err
	}
	if strings.TrimSpace(idempotencyKey) == "" {
		return ManagedOutcomeStorageKey{}, fmt.Errorf("managed context idempotency key is required")
	}
	lookupHMAC, err := deriver.calculateHMAC("idempotency_key", struct {
		OwnerHMAC      string `json:"owner_hmac"`
		IdempotencyKey string `json:"idempotency_key"`
	}{OwnerHMAC: conversationKey.OwnerHMAC, IdempotencyKey: idempotencyKey})
	if err != nil {
		return ManagedOutcomeStorageKey{}, err
	}
	revisionIntentHMAC, err := deriver.calculateHMAC("revision_intent", struct {
		OwnerHMAC        string `json:"owner_hmac"`
		ConversationHMAC string `json:"conversation_hmac"`
		ExpectedRevision uint64 `json:"expected_revision"`
	}{OwnerHMAC: conversationKey.OwnerHMAC, ConversationHMAC: conversationKey.ConversationHMAC, ExpectedRevision: expectedRevision})
	if err != nil {
		return ManagedOutcomeStorageKey{}, err
	}
	return ManagedOutcomeStorageKey{
		RepositoryKey: managedOutcomeRepositoryKeyPrefix + ":" + conversationKey.OwnerHMAC + ":" + lookupHMAC,
		LookupHMAC:    lookupHMAC, RevisionIntentHMAC: revisionIntentHMAC,
		OwnerHMAC: conversationKey.OwnerHMAC, ConversationHMAC: conversationKey.ConversationHMAC,
		KeyVersion:        conversationKey.KeyVersion,
		BillingLookupHMAC: digestBytes([]byte(conversationKey.RepositoryKey)),
	}, nil
}

func (deriver *ManagedConsensusKeyDeriver) DeriveCredentialFingerprint(channelID int, multiKeyIndex int, credential string) (string, error) {
	if channelID <= 0 {
		return "", fmt.Errorf("credential fingerprint channel ID must be positive")
	}
	if multiKeyIndex < 0 {
		return "", fmt.Errorf("credential fingerprint multi-key index must not be negative")
	}
	if credential == "" {
		return "", fmt.Errorf("credential fingerprint credential is required")
	}
	return deriver.calculateHMAC("credential", struct {
		ChannelID     int    `json:"channel_id"`
		MultiKeyIndex int    `json:"multi_key_index"`
		Credential    string `json:"credential"`
	}{
		ChannelID:     channelID,
		MultiKeyIndex: multiKeyIndex,
		Credential:    credential,
	})
}

func (deriver *ManagedConsensusKeyDeriver) DeriveProviderFileStorageKey(owner ManagedConsensusOwner, handle, idempotencyKey string) (ManagedProviderFileStorageKey, error) {
	handleKey, err := deriver.DeriveProviderFileHandleKey(owner, handle)
	if err != nil {
		return ManagedProviderFileStorageKey{}, err
	}
	uploadIntentKey, err := deriver.DeriveProviderFileUploadIntentKey(owner, idempotencyKey)
	if err != nil {
		return ManagedProviderFileStorageKey{}, err
	}
	return ManagedProviderFileStorageKey{
		RepositoryKey: handleKey.RepositoryKey,
		OwnerHMAC:     handleKey.OwnerHMAC, HandleHMAC: handleKey.HandleHMAC, UploadIntentHMAC: uploadIntentKey.UploadIntentHMAC,
		KeyVersion: handleKey.KeyVersion,
	}, nil
}

func (deriver *ManagedConsensusKeyDeriver) DeriveProviderFileUploadIntentKey(owner ManagedConsensusOwner, idempotencyKey string) (ManagedProviderFileUploadIntentKey, error) {
	ownerHMAC, err := deriver.deriveProviderFileOwnerHMAC(owner)
	if err != nil {
		return ManagedProviderFileUploadIntentKey{}, err
	}
	if strings.TrimSpace(idempotencyKey) == "" || strings.TrimSpace(idempotencyKey) != idempotencyKey || len(idempotencyKey) > 256 {
		return ManagedProviderFileUploadIntentKey{}, fmt.Errorf("managed provider file idempotency key is invalid")
	}
	uploadIntentHMAC, err := deriver.calculateHexHMAC("provider_file_upload_intent", struct {
		OwnerHMAC      string `json:"owner_hmac"`
		IdempotencyKey string `json:"idempotency_key"`
	}{OwnerHMAC: ownerHMAC, IdempotencyKey: idempotencyKey})
	if err != nil {
		return ManagedProviderFileUploadIntentKey{}, err
	}
	return ManagedProviderFileUploadIntentKey{
		RepositoryKey: managedProviderFileRepositoryKey(ownerHMAC, uploadIntentHMAC),
		OwnerHMAC:     ownerHMAC, UploadIntentHMAC: uploadIntentHMAC, KeyVersion: deriver.keyVersion,
	}, nil
}

func ManagedProviderFileRepositoryKey(ownerHMAC, uploadIntentHMAC string) (string, error) {
	if !validProviderFileHexHMAC(ownerHMAC) || !validProviderFileHexHMAC(uploadIntentHMAC) {
		return "", fmt.Errorf("managed provider file repository identity is invalid")
	}
	return managedProviderFileRepositoryKey(ownerHMAC, uploadIntentHMAC), nil
}

func managedProviderFileRepositoryKey(ownerHMAC, uploadIntentHMAC string) string {
	return managedProviderFileRepositoryPrefix + ":" + ownerHMAC + ":intent:" + uploadIntentHMAC
}

func (deriver *ManagedConsensusKeyDeriver) DeriveProviderFileHandleKey(owner ManagedConsensusOwner, handle string) (ManagedProviderFileHandleKey, error) {
	ownerHMAC, err := deriver.deriveProviderFileOwnerHMAC(owner)
	if err != nil {
		return ManagedProviderFileHandleKey{}, err
	}
	if strings.TrimSpace(handle) == "" || strings.TrimSpace(handle) != handle || len(handle) > 256 {
		return ManagedProviderFileHandleKey{}, fmt.Errorf("managed provider file handle is invalid")
	}
	handleHMAC, err := deriver.calculateHexHMAC("provider_file_handle", struct {
		OwnerHMAC string `json:"owner_hmac"`
		Handle    string `json:"handle"`
	}{OwnerHMAC: ownerHMAC, Handle: handle})
	if err != nil {
		return ManagedProviderFileHandleKey{}, err
	}
	return ManagedProviderFileHandleKey{
		RepositoryKey: managedProviderFileRepositoryPrefix + ":" + ownerHMAC + ":" + handleHMAC,
		OwnerHMAC:     ownerHMAC, HandleHMAC: handleHMAC, KeyVersion: deriver.keyVersion,
	}, nil
}

func (deriver *ManagedConsensusKeyDeriver) DeriveProviderFileReferenceHMAC(owner ManagedConsensusOwner, providerReference string) (string, error) {
	ownerHMAC, err := deriver.deriveProviderFileOwnerHMAC(owner)
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(providerReference) == "" || strings.TrimSpace(providerReference) != providerReference || len(providerReference) > 512 {
		return "", fmt.Errorf("managed provider file reference is invalid")
	}
	return deriver.calculateHexHMAC("provider_file_reference", struct {
		OwnerHMAC         string `json:"owner_hmac"`
		ProviderReference string `json:"provider_reference"`
	}{OwnerHMAC: ownerHMAC, ProviderReference: providerReference})
}

func (deriver *ManagedConsensusKeyDeriver) DeriveProviderFileTargetReferenceHMAC(targetFingerprint, providerReference string) (string, error) {
	if !validProviderFileHexHMAC(targetFingerprint) || strings.TrimSpace(providerReference) == "" || strings.TrimSpace(providerReference) != providerReference || len(providerReference) > 512 {
		return "", fmt.Errorf("managed provider file target reference identity is invalid")
	}
	return deriver.calculateHexHMAC("provider_file_target_reference", struct {
		TargetFingerprint string `json:"target_fingerprint"`
		ProviderReference string `json:"provider_reference"`
	}{TargetFingerprint: targetFingerprint, ProviderReference: providerReference})
}

func (deriver *ManagedConsensusKeyDeriver) DeriveProviderFileReconciliationMetadataHMAC(targetLookupHMAC, filename string, bytes, createdAtUnix, expiresAtUnix int64) (string, error) {
	if !validProviderFileHexHMAC(targetLookupHMAC) || strings.TrimSpace(filename) == "" || strings.TrimSpace(filename) != filename || len(filename) > 255 ||
		bytes < 0 || createdAtUnix <= 0 || (expiresAtUnix != 0 && expiresAtUnix <= createdAtUnix) {
		return "", fmt.Errorf("managed provider file reconciliation metadata is invalid")
	}
	return deriver.calculateHexHMAC("provider_file_reconciliation_metadata", struct {
		TargetLookupHMAC string `json:"target_lookup_hmac"`
		Filename         string `json:"filename"`
		Bytes            int64  `json:"bytes"`
		CreatedAtUnix    int64  `json:"created_at_unix"`
		ExpiresAtUnix    int64  `json:"expires_at_unix"`
	}{TargetLookupHMAC: targetLookupHMAC, Filename: filename, Bytes: bytes, CreatedAtUnix: createdAtUnix, ExpiresAtUnix: expiresAtUnix})
}

func (deriver *ManagedConsensusKeyDeriver) DeriveProviderFileReconciliationCursorHMAC(targetFingerprint, cursor string) (string, error) {
	if !validProviderFileHexHMAC(targetFingerprint) || strings.TrimSpace(cursor) == "" || strings.TrimSpace(cursor) != cursor || len(cursor) > 512 {
		return "", fmt.Errorf("managed provider file reconciliation cursor is invalid")
	}
	return deriver.calculateHexHMAC("provider_file_reconciliation_cursor", struct {
		TargetFingerprint string `json:"target_fingerprint"`
		Cursor            string `json:"cursor"`
	}{TargetFingerprint: targetFingerprint, Cursor: cursor})
}

func (deriver *ManagedConsensusKeyDeriver) DeriveProviderFileDeletionOperationHMAC(owner ManagedConsensusOwner, handle string) (string, error) {
	ownerHMAC, err := deriver.deriveProviderFileOwnerHMAC(owner)
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(handle) == "" || strings.TrimSpace(handle) != handle || len(handle) > 256 {
		return "", fmt.Errorf("managed provider file handle is invalid")
	}
	return deriver.calculateHexHMAC("provider_file_deletion", struct {
		OwnerHMAC string `json:"owner_hmac"`
		Handle    string `json:"handle"`
	}{OwnerHMAC: ownerHMAC, Handle: handle})
}

func (deriver *ManagedConsensusKeyDeriver) DeriveProviderFileDeletionLeaseHMAC(outboxID int64, nonce string) (string, error) {
	if outboxID <= 0 || strings.TrimSpace(nonce) == "" || strings.TrimSpace(nonce) != nonce || len(nonce) > 128 {
		return "", fmt.Errorf("managed provider file deletion lease identity is invalid")
	}
	return deriver.calculateHexHMAC("provider_file_deletion_lease", struct {
		OutboxID int64  `json:"outbox_id"`
		Nonce    string `json:"nonce"`
	}{OutboxID: outboxID, Nonce: nonce})
}

func (deriver *ManagedConsensusKeyDeriver) DeriveProviderFileDeletionEvidenceHMAC(operationHMAC string, attemptCount int, resultCode string) (string, error) {
	if !validProviderFileHexHMAC(operationHMAC) || attemptCount <= 0 || strings.TrimSpace(resultCode) == "" ||
		strings.TrimSpace(resultCode) != resultCode || len(resultCode) > 64 {
		return "", fmt.Errorf("managed provider file deletion evidence identity is invalid")
	}
	return deriver.calculateHexHMAC("provider_file_deletion_evidence", struct {
		OperationHMAC string `json:"operation_hmac"`
		AttemptCount  int    `json:"attempt_count"`
		ResultCode    string `json:"result_code"`
	}{OperationHMAC: operationHMAC, AttemptCount: attemptCount, ResultCode: resultCode})
}

func (deriver *ManagedConsensusKeyDeriver) DeriveProviderFileReadinessEvidenceHMAC(identity ManagedProviderFileReadinessEvidenceIdentity) (string, error) {
	if !validProviderFileHexHMAC(identity.TargetFingerprint) || !validProviderFileHexHMAC(identity.ScopeFingerprint) || !validProviderFileHexHMAC(identity.CredentialFingerprint) ||
		!validProviderFileHexHMAC(identity.ProjectEvidenceHMAC) || !validProviderFileHexHMAC(identity.SandboxEvidenceHMAC) ||
		!validProviderFileHexHMAC(identity.ImmutableAuditEvidenceHMAC) || !validProviderFileHexHMAC(identity.DatabaseMatrixEvidenceHMAC) ||
		strings.TrimSpace(identity.ProjectAttestationVersion) == "" || strings.TrimSpace(identity.ProjectAttestationVersion) != identity.ProjectAttestationVersion ||
		strings.TrimSpace(identity.SandboxContractVersion) == "" || strings.TrimSpace(identity.SandboxContractVersion) != identity.SandboxContractVersion ||
		strings.TrimSpace(identity.ImmutableAuditVersion) == "" || strings.TrimSpace(identity.ImmutableAuditVersion) != identity.ImmutableAuditVersion ||
		strings.TrimSpace(identity.DatabaseMatrixVersion) == "" || strings.TrimSpace(identity.DatabaseMatrixVersion) != identity.DatabaseMatrixVersion ||
		len(identity.ProjectAttestationVersion) > 64 || len(identity.SandboxContractVersion) > 64 || len(identity.ImmutableAuditVersion) > 64 || len(identity.DatabaseMatrixVersion) > 64 ||
		identity.AttestedAtUnix <= 0 || identity.ExpiresAtUnix <= identity.AttestedAtUnix || identity.ExpiresAtUnix-identity.AttestedAtUnix > int64((24*time.Hour).Seconds()) {
		return "", fmt.Errorf("managed provider file readiness evidence identity is invalid")
	}
	return deriver.calculateHexHMAC("provider_file_readiness_evidence", identity)
}

func (deriver *ManagedConsensusKeyDeriver) DeriveProviderFileTargetFingerprint(identity ManagedProviderFileTargetIdentity) (string, error) {
	if identity.ChannelID <= 0 || identity.ChannelType <= 0 || identity.MultiKeyIndex < 0 ||
		(!identity.ChannelIsMultiKey && identity.MultiKeyIndex != 0) || strings.TrimSpace(identity.Endpoint) == "" ||
		strings.TrimSpace(identity.Endpoint) != identity.Endpoint || len(identity.Endpoint) > 512 ||
		len(identity.Organization) > 256 || len(identity.Project) > 256 || strings.TrimSpace(identity.Organization) != identity.Organization || strings.TrimSpace(identity.Project) != identity.Project {
		return "", fmt.Errorf("managed provider file target identity is invalid")
	}
	return deriver.calculateHexHMAC("provider_file_target", struct {
		ChannelID         int    `json:"channel_id"`
		ChannelType       int    `json:"channel_type"`
		MultiKeyIndex     int    `json:"multi_key_index"`
		ChannelIsMultiKey bool   `json:"channel_is_multi_key"`
		Endpoint          string `json:"endpoint"`
		Organization      string `json:"organization"`
		Project           string `json:"project"`
	}{
		ChannelID: identity.ChannelID, ChannelType: identity.ChannelType, MultiKeyIndex: identity.MultiKeyIndex,
		ChannelIsMultiKey: identity.ChannelIsMultiKey, Endpoint: identity.Endpoint,
		Organization: identity.Organization, Project: identity.Project,
	})
}

func (deriver *ManagedConsensusKeyDeriver) DeriveProviderFileEndpointFingerprint(endpoint string) (string, error) {
	if strings.TrimSpace(endpoint) == "" || strings.TrimSpace(endpoint) != endpoint || len(endpoint) > 512 {
		return "", fmt.Errorf("managed provider file endpoint is invalid")
	}
	return deriver.calculateHexHMAC("provider_file_endpoint", struct {
		Endpoint string `json:"endpoint"`
	}{Endpoint: endpoint})
}

func (deriver *ManagedConsensusKeyDeriver) DeriveProviderFileScopeFingerprint(organization, project string) (string, error) {
	if len(organization) > 256 || len(project) > 256 || strings.TrimSpace(organization) != organization || strings.TrimSpace(project) != project {
		return "", fmt.Errorf("managed provider file scope is invalid")
	}
	return deriver.calculateHexHMAC("provider_file_scope", struct {
		Organization string `json:"organization"`
		Project      string `json:"project"`
	}{Organization: organization, Project: project})
}

func (deriver *ManagedConsensusKeyDeriver) DeriveProviderFileUploadFingerprint(identity ManagedProviderFileUploadFingerprintIdentity) (string, error) {
	if !validProviderFileHexHMAC(identity.OwnerHMAC) || !validProviderFileHexHMAC(identity.ContentDigest) ||
		!validProviderFileHexHMAC(identity.TargetFingerprint) || identity.Purpose != "user_data" ||
		identity.ExpirationSeconds < 60 || identity.ExpirationSeconds > 30*24*60*60 {
		return "", fmt.Errorf("managed provider file upload fingerprint identity is invalid")
	}
	return deriver.calculateHexHMAC("provider_file_upload_fingerprint", identity)
}

func (deriver *ManagedConsensusKeyDeriver) DeriveProviderFileCredentialFingerprint(channelID, multiKeyIndex int, credential string) (string, error) {
	if channelID <= 0 || multiKeyIndex < 0 || credential == "" {
		return "", fmt.Errorf("managed provider file credential identity is invalid")
	}
	return deriver.calculateHexHMAC("provider_file_credential", struct {
		ChannelID     int    `json:"channel_id"`
		MultiKeyIndex int    `json:"multi_key_index"`
		Credential    string `json:"credential"`
	}{ChannelID: channelID, MultiKeyIndex: multiKeyIndex, Credential: credential})
}

func (deriver *ManagedConsensusKeyDeriver) DeriveProviderFileEventHMAC(identity ManagedProviderFileEventIdentity) (string, error) {
	if !validProviderFileHexHMAC(identity.LifecycleHMAC) || identity.Sequence <= 0 || identity.AttemptCount < 0 || identity.CreatedAtUnix <= 0 ||
		strings.TrimSpace(identity.EventType) == "" || strings.TrimSpace(identity.EventType) != identity.EventType || len(identity.EventType) > 48 ||
		len(identity.FromState) > 32 || len(identity.ToState) > 32 || len(identity.ResultCode) > 64 || !validProviderFileHexHMAC(identity.EvidenceDigest) ||
		(identity.Sequence == 1 && identity.PreviousEventHMAC != "") || (identity.Sequence > 1 && !validProviderFileHexHMAC(identity.PreviousEventHMAC)) {
		return "", fmt.Errorf("managed provider file event identity is invalid")
	}
	return deriver.calculateHexHMAC("provider_file_event", identity)
}

func validProviderFileHexHMAC(value string) bool {
	if len(value) != 64 || value != strings.ToLower(value) {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil
}

func (deriver *ManagedConsensusKeyDeriver) deriveProviderFileOwnerHMAC(owner ManagedConsensusOwner) (string, error) {
	if deriver == nil || len(deriver.key) == 0 {
		return "", fmt.Errorf("managed consensus key deriver is required")
	}
	if owner.UserID <= 0 || owner.TokenID <= 0 {
		return "", fmt.Errorf("managed provider file owner identity is invalid")
	}
	endpointFamily := strings.ToLower(strings.TrimSpace(owner.EndpointFamily))
	if endpointFamily == "" {
		return "", fmt.Errorf("managed provider file owner endpoint family is required")
	}
	return deriver.calculateHexHMAC("provider_file_owner", struct {
		UserID         int    `json:"user_id"`
		TokenID        int    `json:"token_id"`
		EndpointFamily string `json:"endpoint_family"`
	}{UserID: owner.UserID, TokenID: owner.TokenID, EndpointFamily: endpointFamily})
}

func (deriver *ManagedConsensusKeyDeriver) deriveOwnerHMAC(owner ManagedConsensusOwner) (string, error) {
	if deriver == nil || len(deriver.key) == 0 {
		return "", fmt.Errorf("managed consensus key deriver is required")
	}
	if owner.UserID <= 0 {
		return "", fmt.Errorf("managed consensus owner user ID must be positive")
	}
	if owner.TokenID <= 0 {
		return "", fmt.Errorf("managed consensus owner token ID must be positive")
	}
	endpointFamily := strings.ToLower(strings.TrimSpace(owner.EndpointFamily))
	if endpointFamily == "" {
		return "", fmt.Errorf("managed consensus owner endpoint family is required")
	}
	return deriver.calculateHMAC("owner", struct {
		UserID         int    `json:"user_id"`
		TokenID        int    `json:"token_id"`
		EndpointFamily string `json:"endpoint_family"`
	}{
		UserID:         owner.UserID,
		TokenID:        owner.TokenID,
		EndpointFamily: endpointFamily,
	})
}

func (deriver *ManagedConsensusKeyDeriver) calculateHMAC(scope string, value any) (string, error) {
	if deriver == nil || len(deriver.key) == 0 {
		return "", fmt.Errorf("managed consensus key deriver is required")
	}
	encoded, err := common.Marshal(value)
	if err != nil {
		return "", fmt.Errorf("encode managed consensus %s HMAC input: %w", scope, err)
	}
	messageAuthenticationCode := hmac.New(sha256.New, deriver.key)
	_, _ = messageAuthenticationCode.Write([]byte(deriver.keyVersion))
	_, _ = messageAuthenticationCode.Write([]byte{0})
	_, _ = messageAuthenticationCode.Write([]byte(scope))
	_, _ = messageAuthenticationCode.Write([]byte{0})
	_, _ = messageAuthenticationCode.Write(encoded)
	return base64.RawURLEncoding.EncodeToString(messageAuthenticationCode.Sum(nil)), nil
}

func (deriver *ManagedConsensusKeyDeriver) calculateHexHMAC(scope string, value any) (string, error) {
	if deriver == nil || len(deriver.key) == 0 {
		return "", fmt.Errorf("managed consensus key deriver is required")
	}
	encoded, err := common.Marshal(value)
	if err != nil {
		return "", fmt.Errorf("encode managed consensus %s HMAC input: %w", scope, err)
	}
	messageAuthenticationCode := hmac.New(sha256.New, deriver.key)
	_, _ = messageAuthenticationCode.Write([]byte(deriver.keyVersion))
	_, _ = messageAuthenticationCode.Write([]byte{0})
	_, _ = messageAuthenticationCode.Write([]byte(scope))
	_, _ = messageAuthenticationCode.Write([]byte{0})
	_, _ = messageAuthenticationCode.Write(encoded)
	return hex.EncodeToString(messageAuthenticationCode.Sum(nil)), nil
}
