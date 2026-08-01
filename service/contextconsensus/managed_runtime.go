package contextconsensus

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/go-redis/redis/v8"
)

const (
	managedConsensusEncryptionKeyEnvironment        = "CONTEXT_CONSENSUS_ENCRYPTION_KEY"
	managedConsensusEncryptionKeyVersionEnvironment = "CONTEXT_CONSENSUS_ENCRYPTION_KEY_VERSION"
	managedConsensusPreviousKeysEnvironment         = "CONTEXT_CONSENSUS_PREVIOUS_ENCRYPTION_KEYS"
	managedConsensusMaximumPreviousKeys             = 4
)

type ManagedConsensusRuntime struct {
	Cipher          *ManagedConsensusCipher
	KeyDeriver      *ManagedConsensusKeyDeriver
	Repository      ManagedConsensusRepository
	readCiphers     map[string]*ManagedConsensusCipher
	readKeyDerivers []*ManagedConsensusKeyDeriver
}

type managedConsensusPreviousKey struct {
	Version string `json:"version"`
	Key     string `json:"key"`
}

type ManagedProviderFileUploadBinding struct {
	IntentKey             ManagedProviderFileUploadIntentKey
	TargetFingerprint     string
	CredentialFingerprint string
	EndpointFingerprint   string
	ScopeFingerprint      string
	RequestFingerprint    string
}

type ManagedProviderFileTargetBinding struct {
	KeyVersion            string
	TargetFingerprint     string
	CredentialFingerprint string
	EndpointFingerprint   string
	ScopeFingerprint      string
}

// NewManagedConsensusRuntimeFromEnvironment builds the managed-state runtime
// from an independent, base64-encoded 32-byte key. It never falls back to any
// session, API, channel, or process-local secret.
func NewManagedConsensusRuntimeFromEnvironment(client *redis.Client, maximumRetention time.Duration) (*ManagedConsensusRuntime, error) {
	runtime, err := NewManagedConsensusCryptoRuntimeFromEnvironment()
	if err != nil {
		return nil, err
	}
	repository, err := NewRedisManagedConsensusRepository(client, maximumRetention)
	if err != nil {
		return nil, err
	}
	runtime.Repository = repository
	return runtime, nil
}

// NewManagedConsensusCryptoRuntimeFromEnvironment builds only the durable
// encryption and lookup runtime. Committed outcome replay intentionally does
// not require Redis to be available.
func NewManagedConsensusCryptoRuntimeFromEnvironment() (*ManagedConsensusRuntime, error) {
	encodedKey := strings.TrimSpace(os.Getenv(managedConsensusEncryptionKeyEnvironment))
	if encodedKey == "" {
		return nil, fmt.Errorf("%s is required", managedConsensusEncryptionKeyEnvironment)
	}
	key, err := base64.StdEncoding.Strict().DecodeString(encodedKey)
	if err != nil {
		return nil, fmt.Errorf("%s must be strict base64: %w", managedConsensusEncryptionKeyEnvironment, err)
	}
	if len(key) != 32 {
		return nil, fmt.Errorf("%s must decode to exactly 32 bytes", managedConsensusEncryptionKeyEnvironment)
	}
	keyVersion := strings.TrimSpace(os.Getenv(managedConsensusEncryptionKeyVersionEnvironment))
	if keyVersion == "" {
		return nil, fmt.Errorf("%s is required", managedConsensusEncryptionKeyVersionEnvironment)
	}
	previousKeys, err := decodeManagedConsensusPreviousKeys(os.Getenv(managedConsensusPreviousKeysEnvironment), keyVersion)
	if err != nil {
		return nil, err
	}
	return newManagedConsensusRuntime(key, keyVersion, previousKeys, nil)
}

