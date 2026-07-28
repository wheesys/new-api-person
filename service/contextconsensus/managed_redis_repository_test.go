package contextconsensus

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/go-redis/redis/v8"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRedisManagedConsensusRepositoryEnforcesLeaseCASFencingAndTTL(t *testing.T) {
	server := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: server.Addr()})
	t.Cleanup(func() { require.NoError(t, client.Close()) })
	repository, err := NewRedisManagedConsensusRepository(client, 10*time.Minute)
	require.NoError(t, err)
	key := managedRedisTestConversationKey(t, "context-secret")

	lease, err := repository.AcquireConsensusLease(context.Background(), key, "request-1", time.Minute)
	require.NoError(t, err)
	assert.Equal(t, uint64(1), lease.FencingToken)
	_, err = repository.AcquireConsensusLease(context.Background(), key, "request-2", time.Minute)
	require.ErrorIs(t, err, ErrManagedConsensusLeaseHeld)

	firstPayload := managedStoreTestEnvelope(ManagedEncryptionPurposeConsensusState, 1)
	firstRecord, err := repository.CompareAndSwapConsensus(context.Background(), key, 0, lease, firstPayload, 2*time.Minute)
	require.NoError(t, err)
	assert.Equal(t, uint64(1), firstRecord.Revision)
	loaded, err := repository.LoadConsensus(context.Background(), key)
	require.NoError(t, err)
	assert.Equal(t, firstRecord.Revision, loaded.Revision)
	assert.Equal(t, firstPayload, loaded.Payload)

	_, err = repository.CompareAndSwapConsensus(context.Background(), key, 0, lease, firstPayload, 2*time.Minute)
	require.ErrorIs(t, err, ErrManagedConsensusRevisionConflict)

	server.FastForward(time.Minute)
	newLease, err := repository.AcquireConsensusLease(context.Background(), key, "request-2", time.Minute)
	require.NoError(t, err)
	assert.Greater(t, newLease.FencingToken, lease.FencingToken)
	_, err = repository.CompareAndSwapConsensus(context.Background(), key, 1, lease, managedStoreTestEnvelope(ManagedEncryptionPurposeConsensusState, 2), 2*time.Minute)
	require.ErrorIs(t, err, ErrManagedConsensusLeaseInvalid)
	secondRecord, err := repository.CompareAndSwapConsensus(context.Background(), key, 1, newLease, managedStoreTestEnvelope(ManagedEncryptionPurposeConsensusState, 2), 2*time.Minute)
	require.NoError(t, err)
	assert.Equal(t, uint64(2), secondRecord.Revision)

	server.FastForward(2 * time.Minute)
	_, err = repository.LoadConsensus(context.Background(), key)
	require.ErrorIs(t, err, ErrManagedConsensusNotFound)
}

func TestRedisManagedConsensusRepositoryCASIsAtomicAcrossClients(t *testing.T) {
	server := miniredis.RunT(t)
	firstClient := redis.NewClient(&redis.Options{Addr: server.Addr()})
	secondClient := redis.NewClient(&redis.Options{Addr: server.Addr()})
	t.Cleanup(func() {
		require.NoError(t, firstClient.Close())
		require.NoError(t, secondClient.Close())
	})
	firstRepository, err := NewRedisManagedConsensusRepository(firstClient, 10*time.Minute)
	require.NoError(t, err)
	secondRepository, err := NewRedisManagedConsensusRepository(secondClient, 10*time.Minute)
	require.NoError(t, err)
	key := managedRedisTestConversationKey(t, "fork-context")
	lease, err := firstRepository.AcquireConsensusLease(context.Background(), key, "fork-holder", time.Minute)
	require.NoError(t, err)

	start := make(chan struct{})
	results := make(chan error, 2)
	var waitGroup sync.WaitGroup
	for _, repository := range []*RedisManagedConsensusRepository{firstRepository, secondRepository} {
		waitGroup.Add(1)
		go func(repository *RedisManagedConsensusRepository) {
			defer waitGroup.Done()
			<-start
			_, compareErr := repository.CompareAndSwapConsensus(
				context.Background(),
				key,
				0,
				lease,
				managedStoreTestEnvelope(ManagedEncryptionPurposeConsensusState, 1),
				time.Minute,
			)
			results <- compareErr
		}(repository)
	}
	close(start)
	waitGroup.Wait()
	close(results)

	var successes int
	var conflicts int
	for result := range results {
		if result == nil {
			successes++
		} else if errors.Is(result, ErrManagedConsensusRevisionConflict) {
			conflicts++
		}
	}
	assert.Equal(t, 1, successes)
	assert.Equal(t, 1, conflicts)
}

