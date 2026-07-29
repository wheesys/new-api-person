package contextconsensus

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestManagedConsensusSessionCreatesLoadsInjectsRenewsAndCommits(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	runtime, repository := managedSessionTestRuntime(t, func() time.Time { return now })
	owner := ManagedConsensusOwner{UserID: 12, TokenID: 34, EndpointFamily: "chat"}

	newSession, err := BeginManagedConsensusSession(context.Background(), runtime, BeginManagedConsensusSessionRequest{
		Owner:             owner,
		ExternalContextID: "raw-context-secret",
		ExpectedRevision:  0,
		HolderID:          "request-create",
		LeaseTTL:          time.Minute,
	})
	require.NoError(t, err)
	state, err := newSession.State()
	require.NoError(t, err)
	assert.Nil(t, state)
	unchangedBody, err := newSession.Inject([]byte(`{"messages":[{"role":"user","content":"current"}]}`), types.RelayFormatOpenAI)
	require.NoError(t, err)
	assert.JSONEq(t, `{"messages":[{"role":"user","content":"current"}]}`, string(unchangedBody))

	firstState := managedSessionTestState(1, now)
	firstRecord, err := newSession.Commit(context.Background(), firstState, 10*time.Minute)
	require.NoError(t, err)
	assert.Equal(t, uint64(1), firstRecord.Revision)

	loadedSession, err := BeginManagedConsensusSession(context.Background(), runtime, BeginManagedConsensusSessionRequest{
		Owner:             owner,
		ExternalContextID: "raw-context-secret",
		ExpectedRevision:  1,
		HolderID:          "request-load",
		LeaseTTL:          time.Minute,
	})
	require.NoError(t, err)
	loadedState, err := loadedSession.State()
	require.NoError(t, err)
	require.NotNil(t, loadedState)
	assert.Equal(t, uint64(1), loadedState.Revision)
	loadedState.TaskConsensus.SourceDigest = "mutated-copy"
	secondRead, err := loadedSession.State()
	require.NoError(t, err)
	assert.Equal(t, "stored-source", secondRead.TaskConsensus.SourceDigest)

	injected, err := loadedSession.Inject([]byte(`{"messages":[{"role":"system","content":"safe"},{"role":"user","content":"current"}]}`), types.RelayFormatOpenAI)
	require.NoError(t, err)
	assert.Contains(t, string(injected), "Untrusted historical context summary.")
	assert.Contains(t, string(injected), "current")
	require.NoError(t, loadedSession.Renew(context.Background(), 2*time.Minute))

	secondState := managedSessionTestState(2, now.Add(time.Minute))
	secondRecord, err := loadedSession.Commit(context.Background(), secondState, 10*time.Minute)
	require.NoError(t, err)
	assert.Equal(t, uint64(2), secondRecord.Revision)
	assert.Empty(t, repository.consensusLeases)

	storageKey, err := runtime.KeyDeriver.DeriveConversationStorageKey(owner, "raw-context-secret")
	require.NoError(t, err)
	assert.NotContains(t, storageKey.RepositoryKey, "raw-context-secret")
}

func TestManagedConsensusSessionRejectsRevisionMismatchAndReleasesLease(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	runtime, repository := managedSessionTestRuntime(t, func() time.Time { return now })
	owner := ManagedConsensusOwner{UserID: 1, TokenID: 2, EndpointFamily: "responses"}
	seedManagedSessionState(t, runtime, owner, "context", managedSessionTestState(1, now))

	_, err := BeginManagedConsensusSession(context.Background(), runtime, BeginManagedConsensusSessionRequest{
		Owner:             owner,
		ExternalContextID: "context",
		ExpectedRevision:  0,
		HolderID:          "stale-request",
		LeaseTTL:          time.Minute,
	})
	require.ErrorIs(t, err, ErrManagedConsensusRevisionConflict)
	assert.Empty(t, repository.consensusLeases)

	validSession, err := BeginManagedConsensusSession(context.Background(), runtime, BeginManagedConsensusSessionRequest{
		Owner:             owner,
		ExternalContextID: "context",
		ExpectedRevision:  1,
		HolderID:          "valid-request",
		LeaseTTL:          time.Minute,
	})
	require.NoError(t, err)
	require.NoError(t, validSession.Close(context.Background()))
}

