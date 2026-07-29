package contextconsensus

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type managedCommitFaultRepository struct {
	ManagedConsensusRepository
	compareAndSwap func(context.Context, ManagedConversationStorageKey, uint64, ManagedConsensusLease, ManagedEncryptedEnvelope, time.Duration) (ManagedConsensusRecord, error)
	load           func(context.Context, ManagedConversationStorageKey) (ManagedConsensusRecord, error)
}

func (repository *managedCommitFaultRepository) CompareAndSwapConsensus(ctx context.Context, key ManagedConversationStorageKey, expected uint64, lease ManagedConsensusLease, payload ManagedEncryptedEnvelope, ttl time.Duration) (ManagedConsensusRecord, error) {
	return repository.compareAndSwap(ctx, key, expected, lease, payload, ttl)
}

func (repository *managedCommitFaultRepository) LoadConsensus(ctx context.Context, key ManagedConversationStorageKey) (ManagedConsensusRecord, error) {
	if repository.load != nil {
		return repository.load(ctx, key)
	}
	return repository.ManagedConsensusRepository.LoadConsensus(ctx, key)
}

func TestManagedConsensusCommitRecoversAppliedCASAfterTransportError(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	runtime, memoryRepository := managedSessionTestRuntime(t, func() time.Time { return now })
	baseRepository := runtime.Repository
	first := true
	faultRepository := &managedCommitFaultRepository{ManagedConsensusRepository: baseRepository}
	faultRepository.compareAndSwap = func(ctx context.Context, key ManagedConversationStorageKey, expected uint64, lease ManagedConsensusLease, payload ManagedEncryptedEnvelope, ttl time.Duration) (ManagedConsensusRecord, error) {
		record, err := baseRepository.CompareAndSwapConsensus(ctx, key, expected, lease, payload, ttl)
		if first && err == nil {
			first = false
			return ManagedConsensusRecord{}, errors.New("transport closed after Redis applied CAS")
		}
		return record, err
	}
	runtime.Repository = faultRepository
	session, err := BeginManagedConsensusSession(context.Background(), runtime, BeginManagedConsensusSessionRequest{
		Owner:             ManagedConsensusOwner{UserID: 1, TokenID: 2, EndpointFamily: "chat"},
		ExternalContextID: "context", HolderID: "request", LeaseTTL: time.Minute,
	})
	require.NoError(t, err)
	result, err := session.CommitWithRecovery(context.Background(), managedSessionTestState(1, now), 10*time.Minute)
	require.NoError(t, err)
	assert.True(t, result.Recovered)
	assert.Equal(t, uint64(1), result.Record.Revision)
	assert.Len(t, memoryRepository.consensusRecords, 1)
}

func TestManagedConsensusCommitRetriesOnceThenReportsDefinitiveFailure(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	runtime, _ := managedSessionTestRuntime(t, func() time.Time { return now })
	baseRepository := runtime.Repository
	attempts := 0
	faultRepository := &managedCommitFaultRepository{ManagedConsensusRepository: baseRepository}
	faultRepository.compareAndSwap = func(context.Context, ManagedConversationStorageKey, uint64, ManagedConsensusLease, ManagedEncryptedEnvelope, time.Duration) (ManagedConsensusRecord, error) {
		attempts++
		return ManagedConsensusRecord{}, errors.New("temporary Redis error")
	}
	runtime.Repository = faultRepository
	session, err := BeginManagedConsensusSession(context.Background(), runtime, BeginManagedConsensusSessionRequest{
		Owner:             ManagedConsensusOwner{UserID: 1, TokenID: 2, EndpointFamily: "chat"},
		ExternalContextID: "context", HolderID: "request", LeaseTTL: time.Minute,
	})
	require.NoError(t, err)
	_, err = session.CommitWithRecovery(context.Background(), managedSessionTestState(1, now), 10*time.Minute)
	require.ErrorIs(t, err, ErrManagedConsensusCommitFailed)
	assert.Equal(t, 2, attempts)
}

func TestManagedConsensusCommitReportsUnknownWhenReadAfterWriteFails(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	runtime, _ := managedSessionTestRuntime(t, func() time.Time { return now })
	baseRepository := runtime.Repository
	faultRepository := &managedCommitFaultRepository{ManagedConsensusRepository: baseRepository}
	faultRepository.compareAndSwap = func(context.Context, ManagedConversationStorageKey, uint64, ManagedConsensusLease, ManagedEncryptedEnvelope, time.Duration) (ManagedConsensusRecord, error) {
		return ManagedConsensusRecord{}, errors.New("temporary Redis error")
	}
	runtime.Repository = faultRepository
	session, err := BeginManagedConsensusSession(context.Background(), runtime, BeginManagedConsensusSessionRequest{
		Owner:             ManagedConsensusOwner{UserID: 1, TokenID: 2, EndpointFamily: "chat"},
		ExternalContextID: "context", HolderID: "request", LeaseTTL: time.Minute,
	})
	require.NoError(t, err)
	faultRepository.load = func(context.Context, ManagedConversationStorageKey) (ManagedConsensusRecord, error) {
		return ManagedConsensusRecord{}, errors.New("Redis read unavailable")
	}
	_, err = session.CommitWithRecovery(context.Background(), managedSessionTestState(1, now), 10*time.Minute)
	require.ErrorIs(t, err, ErrManagedConsensusOutcomeUnknown)
}

func TestManagedConsensusCommitUsesDetachedReadWithoutRetryingCanceledRequest(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	runtime, _ := managedSessionTestRuntime(t, func() time.Time { return now })
	baseRepository := runtime.Repository
	attempts := 0
	recoveryReadUsedActiveContext := false
	requestContext, cancelRequest := context.WithCancel(context.Background())
	faultRepository := &managedCommitFaultRepository{ManagedConsensusRepository: baseRepository}
	faultRepository.compareAndSwap = func(context.Context, ManagedConversationStorageKey, uint64, ManagedConsensusLease, ManagedEncryptedEnvelope, time.Duration) (ManagedConsensusRecord, error) {
		attempts++
		cancelRequest()
		return ManagedConsensusRecord{}, errors.New("temporary Redis error")
	}
	faultRepository.load = func(ctx context.Context, key ManagedConversationStorageKey) (ManagedConsensusRecord, error) {
		recoveryReadUsedActiveContext = ctx.Err() == nil
		return baseRepository.LoadConsensus(ctx, key)
	}
	runtime.Repository = faultRepository
	session, err := BeginManagedConsensusSession(context.Background(), runtime, BeginManagedConsensusSessionRequest{
		Owner:             ManagedConsensusOwner{UserID: 1, TokenID: 2, EndpointFamily: "chat"},
		ExternalContextID: "context", HolderID: "request", LeaseTTL: time.Minute,
	})
	require.NoError(t, err)

	_, err = session.CommitWithRecovery(requestContext, managedSessionTestState(1, now), 10*time.Minute)

	require.ErrorIs(t, err, ErrManagedConsensusCommitFailed)
	assert.True(t, recoveryReadUsedActiveContext)
	assert.Equal(t, 1, attempts)
}
