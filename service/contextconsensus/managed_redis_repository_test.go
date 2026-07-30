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

func TestRedisManagedConsensusRepositoryMigratesPreviousNamespaceAtomically(t *testing.T) {
	server := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: server.Addr()})
	t.Cleanup(func() { require.NoError(t, client.Close()) })
	repository, err := NewRedisManagedConsensusRepository(client, 10*time.Minute)
	require.NoError(t, err)
	owner := ManagedConsensusOwner{UserID: 3, TokenID: 7, EndpointFamily: "responses"}
	previousDeriver, err := NewManagedConsensusKeyDeriver([]byte(strings.Repeat("p", 32)), "v1")
	require.NoError(t, err)
	activeDeriver, err := NewManagedConsensusKeyDeriver([]byte(strings.Repeat("a", 32)), "v2")
	require.NoError(t, err)
	previousKey, err := previousDeriver.DeriveConversationStorageKey(owner, "rotating-context")
	require.NoError(t, err)
	activeKey, err := activeDeriver.DeriveConversationStorageKey(owner, "rotating-context")
	require.NoError(t, err)
	lease, err := repository.AcquireConsensusLease(context.Background(), previousKey, "rotation-holder", time.Minute)
	require.NoError(t, err)
	_, err = repository.CompareAndSwapConsensus(
		context.Background(),
		previousKey,
		0,
		lease,
		managedStoreTestEnvelope(ManagedEncryptionPurposeConsensusState, 1),
		2*time.Minute,
	)
	require.NoError(t, err)
	activePayload := managedStoreTestEnvelope(ManagedEncryptionPurposeConsensusState, 2)
	activePayload.KeyVersion = "v2"
	competingActiveLease, err := repository.AcquireConsensusLease(context.Background(), activeKey, "competing-active-holder", time.Minute)
	require.NoError(t, err)
	_, err = repository.CompareAndSwapMigrateConsensus(
		context.Background(), previousKey, activeKey, 1, lease, activePayload, 2*time.Minute,
	)
	require.ErrorIs(t, err, ErrManagedConsensusKeyConflict)
	_, err = repository.LoadConsensus(context.Background(), previousKey)
	require.NoError(t, err)
	require.NoError(t, repository.ReleaseConsensusLease(context.Background(), competingActiveLease))

	record, err := repository.CompareAndSwapMigrateConsensus(
		context.Background(),
		previousKey,
		activeKey,
		1,
		lease,
		activePayload,
		2*time.Minute,
	)
	require.NoError(t, err)
	assert.Equal(t, uint64(2), record.Revision)
	_, err = repository.LoadConsensus(context.Background(), previousKey)
	require.ErrorIs(t, err, ErrManagedConsensusNotFound)
	loaded, err := repository.LoadConsensus(context.Background(), activeKey)
	require.NoError(t, err)
	assert.Equal(t, activePayload, loaded.Payload)
	activeLease, err := repository.AcquireConsensusLease(context.Background(), activeKey, "active-holder", time.Minute)
	require.NoError(t, err)
	assert.Greater(t, activeLease.FencingToken, lease.FencingToken)
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
	payload.KeyVersion = key.KeyVersion

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

func TestRedisManagedConsensusRepositoryCommitsConsensusAndProviderStateAtomically(t *testing.T) {
	server := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: server.Addr()})
	t.Cleanup(func() { require.NoError(t, client.Close()) })
	repository, err := NewRedisManagedConsensusRepository(client, 10*time.Minute)
	require.NoError(t, err)
	owner := ManagedConsensusOwner{UserID: 14, TokenID: 19, EndpointFamily: "responses"}
	deriver, err := NewManagedConsensusKeyDeriver([]byte(strings.Repeat("a", 32)), "v1")
	require.NoError(t, err)
	conversationKey, err := deriver.DeriveConversationStorageKey(owner, "atomic-context")
	require.NoError(t, err)
	providerKey, err := deriver.DeriveProviderStateStorageKey(owner, "resp_atomic_state")
	require.NoError(t, err)
	lease, err := repository.AcquireConsensusLease(context.Background(), conversationKey, "atomic-holder", time.Minute)
	require.NoError(t, err)
	consensusPayload := managedStoreTestEnvelope(ManagedEncryptionPurposeConsensusState, 1)
	consensusPayload.KeyVersion = "v1"
	providerPayload := managedStoreTestEnvelope(ManagedEncryptionPurposeProviderState, 1)
	providerPayload.KeyVersion = "v1"

	consensusRecord, providerRecord, err := repository.CompareAndSwapConsensusWithProviderState(
		context.Background(), conversationKey, 0, lease, consensusPayload, providerKey, nil,
		"binding-digest", providerPayload, 2*time.Minute, 2*time.Minute,
	)
	require.NoError(t, err)
	assert.Equal(t, uint64(1), consensusRecord.Revision)
	assert.Equal(t, "binding-digest", providerRecord.BindingDigest)
	loadedConsensus, err := repository.LoadConsensus(context.Background(), conversationKey)
	require.NoError(t, err)
	assert.Equal(t, consensusPayload, loadedConsensus.Payload)
	loadedProvider, err := repository.LoadProviderStateBinding(context.Background(), providerKey)
	require.NoError(t, err)
	assert.Equal(t, providerPayload, loadedProvider.Payload)
}

