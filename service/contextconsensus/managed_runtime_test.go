package contextconsensus

import (
	"encoding/base64"
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