func TestManagedConsensusSessionFailsClosedOnCiphertextTamperingAndReleasesLease(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	runtime, repository := managedSessionTestRuntime(t, func() time.Time { return now })
	owner := ManagedConsensusOwner{UserID: 5, TokenID: 8, EndpointFamily: "claude"}
	seedManagedSessionState(t, runtime, owner, "context", managedSessionTestState(1, now))
	storageKey, err := runtime.KeyDeriver.DeriveConversationStorageKey(owner, "context")
	require.NoError(t, err)
	record := repository.consensusRecords[storageKey.RepositoryKey]
	lastCharacter := "A"
	if strings.HasSuffix(record.Payload.Ciphertext, lastCharacter) {
		lastCharacter = "B"
	}
	record.Payload.Ciphertext = record.Payload.Ciphertext[:len(record.Payload.Ciphertext)-1] + lastCharacter
	repository.consensusRecords[storageKey.RepositoryKey] = record

	_, err = BeginManagedConsensusSession(context.Background(), runtime, BeginManagedConsensusSessionRequest{
		Owner:             owner,
		ExternalContextID: "context",
		ExpectedRevision:  1,
		HolderID:          "tampered-request",
		LeaseTTL:          time.Minute,
	})
	require.Error(t, err)
	assert.Empty(t, repository.consensusLeases)
}

func TestManagedConsensusSessionRenewFailurePermanentlyBlocksCommit(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	runtime, repository := managedSessionTestRuntime(t, func() time.Time { return now })
	owner := ManagedConsensusOwner{UserID: 5, TokenID: 8, EndpointFamily: "gemini"}
	session, err := BeginManagedConsensusSession(context.Background(), runtime, BeginManagedConsensusSessionRequest{
		Owner:             owner,
		ExternalContextID: "context",
		ExpectedRevision:  0,
		HolderID:          "renew-request",
		LeaseTTL:          time.Minute,
	})
	require.NoError(t, err)
	storageKey, err := runtime.KeyDeriver.DeriveConversationStorageKey(owner, "context")
	require.NoError(t, err)
	invalidLease := repository.consensusLeases[storageKey.RepositoryKey]
	invalidLease.FencingToken++
	repository.consensusLeases[storageKey.RepositoryKey] = invalidLease

	require.ErrorIs(t, session.Renew(context.Background(), time.Minute), ErrManagedConsensusLeaseInvalid)
	_, err = session.Commit(context.Background(), managedSessionTestState(1, now), time.Minute)
	require.ErrorIs(t, err, ErrManagedConsensusLeaseInvalid)
}

func managedSessionTestRuntime(t *testing.T, now func() time.Time) (*ManagedConsensusRuntime, *memoryManagedConsensusRepository) {
	t.Helper()
	key := []byte(strings.Repeat("m", 32))
	cipher, err := NewManagedConsensusCipher(key, "v1")
	require.NoError(t, err)
	deriver, err := NewManagedConsensusKeyDeriver(key, "v1")
	require.NoError(t, err)
	repository := newMemoryManagedConsensusRepository(now)
	return &ManagedConsensusRuntime{Cipher: cipher, KeyDeriver: deriver, Repository: repository}, repository
}

func seedManagedSessionState(t *testing.T, runtime *ManagedConsensusRuntime, owner ManagedConsensusOwner, contextID string, state ManagedConsensusState) {
	t.Helper()
	session, err := BeginManagedConsensusSession(context.Background(), runtime, BeginManagedConsensusSessionRequest{
		Owner:             owner,
		ExternalContextID: contextID,
		ExpectedRevision:  0,
		HolderID:          "seed-request",
		LeaseTTL:          time.Minute,
	})
	require.NoError(t, err)
	_, err = session.Commit(context.Background(), state, 10*time.Minute)
	require.NoError(t, err)
}

func managedSessionTestState(revision uint64, now time.Time) ManagedConsensusState {
	state := managedInjectionTestState()
	state.Revision = revision
	state.CreatedAtUnix = now.Unix()
	state.UpdatedAtUnix = now.Unix()
	return state
}