func decodeManagedConsensusPreviousKeys(rawValue, activeVersion string) ([]managedConsensusPreviousKey, error) {
	rawValue = strings.TrimSpace(rawValue)
	if rawValue == "" {
		return nil, nil
	}
	var previousKeys []managedConsensusPreviousKey
	if err := common.UnmarshalJsonStr(rawValue, &previousKeys); err != nil {
		return nil, fmt.Errorf("%s must be a JSON array", managedConsensusPreviousKeysEnvironment)
	}
	if previousKeys == nil {
		return nil, fmt.Errorf("%s must be a JSON array", managedConsensusPreviousKeysEnvironment)
	}
	if len(previousKeys) > managedConsensusMaximumPreviousKeys {
		return nil, fmt.Errorf("%s supports at most %d keys", managedConsensusPreviousKeysEnvironment, managedConsensusMaximumPreviousKeys)
	}
	seenVersions := map[string]struct{}{activeVersion: {}}
	for index := range previousKeys {
		previousKeys[index].Version = strings.TrimSpace(previousKeys[index].Version)
		if previousKeys[index].Version == "" {
			return nil, fmt.Errorf("%s contains an empty key version", managedConsensusPreviousKeysEnvironment)
		}
		if _, found := seenVersions[previousKeys[index].Version]; found {
			return nil, fmt.Errorf("%s contains a duplicate key version", managedConsensusPreviousKeysEnvironment)
		}
		seenVersions[previousKeys[index].Version] = struct{}{}
	}
	return previousKeys, nil
}

func newManagedConsensusRuntime(activeKey []byte, activeVersion string, previousKeys []managedConsensusPreviousKey, repository ManagedConsensusRepository) (*ManagedConsensusRuntime, error) {
	activeVersion = strings.TrimSpace(activeVersion)
	activeCipher, activeDeriver, err := buildManagedConsensusKeyVersion(activeKey, activeVersion)
	if err != nil {
		return nil, err
	}
	runtime := &ManagedConsensusRuntime{
		Cipher:          activeCipher,
		KeyDeriver:      activeDeriver,
		Repository:      repository,
		readCiphers:     map[string]*ManagedConsensusCipher{activeVersion: activeCipher},
		readKeyDerivers: []*ManagedConsensusKeyDeriver{activeDeriver},
	}
	for _, previousKey := range previousKeys {
		decodedKey, decodeErr := base64.StdEncoding.Strict().DecodeString(strings.TrimSpace(previousKey.Key))
		if decodeErr != nil || len(decodedKey) != 32 {
			return nil, fmt.Errorf("%s contains an invalid key for version %q", managedConsensusPreviousKeysEnvironment, previousKey.Version)
		}
		cipherValue, deriver, buildErr := buildManagedConsensusKeyVersion(decodedKey, previousKey.Version)
		if buildErr != nil {
			return nil, buildErr
		}
		runtime.readCiphers[previousKey.Version] = cipherValue
		runtime.readKeyDerivers = append(runtime.readKeyDerivers, deriver)
	}
	return runtime, nil
}

func (runtime *ManagedConsensusRuntime) AttachRepository(repository ManagedConsensusRepository) error {
	if runtime == nil || repository == nil {
		return fmt.Errorf("managed consensus repository is required")
	}
	runtime.Repository = repository
	return nil
}

func (runtime *ManagedConsensusRuntime) outcomeStorageKeys(owner ManagedConsensusOwner, externalContextID, idempotencyKey string, expectedRevision uint64) ([]ManagedOutcomeStorageKey, error) {
	if runtime == nil || len(runtime.readKeyDerivers) == 0 {
		return nil, fmt.Errorf("managed consensus runtime is unavailable")
	}
	keys := make([]ManagedOutcomeStorageKey, 0, len(runtime.readKeyDerivers))
	for _, deriver := range runtime.readKeyDerivers {
		key, err := deriver.DeriveOutcomeStorageKey(owner, externalContextID, idempotencyKey, expectedRevision)
		if err != nil {
			return nil, err
		}
		keys = append(keys, key)
	}
	return keys, nil
}