func TestRedisManagedConsensusRepositoryProviderBindingIsEncryptedConflictSafeAndExpiring(t *testing.T) {
	server := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: server.Addr()})
	t.Cleanup(func() { require.NoError(t, client.Close()) })
	repository, err := NewRedisManagedConsensusRepository(client, 10*time.Minute)
	require.NoError(t, err)
	deriver, err := NewManagedConsensusKeyDeriver([]byte(strings.Repeat("k", 32)), "v1")
	require.NoError(t, err)
	key, err := deriver.DeriveProviderStateStorageKey(
		ManagedConsensusOwner{UserID: 4, TokenID: 9, EndpointFamily: "responses"},
		"raw-provider-state-secret",
	)
	require.NoError(t, err)
	payload := managedStoreTestEnvelope(ManagedEncryptionPurposeProviderState, 1)

	record, err := repository.RegisterProviderStateBinding(context.Background(), key, "binding-digest-a", payload, time.Minute)
	require.NoError(t, err)
	loaded, err := repository.LoadProviderStateBinding(context.Background(), key)
	require.NoError(t, err)
	assert.Equal(t, record.BindingDigest, loaded.BindingDigest)
	assert.Equal(t, payload, loaded.Payload)
	for _, storedKey := range server.Keys() {
		assert.NotContains(t, storedKey, "raw-provider-state-secret")
	}
	storedPayload := server.HGet(key.RepositoryKey, managedRedisPayloadField)
	assert.NotContains(t, storedPayload, "raw-provider-state-secret")

	_, err = repository.RegisterProviderStateBinding(context.Background(), key, "binding-digest-b", payload, time.Minute)
	require.ErrorIs(t, err, ErrProviderStateBindingConflict)
	server.FastForward(time.Minute)
	_, err = repository.LoadProviderStateBinding(context.Background(), key)
	require.ErrorIs(t, err, ErrProviderStateBindingNotFound)
}

func TestRedisManagedConsensusRepositoryRejectsPermanentOrOverlongState(t *testing.T) {
	server := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: server.Addr()})
	t.Cleanup(func() { require.NoError(t, client.Close()) })
	repository, err := NewRedisManagedConsensusRepository(client, time.Minute)
	require.NoError(t, err)
	key := managedRedisTestConversationKey(t, "no-ttl")
	require.NoError(t, client.HSet(context.Background(), key.RepositoryKey, map[string]interface{}{
		managedRedisRevisionField: "1",
		managedRedisFencingField:  "1",
		managedRedisPayloadField:  `{}`,
	}).Err())

	_, err = repository.LoadConsensus(context.Background(), key)
	require.ErrorContains(t, err, "no valid TTL")
	_, err = repository.AcquireConsensusLease(context.Background(), key, "holder", 2*time.Minute)
	require.ErrorContains(t, err, "maximum retention")
}

func managedRedisTestConversationKey(t *testing.T, externalContextID string) ManagedConversationStorageKey {
	t.Helper()
	deriver, err := NewManagedConsensusKeyDeriver([]byte(strings.Repeat("h", 32)), "v1")
	require.NoError(t, err)
	key, err := deriver.DeriveConversationStorageKey(
		ManagedConsensusOwner{UserID: 3, TokenID: 7, EndpointFamily: "chat"},
		externalContextID,
	)
	require.NoError(t, err)
	return key
}