func TestRedisManagedConsensusRepositoryRejectsPreviousProviderNamespaceBeforeCAS(t *testing.T) {
	server := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: server.Addr()})
	t.Cleanup(func() { require.NoError(t, client.Close()) })
	repository, err := NewRedisManagedConsensusRepository(client, 10*time.Minute)
	require.NoError(t, err)
	owner := ManagedConsensusOwner{UserID: 21, TokenID: 34, EndpointFamily: "responses"}
	previousDeriver, err := NewManagedConsensusKeyDeriver([]byte(strings.Repeat("p", 32)), "v1")
	require.NoError(t, err)
	activeDeriver, err := NewManagedConsensusKeyDeriver([]byte(strings.Repeat("a", 32)), "v2")
	require.NoError(t, err)
	activeConversationKey, err := activeDeriver.DeriveConversationStorageKey(owner, "rotation-conflict")
	require.NoError(t, err)
	previousProviderKey, err := previousDeriver.DeriveProviderStateStorageKey(owner, "resp_reused_state")
	require.NoError(t, err)
	activeProviderKey, err := activeDeriver.DeriveProviderStateStorageKey(owner, "resp_reused_state")
	require.NoError(t, err)
	previousPayload := managedStoreTestEnvelope(ManagedEncryptionPurposeProviderState, 1)
	previousPayload.KeyVersion = "v1"
	_, err = repository.RegisterProviderStateBinding(context.Background(), previousProviderKey, "previous-digest", previousPayload, 2*time.Minute)
	require.NoError(t, err)
	lease, err := repository.AcquireConsensusLease(context.Background(), activeConversationKey, "rotation-holder", time.Minute)
	require.NoError(t, err)
	consensusPayload := managedStoreTestEnvelope(ManagedEncryptionPurposeConsensusState, 1)
	consensusPayload.KeyVersion = "v2"
	providerPayload := managedStoreTestEnvelope(ManagedEncryptionPurposeProviderState, 1)
	providerPayload.KeyVersion = "v2"

	_, _, err = repository.CompareAndSwapConsensusWithProviderState(
		context.Background(), activeConversationKey, 0, lease, consensusPayload, activeProviderKey,
		[]ManagedProviderStateStorageKey{previousProviderKey}, "active-digest", providerPayload, 2*time.Minute, 2*time.Minute,
	)
	require.ErrorIs(t, err, ErrProviderStateBindingConflict)
	_, err = repository.LoadConsensus(context.Background(), activeConversationKey)
	require.ErrorIs(t, err, ErrManagedConsensusNotFound)
	_, err = repository.LoadProviderStateBinding(context.Background(), activeProviderKey)
	require.ErrorIs(t, err, ErrProviderStateBindingNotFound)
}

func TestRedisManagedConsensusRepositoryMigratesConsensusAndProviderStateAtomically(t *testing.T) {
	server := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: server.Addr()})
	t.Cleanup(func() { require.NoError(t, client.Close()) })
	repository, err := NewRedisManagedConsensusRepository(client, 10*time.Minute)
	require.NoError(t, err)
	owner := ManagedConsensusOwner{UserID: 55, TokenID: 89, EndpointFamily: "responses"}
	previousDeriver, err := NewManagedConsensusKeyDeriver([]byte(strings.Repeat("p", 32)), "v1")
	require.NoError(t, err)
	activeDeriver, err := NewManagedConsensusKeyDeriver([]byte(strings.Repeat("a", 32)), "v2")
	require.NoError(t, err)
	previousKey, err := previousDeriver.DeriveConversationStorageKey(owner, "atomic-migration")
	require.NoError(t, err)
	activeKey, err := activeDeriver.DeriveConversationStorageKey(owner, "atomic-migration")
	require.NoError(t, err)
	providerKey, err := activeDeriver.DeriveProviderStateStorageKey(owner, "resp_migrated_state")
	require.NoError(t, err)
	lease, err := repository.AcquireConsensusLease(context.Background(), previousKey, "migration-holder", time.Minute)
	require.NoError(t, err)
	previousPayload := managedStoreTestEnvelope(ManagedEncryptionPurposeConsensusState, 1)
	previousPayload.KeyVersion = "v1"
	_, err = repository.CompareAndSwapConsensus(context.Background(), previousKey, 0, lease, previousPayload, 2*time.Minute)
	require.NoError(t, err)
	nextPayload := managedStoreTestEnvelope(ManagedEncryptionPurposeConsensusState, 2)
	nextPayload.KeyVersion = "v2"
	providerPayload := managedStoreTestEnvelope(ManagedEncryptionPurposeProviderState, 1)
	providerPayload.KeyVersion = "v2"

	_, _, err = repository.CompareAndSwapMigrateConsensusWithProviderState(
		context.Background(), previousKey, activeKey, 1, lease, nextPayload, providerKey, nil,
		"binding-digest", providerPayload, 2*time.Minute, 2*time.Minute,
	)
	require.NoError(t, err)
	_, err = repository.LoadConsensus(context.Background(), previousKey)
	require.ErrorIs(t, err, ErrManagedConsensusNotFound)
	loadedConsensus, err := repository.LoadConsensus(context.Background(), activeKey)
	require.NoError(t, err)
	assert.Equal(t, nextPayload, loadedConsensus.Payload)
	loadedProvider, err := repository.LoadProviderStateBinding(context.Background(), providerKey)
	require.NoError(t, err)
	assert.Equal(t, providerPayload, loadedProvider.Payload)
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