func (runtime *ManagedConsensusRuntime) ProviderFileUploadIntentKeys(owner ManagedConsensusOwner, idempotencyKey string) ([]ManagedProviderFileUploadIntentKey, error) {
	if runtime == nil || len(runtime.readKeyDerivers) == 0 {
		return nil, fmt.Errorf("managed consensus runtime is unavailable")
	}
	keys := make([]ManagedProviderFileUploadIntentKey, 0, len(runtime.readKeyDerivers))
	for _, deriver := range runtime.readKeyDerivers {
		key, err := deriver.DeriveProviderFileUploadIntentKey(owner, idempotencyKey)
		if err != nil {
			return nil, err
		}
		keys = append(keys, key)
	}
	return keys, nil
}

func (runtime *ManagedConsensusRuntime) ProviderFileHandleKeys(owner ManagedConsensusOwner, handle string) ([]ManagedProviderFileHandleKey, error) {
	if runtime == nil || len(runtime.readKeyDerivers) == 0 {
		return nil, fmt.Errorf("managed consensus runtime is unavailable")
	}
	keys := make([]ManagedProviderFileHandleKey, 0, len(runtime.readKeyDerivers))
	for _, deriver := range runtime.readKeyDerivers {
		key, err := deriver.DeriveProviderFileHandleKey(owner, handle)
		if err != nil {
			return nil, err
		}
		keys = append(keys, key)
	}
	return keys, nil
}

func (runtime *ManagedConsensusRuntime) ProviderFileUploadBindings(
	owner ManagedConsensusOwner,
	idempotencyKey string,
	contentDigest string,
	target ManagedProviderFileTargetIdentity,
	credential string,
	purpose string,
	expirationSeconds int,
) ([]ManagedProviderFileUploadBinding, error) {
	if runtime == nil || len(runtime.readKeyDerivers) == 0 {
		return nil, fmt.Errorf("managed consensus runtime is unavailable")
	}
	bindings := make([]ManagedProviderFileUploadBinding, 0, len(runtime.readKeyDerivers))
	targetBindings, err := runtime.ProviderFileTargetBindings(target, credential)
	if err != nil {
		return nil, err
	}
	for index, deriver := range runtime.readKeyDerivers {
		intentKey, err := deriver.DeriveProviderFileUploadIntentKey(owner, idempotencyKey)
		if err != nil {
			return nil, err
		}
		targetBinding := targetBindings[index]
		requestFingerprint, err := deriver.DeriveProviderFileUploadFingerprint(ManagedProviderFileUploadFingerprintIdentity{
			OwnerHMAC: intentKey.OwnerHMAC, ContentDigest: contentDigest, TargetFingerprint: targetBinding.TargetFingerprint,
			Purpose: purpose, ExpirationSeconds: expirationSeconds,
		})
		if err != nil {
			return nil, err
		}
		bindings = append(bindings, ManagedProviderFileUploadBinding{
			IntentKey: intentKey, TargetFingerprint: targetBinding.TargetFingerprint, CredentialFingerprint: targetBinding.CredentialFingerprint,
			EndpointFingerprint: targetBinding.EndpointFingerprint, ScopeFingerprint: targetBinding.ScopeFingerprint, RequestFingerprint: requestFingerprint,
		})
	}
	return bindings, nil
}

func (runtime *ManagedConsensusRuntime) ProviderFileTargetBindings(target ManagedProviderFileTargetIdentity, credential string) ([]ManagedProviderFileTargetBinding, error) {
	if runtime == nil || len(runtime.readKeyDerivers) == 0 {
		return nil, fmt.Errorf("managed consensus runtime is unavailable")
	}
	bindings := make([]ManagedProviderFileTargetBinding, 0, len(runtime.readKeyDerivers))
	for _, deriver := range runtime.readKeyDerivers {
		targetFingerprint, err := deriver.DeriveProviderFileTargetFingerprint(target)
		if err != nil {
			return nil, err
		}
		credentialFingerprint, err := deriver.DeriveProviderFileCredentialFingerprint(target.ChannelID, target.MultiKeyIndex, credential)
		if err != nil {
			return nil, err
		}
		endpointFingerprint, err := deriver.DeriveProviderFileEndpointFingerprint(target.Endpoint)
		if err != nil {
			return nil, err
		}
		scopeFingerprint, err := deriver.DeriveProviderFileScopeFingerprint(target.Organization, target.Project)
		if err != nil {
			return nil, err
		}
		bindings = append(bindings, ManagedProviderFileTargetBinding{
			KeyVersion: deriver.keyVersion, TargetFingerprint: targetFingerprint, CredentialFingerprint: credentialFingerprint,
			EndpointFingerprint: endpointFingerprint, ScopeFingerprint: scopeFingerprint,
		})
	}
	return bindings, nil
}

