package contextconsensus

import (
	"context"
	"encoding/base64"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type ambiguousManagedMigrationRepository struct {
	*memoryManagedConsensusRepository
	returnErrorAfterMigration bool
}

func (repository *ambiguousManagedMigrationRepository) CompareAndSwapMigrateConsensus(
	ctx context.Context,
	previousKey ManagedConversationStorageKey,
	activeKey ManagedConversationStorageKey,
	expectedRevision uint64,
	lease ManagedConsensusLease,
	payload ManagedEncryptedEnvelope,
	ttl time.Duration,
) (ManagedConsensusRecord, error) {
	record, err := repository.memoryManagedConsensusRepository.CompareAndSwapMigrateConsensus(
		ctx, previousKey, activeKey, expectedRevision, lease, payload, ttl,
	)
	if err == nil && repository.returnErrorAfterMigration {
		repository.returnErrorAfterMigration = false
		return ManagedConsensusRecord{}, fmt.Errorf("migration response unavailable")
	}
	return record, err
}

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

func TestManagedConsensusSessionReadsPreviousKeyNamespaceAndWritesActiveCipher(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	repository := newMemoryManagedConsensusRepository(func() time.Time { return now })
	oldKey := []byte(strings.Repeat("o", 32))
	activeKey := []byte(strings.Repeat("n", 32))
	oldRuntime := managedSessionRuntimeWithKeys(t, repository, oldKey, "v1", nil)
	owner := ManagedConsensusOwner{UserID: 42, TokenID: 84, EndpointFamily: "responses"}
	seedManagedSessionState(t, oldRuntime, owner, "rotating-context", managedSessionTestState(1, now))
	activeOnlyBeforeMigration := managedSessionRuntimeWithKeys(t, repository, activeKey, "v2", nil)
	_, err := BeginManagedConsensusSession(context.Background(), activeOnlyBeforeMigration, BeginManagedConsensusSessionRequest{
		Owner:             owner,
		ExternalContextID: "rotating-context",
		ExpectedRevision:  1,
		HolderID:          "old-key-not-configured",
		LeaseTTL:          time.Minute,
	})
	require.ErrorIs(t, err, ErrManagedConsensusNotFound)

	rotatedRuntime := managedSessionRuntimeWithKeys(t, repository, activeKey, "v2", []managedConsensusPreviousKey{{
		Version: "v1",
		Key:     base64.StdEncoding.EncodeToString(oldKey),
	}})
	session, err := BeginManagedConsensusSession(context.Background(), rotatedRuntime, BeginManagedConsensusSessionRequest{
		Owner:             owner,
		ExternalContextID: "rotating-context",
		ExpectedRevision:  1,
		HolderID:          "rotated-request",
		LeaseTTL:          time.Minute,
	})
	require.NoError(t, err)
	state, err := session.State()
	require.NoError(t, err)
	require.NotNil(t, state)
	assert.Equal(t, uint64(1), state.Revision)

	_, err = session.Commit(context.Background(), managedSessionTestState(2, now.Add(time.Minute)), 10*time.Minute)
	require.NoError(t, err)
	oldStorageKey, err := oldRuntime.KeyDeriver.DeriveConversationStorageKey(owner, "rotating-context")
	require.NoError(t, err)
	activeStorageKey, err := rotatedRuntime.KeyDeriver.DeriveConversationStorageKey(owner, "rotating-context")
	require.NoError(t, err)
	_, oldKeyStillExists := repository.consensusRecords[oldStorageKey.RepositoryKey]
	assert.False(t, oldKeyStillExists)
	record := repository.consensusRecords[activeStorageKey.RepositoryKey]
	assert.Equal(t, "v2", record.Payload.KeyVersion)

	reloaded, err := BeginManagedConsensusSession(context.Background(), rotatedRuntime, BeginManagedConsensusSessionRequest{
		Owner:             owner,
		ExternalContextID: "rotating-context",
		ExpectedRevision:  2,
		HolderID:          "rotated-reload",
		LeaseTTL:          time.Minute,
	})
	require.NoError(t, err)
	require.NoError(t, reloaded.Close(context.Background()))

	activeOnlyRuntime := managedSessionRuntimeWithKeys(t, repository, activeKey, "v2", nil)
	activeOnlySession, err := BeginManagedConsensusSession(context.Background(), activeOnlyRuntime, BeginManagedConsensusSessionRequest{
		Owner:             owner,
		ExternalContextID: "rotating-context",
		ExpectedRevision:  2,
		HolderID:          "missing-old-namespace",
		LeaseTTL:          time.Minute,
	})
	require.NoError(t, err)
	require.NoError(t, activeOnlySession.Close(context.Background()))
}

func TestManagedConsensusSessionRejectsDualKeyNamespaceConflict(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	repository := newMemoryManagedConsensusRepository(func() time.Time { return now })
	oldKey := []byte(strings.Repeat("o", 32))
	activeKey := []byte(strings.Repeat("n", 32))
	owner := ManagedConsensusOwner{UserID: 7, TokenID: 9, EndpointFamily: "chat"}
	oldRuntime := managedSessionRuntimeWithKeys(t, repository, oldKey, "v1", nil)
	activeRuntime := managedSessionRuntimeWithKeys(t, repository, activeKey, "v2", nil)
	seedManagedSessionState(t, oldRuntime, owner, "conflicting-context", managedSessionTestState(1, now))
	seedManagedSessionState(t, activeRuntime, owner, "conflicting-context", managedSessionTestState(1, now))

	rotatedRuntime := managedSessionRuntimeWithKeys(t, repository, activeKey, "v2", []managedConsensusPreviousKey{{
		Version: "v1",
		Key:     base64.StdEncoding.EncodeToString(oldKey),
	}})
	_, err := BeginManagedConsensusSession(context.Background(), rotatedRuntime, BeginManagedConsensusSessionRequest{
		Owner:             owner,
		ExternalContextID: "conflicting-context",
		ExpectedRevision:  1,
		HolderID:          "conflict-request",
		LeaseTTL:          time.Minute,
	})
	require.ErrorIs(t, err, ErrManagedConsensusKeyConflict)
	assert.Empty(t, repository.consensusLeases)
}

func TestManagedConsensusSessionRecoversAmbiguousPreviousKeyMigration(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	baseRepository := newMemoryManagedConsensusRepository(func() time.Time { return now })
	oldKey := []byte(strings.Repeat("o", 32))
	activeKey := []byte(strings.Repeat("n", 32))
	owner := ManagedConsensusOwner{UserID: 11, TokenID: 13, EndpointFamily: "responses"}
	oldRuntime := managedSessionRuntimeWithKeys(t, baseRepository, oldKey, "v1", nil)
	seedManagedSessionState(t, oldRuntime, owner, "ambiguous-migration", managedSessionTestState(1, now))
	repository := &ambiguousManagedMigrationRepository{
		memoryManagedConsensusRepository: baseRepository,
		returnErrorAfterMigration:        true,
	}
	rotatedRuntime := managedSessionRuntimeWithKeys(t, repository, activeKey, "v2", []managedConsensusPreviousKey{{
		Version: "v1",
		Key:     base64.StdEncoding.EncodeToString(oldKey),
	}})
	session, err := BeginManagedConsensusSession(context.Background(), rotatedRuntime, BeginManagedConsensusSessionRequest{
		Owner:             owner,
		ExternalContextID: "ambiguous-migration",
		ExpectedRevision:  1,
		HolderID:          "ambiguous-request",
		LeaseTTL:          time.Minute,
	})
	require.NoError(t, err)
	result, err := session.CommitWithRecovery(
		context.Background(),
		managedSessionTestState(2, now.Add(time.Minute)),
		10*time.Minute,
	)
	require.NoError(t, err)
	assert.True(t, result.Recovered)
	assert.Equal(t, uint64(2), result.Record.Revision)
	activeStorageKey, err := rotatedRuntime.KeyDeriver.DeriveConversationStorageKey(owner, "ambiguous-migration")
	require.NoError(t, err)
	assert.Contains(t, baseRepository.consensusRecords, activeStorageKey.RepositoryKey)
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

func managedSessionRuntimeWithKeys(
	t *testing.T,
	repository ManagedConsensusRepository,
	activeKey []byte,
	activeVersion string,
	previousKeys []managedConsensusPreviousKey,
) *ManagedConsensusRuntime {
	t.Helper()
	runtime, err := newManagedConsensusRuntime(activeKey, activeVersion, previousKeys, repository)
	require.NoError(t, err)
	return runtime
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
