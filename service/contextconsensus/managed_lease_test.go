package contextconsensus

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestManagedConsensusLeaseGuardCancelsRequestOnRenewalFailureAndReleases(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	runtime, repository := managedSessionTestRuntime(t, func() time.Time { return now })
	session, err := BeginManagedConsensusSession(context.Background(), runtime, BeginManagedConsensusSessionRequest{
		Owner:             ManagedConsensusOwner{UserID: 1, TokenID: 2, EndpointFamily: "chat"},
		ExternalContextID: "context",
		ExpectedRevision:  0,
		HolderID:          "request",
		LeaseTTL:          time.Minute,
	})
	require.NoError(t, err)

	requestContext, cancel := context.WithCancel(context.Background())
	ticks := make(chan time.Time, 1)
	guard := startManagedConsensusLeaseGuardWithTicks(requestContext, cancel, session, time.Minute, ticks, func() {})
	repository.mutex.Lock()
	for key, lease := range repository.consensusLeases {
		lease.FencingToken++
		repository.consensusLeases[key] = lease
	}
	repository.mutex.Unlock()
	ticks <- now

	<-requestContext.Done()
	require.ErrorIs(t, guard.RenewalError(), ErrManagedConsensusLeaseInvalid)
	require.Error(t, guard.Close(context.Background()))
	repository.mutex.Lock()
	leaseCount := len(repository.consensusLeases)
	repository.mutex.Unlock()
	assert.Equal(t, 1, leaseCount, "a mismatched fencing token must not release another holder's lease")
}

func TestManagedConsensusLeaseGuardStopsAndReleasesOwnedLease(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	runtime, repository := managedSessionTestRuntime(t, func() time.Time { return now })
	session, err := BeginManagedConsensusSession(context.Background(), runtime, BeginManagedConsensusSessionRequest{
		Owner:             ManagedConsensusOwner{UserID: 1, TokenID: 2, EndpointFamily: "chat"},
		ExternalContextID: "context",
		ExpectedRevision:  0,
		HolderID:          "request",
		LeaseTTL:          time.Minute,
	})
	require.NoError(t, err)
	requestContext, cancel := context.WithCancel(context.Background())
	guard := startManagedConsensusLeaseGuardWithTicks(requestContext, cancel, session, time.Minute, make(chan time.Time), func() {})
	require.NoError(t, guard.Close(context.Background()))
	repository.mutex.Lock()
	leaseCount := len(repository.consensusLeases)
	repository.mutex.Unlock()
	assert.Zero(t, leaseCount)
}