func (runtime *ManagedConsensusRuntime) EncryptProviderFileReference(ctx context.Context, key ManagedProviderFileUploadIntentKey, payload ManagedProviderFileReferencePayload) ([]byte, string, error) {
	if runtime == nil || runtime.Cipher == nil || runtime.KeyDeriver == nil || key.KeyVersion != runtime.KeyDeriver.keyVersion {
		return nil, "", fmt.Errorf("managed consensus active provider file key is unavailable")
	}
	if err := payload.Validate(); err != nil {
		return nil, "", err
	}
	envelope, err := runtime.Cipher.EncryptJSON(ctx, ManagedEncryptionContext{
		RepositoryKey: key.RepositoryKey, Purpose: ManagedEncryptionPurposeProviderFileReference, Revision: 1,
	}, payload)
	if err != nil {
		return nil, "", err
	}
	encodedEnvelope, err := common.Marshal(envelope)
	if err != nil {
		return nil, "", fmt.Errorf("encode managed provider file reference envelope: %w", err)
	}
	return encodedEnvelope, envelope.KeyVersion, nil
}

func (runtime *ManagedConsensusRuntime) DecryptProviderFileReference(ctx context.Context, repositoryKey string, encodedEnvelope []byte) (ManagedProviderFileReferencePayload, error) {
	if runtime == nil || strings.TrimSpace(repositoryKey) == "" || len(encodedEnvelope) == 0 {
		return ManagedProviderFileReferencePayload{}, fmt.Errorf("managed provider file reference is unavailable")
	}
	var envelope ManagedEncryptedEnvelope
	if err := common.Unmarshal(encodedEnvelope, &envelope); err != nil {
		return ManagedProviderFileReferencePayload{}, fmt.Errorf("decode managed provider file reference envelope: %w", err)
	}
	var payload ManagedProviderFileReferencePayload
	if err := runtime.decryptJSON(ctx, ManagedEncryptionContext{
		RepositoryKey: repositoryKey, Purpose: ManagedEncryptionPurposeProviderFileReference, Revision: 1,
	}, envelope, &payload); err != nil {
		return ManagedProviderFileReferencePayload{}, err
	}
	if err := payload.Validate(); err != nil {
		return ManagedProviderFileReferencePayload{}, err
	}
	return payload, nil
}

func (runtime *ManagedConsensusRuntime) EncryptProviderFileReconciliationReference(ctx context.Context, targetLookupHMAC string, payload ManagedProviderFileReconciliationPayload) ([]byte, string, error) {
	if runtime == nil || runtime.Cipher == nil || runtime.KeyDeriver == nil || !validProviderFileHexHMAC(targetLookupHMAC) {
		return nil, "", fmt.Errorf("managed provider file reconciliation key is unavailable")
	}
	if err := payload.Validate(); err != nil {
		return nil, "", err
	}
	envelope, err := runtime.Cipher.EncryptJSON(ctx, ManagedEncryptionContext{
		RepositoryKey: managedProviderFileRepositoryPrefix + ":reconciliation:" + targetLookupHMAC,
		Purpose:       ManagedEncryptionPurposeProviderFileReconciliation, Revision: 1,
	}, payload)
	if err != nil {
		return nil, "", err
	}
	encodedEnvelope, err := common.Marshal(envelope)
	if err != nil {
		return nil, "", fmt.Errorf("encode managed provider file reconciliation envelope: %w", err)
	}
	return encodedEnvelope, envelope.KeyVersion, nil
}

