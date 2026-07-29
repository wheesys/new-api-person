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

// NewManagedConsensusRuntimeFromEnvironment builds the managed-state runtime
// from an independent, base64-encoded 32-byte key. It never falls back to any
// session, API, channel, or process-local secret.
func NewManagedConsensusRuntimeFromEnvironment(client *redis.Client, maximumRetention time.Duration) (*ManagedConsensusRuntime, error) {
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
	repository, err := NewRedisManagedConsensusRepository(client, maximumRetention)
	if err != nil {
		return nil, err
	}
	return newManagedConsensusRuntime(key, keyVersion, previousKeys, repository)
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
	if repository == nil {
		return nil, fmt.Errorf("managed consensus repository is required")
	}
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
