package contextconsensus

import (
	"encoding/base64"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/go-redis/redis/v8"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewManagedConsensusRuntimeFromEnvironmentFailsClosed(t *testing.T) {
	client := redis.NewClient(&redis.Options{Addr: "127.0.0.1:1"})
	t.Cleanup(func() { require.NoError(t, client.Close()) })

	t.Setenv(managedConsensusEncryptionKeyEnvironment, "")
	t.Setenv(managedConsensusEncryptionKeyVersionEnvironment, "")
	t.Setenv(managedConsensusPreviousKeysEnvironment, "")
	_, err := NewManagedConsensusRuntimeFromEnvironment(client, time.Hour)
	require.ErrorContains(t, err, managedConsensusEncryptionKeyEnvironment)

	t.Setenv(managedConsensusEncryptionKeyEnvironment, "not-base64")
	_, err = NewManagedConsensusRuntimeFromEnvironment(client, time.Hour)
	require.ErrorContains(t, err, "strict base64")

	t.Setenv(managedConsensusEncryptionKeyEnvironment, base64.StdEncoding.EncodeToString([]byte("short")))
	_, err = NewManagedConsensusRuntimeFromEnvironment(client, time.Hour)
	require.ErrorContains(t, err, "exactly 32 bytes")

	t.Setenv(managedConsensusEncryptionKeyEnvironment, base64.StdEncoding.EncodeToString([]byte(strings.Repeat("k", 32))))
	_, err = NewManagedConsensusRuntimeFromEnvironment(client, time.Hour)
	require.ErrorContains(t, err, managedConsensusEncryptionKeyVersionEnvironment)
}

func TestNewManagedConsensusRuntimeFromEnvironmentBuildsDomainSeparatedRuntime(t *testing.T) {
	client := redis.NewClient(&redis.Options{Addr: "127.0.0.1:1"})
	t.Cleanup(func() { require.NoError(t, client.Close()) })
	t.Setenv(managedConsensusEncryptionKeyEnvironment, base64.StdEncoding.EncodeToString([]byte(strings.Repeat("k", 32))))
	t.Setenv(managedConsensusEncryptionKeyVersionEnvironment, "managed-v1")
	t.Setenv(managedConsensusPreviousKeysEnvironment, "")

	runtime, err := NewManagedConsensusRuntimeFromEnvironment(client, time.Hour)
	require.NoError(t, err)
	require.NotNil(t, runtime.Cipher)
	require.NotNil(t, runtime.KeyDeriver)
	require.NotNil(t, runtime.Repository)

	storageKey, err := runtime.KeyDeriver.DeriveConversationStorageKey(
		ManagedConsensusOwner{UserID: 1, TokenID: 2, EndpointFamily: "chat"},
		"raw-context-id",
	)
	require.NoError(t, err)
	assert.NotContains(t, storageKey.RepositoryKey, "raw-context-id")
}

func TestNewManagedConsensusRuntimeFromEnvironmentBuildsBoundedPreviousKeyring(t *testing.T) {
	client := redis.NewClient(&redis.Options{Addr: "127.0.0.1:1"})
	t.Cleanup(func() { require.NoError(t, client.Close()) })
	activeKey := base64.StdEncoding.EncodeToString([]byte(strings.Repeat("n", 32)))
	previousKey := base64.StdEncoding.EncodeToString([]byte(strings.Repeat("o", 32)))
	t.Setenv(managedConsensusEncryptionKeyEnvironment, activeKey)
	t.Setenv(managedConsensusEncryptionKeyVersionEnvironment, "v2")
	t.Setenv(managedConsensusPreviousKeysEnvironment, fmt.Sprintf(`[{"version":"v1","key":%q}]`, previousKey))

	runtime, err := NewManagedConsensusRuntimeFromEnvironment(client, time.Hour)
	require.NoError(t, err)
	assert.Len(t, runtime.readCiphers, 2)
	assert.Len(t, runtime.readKeyDerivers, 2)
	assert.Equal(t, "v2", runtime.Cipher.keyVersion)
	assert.Equal(t, "v2", runtime.readKeyDerivers[0].keyVersion)
	assert.Equal(t, "v1", runtime.readKeyDerivers[1].keyVersion)
}

func TestNewManagedConsensusRuntimeFromEnvironmentRejectsInvalidPreviousKeyring(t *testing.T) {
	client := redis.NewClient(&redis.Options{Addr: "127.0.0.1:1"})
	t.Cleanup(func() { require.NoError(t, client.Close()) })
	encodedKey := base64.StdEncoding.EncodeToString([]byte(strings.Repeat("k", 32)))
	t.Setenv(managedConsensusEncryptionKeyEnvironment, encodedKey)
	t.Setenv(managedConsensusEncryptionKeyVersionEnvironment, "v2")

	t.Setenv(managedConsensusPreviousKeysEnvironment, `[{"version":"v2","key":"redacted"}]`)
	_, err := NewManagedConsensusRuntimeFromEnvironment(client, time.Hour)
	require.ErrorContains(t, err, "duplicate key version")

	t.Setenv(managedConsensusPreviousKeysEnvironment, `[{"version":"v1","key":"not-base64"}]`)
	_, err = NewManagedConsensusRuntimeFromEnvironment(client, time.Hour)
	require.ErrorContains(t, err, "invalid key")
	assert.NotContains(t, err.Error(), "not-base64")

	t.Setenv(managedConsensusPreviousKeysEnvironment, `null`)
	_, err = NewManagedConsensusRuntimeFromEnvironment(client, time.Hour)
	require.ErrorContains(t, err, "must be a JSON array")

	tooMany := make([]string, 0, managedConsensusMaximumPreviousKeys+1)
	for index := 0; index <= managedConsensusMaximumPreviousKeys; index++ {
		tooMany = append(tooMany, fmt.Sprintf(`{"version":"old-%d","key":%q}`, index, encodedKey))
	}
	t.Setenv(managedConsensusPreviousKeysEnvironment, "["+strings.Join(tooMany, ",")+"]")
	_, err = NewManagedConsensusRuntimeFromEnvironment(client, time.Hour)
	require.ErrorContains(t, err, "supports at most")
}