func (runtime *ManagedConsensusRuntime) DecryptProviderFileReconciliationReference(ctx context.Context, targetLookupHMAC string, encodedEnvelope []byte) (ManagedProviderFileReconciliationPayload, error) {
	if runtime == nil || !validProviderFileHexHMAC(targetLookupHMAC) || len(encodedEnvelope) == 0 {
		return ManagedProviderFileReconciliationPayload{}, fmt.Errorf("managed provider file reconciliation reference is unavailable")
	}
	var envelope ManagedEncryptedEnvelope
	if err := common.Unmarshal(encodedEnvelope, &envelope); err != nil {
		return ManagedProviderFileReconciliationPayload{}, fmt.Errorf("decode managed provider file reconciliation envelope: %w", err)
	}
	var payload ManagedProviderFileReconciliationPayload
	if err := runtime.decryptJSON(ctx, ManagedEncryptionContext{
		RepositoryKey: managedProviderFileRepositoryPrefix + ":reconciliation:" + targetLookupHMAC,
		Purpose:       ManagedEncryptionPurposeProviderFileReconciliation, Revision: 1,
	}, envelope, &payload); err != nil {
		return ManagedProviderFileReconciliationPayload{}, err
	}
	if err := payload.Validate(); err != nil {
		return ManagedProviderFileReconciliationPayload{}, err
	}
	return payload, nil
}

func buildManagedConsensusKeyVersion(key []byte, keyVersion string) (*ManagedConsensusCipher, *ManagedConsensusKeyDeriver, error) {
	cipherValue, err := NewManagedConsensusCipher(key, keyVersion)
	if err != nil {
		return nil, nil, err
	}
	hashKeyDerivation := hmac.New(sha256.New, key)
	_, _ = hashKeyDerivation.Write([]byte("new-api/context-consensus/hmac/v1"))
	keyDeriver, err := NewManagedConsensusKeyDeriver(hashKeyDerivation.Sum(nil), keyVersion)
	if err != nil {
		return nil, nil, err
	}
	return cipherValue, keyDeriver, nil
}

func (runtime *ManagedConsensusRuntime) conversationStorageKeys(owner ManagedConsensusOwner, externalContextID string) ([]ManagedConversationStorageKey, error) {
	if runtime == nil || runtime.KeyDeriver == nil {
		return nil, fmt.Errorf("managed consensus runtime is unavailable")
	}
	derivers := runtime.readKeyDerivers
	if len(derivers) == 0 {
		derivers = []*ManagedConsensusKeyDeriver{runtime.KeyDeriver}
	}
	keys := make([]ManagedConversationStorageKey, 0, len(derivers))
	for _, deriver := range derivers {
		key, err := deriver.DeriveConversationStorageKey(owner, externalContextID)
		if err != nil {
			return nil, err
		}
		keys = append(keys, key)
	}
	return keys, nil
}

func (runtime *ManagedConsensusRuntime) readConversationStorageKeys(storageKey, activeStorageKey ManagedConversationStorageKey) []ManagedConversationStorageKey {
	if runtime == nil {
		return nil
	}
	keys := make([]ManagedConversationStorageKey, 0, len(runtime.readKeyDerivers))
	seen := make(map[string]struct{}, len(runtime.readKeyDerivers))
	for _, key := range []ManagedConversationStorageKey{activeStorageKey, storageKey} {
		if key.RepositoryKey == "" {
			continue
		}
		identity := key.OwnerHMAC + "\x00" + key.ConversationHMAC
		if _, exists := seen[identity]; exists {
			continue
		}
		seen[identity] = struct{}{}
		keys = append(keys, key)
	}
	return keys
}

func (runtime *ManagedConsensusRuntime) decryptJSON(ctx context.Context, encryptionContext ManagedEncryptionContext, envelope ManagedEncryptedEnvelope, destination any) error {
	if runtime == nil || runtime.Cipher == nil {
		return fmt.Errorf("managed consensus runtime is unavailable")
	}
	cipherValue := runtime.Cipher
	if len(runtime.readCiphers) > 0 {
		var found bool
		cipherValue, found = runtime.readCiphers[envelope.KeyVersion]
		if !found {
			return fmt.Errorf("managed consensus encryption key version is unavailable")
		}
	}
	return cipherValue.DecryptJSON(ctx, encryptionContext, envelope, destination)
}
