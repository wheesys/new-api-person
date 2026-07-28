package contextconsensus

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/go-redis/redis/v8"
)

const (
	managedConsensusEncryptionKeyEnvironment        = "CONTEXT_CONSENSUS_ENCRYPTION_KEY"
	managedConsensusEncryptionKeyVersionEnvironment = "CONTEXT_CONSENSUS_ENCRYPTION_KEY_VERSION"
)

type ManagedConsensusRuntime struct {
	Cipher     *ManagedConsensusCipher
	KeyDeriver *ManagedConsensusKeyDeriver
	Repository ManagedConsensusRepository
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
	cipher, err := NewManagedConsensusCipher(key, keyVersion)
	if err != nil {
		return nil, err
	}
	hashKeyDerivation := hmac.New(sha256.New, key)
	_, _ = hashKeyDerivation.Write([]byte("new-api/context-consensus/hmac/v1"))
	keyDeriver, err := NewManagedConsensusKeyDeriver(hashKeyDerivation.Sum(nil), keyVersion)
	if err != nil {
		return nil, err
	}
	repository, err := NewRedisManagedConsensusRepository(client, maximumRetention)
	if err != nil {
		return nil, err
	}
	return &ManagedConsensusRuntime{Cipher: cipher, KeyDeriver: keyDeriver, Repository: repository}, nil
}
