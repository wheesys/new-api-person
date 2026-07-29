package contextconsensus

import (
	"context"
	"errors"
	"fmt"
	"math"
	"sync"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type memoryManagedConsensusRepository struct {
	mutex                 sync.Mutex
	now                   func() time.Time
	consensusRecords      map[string]ManagedConsensusRecord
	consensusLeases       map[string]ManagedConsensusLease
	nextFencingTokens     map[string]uint64
	providerStateBindings map[string]ManagedProviderStateRecord
}

var _ ManagedConsensusRepository = (*memoryManagedConsensusRepository)(nil)
var _ ManagedConsensusKeyRotationRepository = (*memoryManagedConsensusRepository)(nil)

func newMemoryManagedConsensusRepository(now func() time.Time) *memoryManagedConsensusRepository {
	return &memoryManagedConsensusRepository{
		now:                   now,
		consensusRecords:      map[string]ManagedConsensusRecord{},
		consensusLeases:       map[string]ManagedConsensusLease{},
		nextFencingTokens:     map[string]uint64{},
		providerStateBindings: map[string]ManagedProviderStateRecord{},
	}
}

func (repository *memoryManagedConsensusRepository) LoadConsensus(ctx context.Context, key ManagedConversationStorageKey) (ManagedConsensusRecord, error) {
	if err := ctx.Err(); err != nil {
		return ManagedConsensusRecord{}, err
	}
	repository.mutex.Lock()
	defer repository.mutex.Unlock()
	record, found := repository.consensusRecords[key.RepositoryKey]
	if !found || !record.ExpiresAt.After(repository.now()) {
		delete(repository.consensusRecords, key.RepositoryKey)
		return ManagedConsensusRecord{}, ErrManagedConsensusNotFound
	}
	return record, nil
}

func (repository *memoryManagedConsensusRepository) AcquireConsensusLease(ctx context.Context, key ManagedConversationStorageKey, holderID string, ttl time.Duration) (ManagedConsensusLease, error) {
	if err := ctx.Err(); err != nil {
		return ManagedConsensusLease{}, err
	}
	if key.RepositoryKey == "" || holderID == "" || ttl <= 0 {
		return ManagedConsensusLease{}, fmt.Errorf("conversation key, lease holder, and positive TTL are required")
	}
	repository.mutex.Lock()
	defer repository.mutex.Unlock()
	now := repository.now()
	if current, found := repository.consensusLeases[key.RepositoryKey]; found && current.ExpiresAt.After(now) {
		return ManagedConsensusLease{}, ErrManagedConsensusLeaseHeld
	}
	nextFencingToken := repository.nextFencingTokens[key.RepositoryKey] + 1
	if nextFencingToken == 0 {
		return ManagedConsensusLease{}, fmt.Errorf("managed consensus fencing token overflow")
	}
	repository.nextFencingTokens[key.RepositoryKey] = nextFencingToken
	lease := ManagedConsensusLease{
		RepositoryKey: key.RepositoryKey,
		HolderID:      holderID,
		FencingToken:  nextFencingToken,
		ExpiresAt:     now.Add(ttl),
	}
	repository.consensusLeases[key.RepositoryKey] = lease
	return lease, nil
}

func (repository *memoryManagedConsensusRepository) RenewConsensusLease(ctx context.Context, lease ManagedConsensusLease, ttl time.Duration) (ManagedConsensusLease, error) {
	if err := ctx.Err(); err != nil {
		return ManagedConsensusLease{}, err
	}
	if ttl <= 0 {
		return ManagedConsensusLease{}, fmt.Errorf("positive lease TTL is required")
	}
	repository.mutex.Lock()
	defer repository.mutex.Unlock()
	current, found := repository.consensusLeases[lease.RepositoryKey]
	if !found || !current.ExpiresAt.After(repository.now()) || current.HolderID != lease.HolderID || current.FencingToken != lease.FencingToken {
		return ManagedConsensusLease{}, ErrManagedConsensusLeaseInvalid
	}
	current.ExpiresAt = repository.now().Add(ttl)
	repository.consensusLeases[lease.RepositoryKey] = current
	return current, nil
}

func (repository *memoryManagedConsensusRepository) ReleaseConsensusLease(ctx context.Context, lease ManagedConsensusLease) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	repository.mutex.Lock()
	defer repository.mutex.Unlock()
	current, found := repository.consensusLeases[lease.RepositoryKey]
	if !found || !current.ExpiresAt.After(repository.now()) || current.HolderID != lease.HolderID || current.FencingToken != lease.FencingToken {
		return ErrManagedConsensusLeaseInvalid
	}
	delete(repository.consensusLeases, lease.RepositoryKey)
	return nil
}

func (repository *memoryManagedConsensusRepository) CompareAndSwapConsensus(
	ctx context.Context,
	key ManagedConversationStorageKey,
	expectedRevision uint64,
	lease ManagedConsensusLease,
	payload ManagedEncryptedEnvelope,
	ttl time.Duration,
) (ManagedConsensusRecord, error) {
	if err := ctx.Err(); err != nil {
		return ManagedConsensusRecord{}, err
	}
	if ttl <= 0 {
		return ManagedConsensusRecord{}, fmt.Errorf("positive consensus TTL is required")
	}
	if expectedRevision == math.MaxUint64 {
		return ManagedConsensusRecord{}, fmt.Errorf("managed consensus revision overflow")
	}
	nextRevision := expectedRevision + 1
	if payload.Purpose != ManagedEncryptionPurposeConsensusState || payload.Revision != nextRevision {
		return ManagedConsensusRecord{}, fmt.Errorf("encrypted consensus payload revision or purpose does not match CAS")
	}
	repository.mutex.Lock()
	defer repository.mutex.Unlock()
	now := repository.now()
	currentLease, found := repository.consensusLeases[key.RepositoryKey]
	if lease.RepositoryKey != key.RepositoryKey || !found || !currentLease.ExpiresAt.After(now) || currentLease.HolderID != lease.HolderID || currentLease.FencingToken != lease.FencingToken {
		return ManagedConsensusRecord{}, ErrManagedConsensusLeaseInvalid
	}
	currentRecord, found := repository.consensusRecords[key.RepositoryKey]
	if found && !currentRecord.ExpiresAt.After(now) {
		delete(repository.consensusRecords, key.RepositoryKey)
		currentRecord = ManagedConsensusRecord{}
		found = false
	}
	currentRevision := uint64(0)
	if found {
		currentRevision = currentRecord.Revision
	}
	if currentRevision != expectedRevision {
		return ManagedConsensusRecord{}, ErrManagedConsensusRevisionConflict
	}
	record := ManagedConsensusRecord{
		Revision:     nextRevision,
		FencingToken: currentLease.FencingToken,
		Payload:      payload,
		ExpiresAt:    now.Add(ttl),
	}
	repository.consensusRecords[key.RepositoryKey] = record
	return record, nil
}

func (repository *memoryManagedConsensusRepository) CompareAndSwapMigrateConsensus(
	ctx context.Context,
	previousKey ManagedConversationStorageKey,
	activeKey ManagedConversationStorageKey,
	expectedRevision uint64,
	lease ManagedConsensusLease,
	payload ManagedEncryptedEnvelope,
	ttl time.Duration,
) (ManagedConsensusRecord, error) {
	if err := ctx.Err(); err != nil {
		return ManagedConsensusRecord{}, err
	}
	if ttl <= 0 || previousKey.RepositoryKey == activeKey.RepositoryKey {
		return ManagedConsensusRecord{}, fmt.Errorf("valid migration keys and positive TTL are required")
	}
	if expectedRevision == math.MaxUint64 || payload.Purpose != ManagedEncryptionPurposeConsensusState || payload.Revision != expectedRevision+1 {
		return ManagedConsensusRecord{}, fmt.Errorf("encrypted consensus migration payload is invalid")
	}
	if payload.KeyVersion != activeKey.KeyVersion {
		return ManagedConsensusRecord{}, fmt.Errorf("managed consensus migration payload does not use the active key version")
	}
	repository.mutex.Lock()
	defer repository.mutex.Unlock()
	now := repository.now()
	currentLease, found := repository.consensusLeases[previousKey.RepositoryKey]
	if lease.RepositoryKey != previousKey.RepositoryKey || !found || !currentLease.ExpiresAt.After(now) || currentLease.HolderID != lease.HolderID || currentLease.FencingToken != lease.FencingToken {
		return ManagedConsensusRecord{}, ErrManagedConsensusLeaseInvalid
	}
	previousRecord, found := repository.consensusRecords[previousKey.RepositoryKey]
	if !found || !previousRecord.ExpiresAt.After(now) || previousRecord.Revision != expectedRevision {
		return ManagedConsensusRecord{}, ErrManagedConsensusRevisionConflict
	}
	if activeRecord, activeFound := repository.consensusRecords[activeKey.RepositoryKey]; activeFound && activeRecord.ExpiresAt.After(now) {
		return ManagedConsensusRecord{}, ErrManagedConsensusKeyConflict
	}
	if activeLease, activeLeaseFound := repository.consensusLeases[activeKey.RepositoryKey]; activeLeaseFound && activeLease.ExpiresAt.After(now) {
		return ManagedConsensusRecord{}, ErrManagedConsensusKeyConflict
	}
	record := ManagedConsensusRecord{
		Revision:     expectedRevision + 1,
		FencingToken: lease.FencingToken,
		Payload:      payload,
		ExpiresAt:    now.Add(ttl),
	}
	repository.consensusRecords[activeKey.RepositoryKey] = record
	delete(repository.consensusRecords, previousKey.RepositoryKey)
	if repository.nextFencingTokens[activeKey.RepositoryKey] < lease.FencingToken {
		repository.nextFencingTokens[activeKey.RepositoryKey] = lease.FencingToken
	}
	return record, nil
}

func (repository *memoryManagedConsensusRepository) DeleteConsensus(ctx context.Context, key ManagedConversationStorageKey, expectedRevision uint64, lease ManagedConsensusLease) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	repository.mutex.Lock()
	defer repository.mutex.Unlock()
	now := repository.now()
	currentLease, found := repository.consensusLeases[key.RepositoryKey]
	if lease.RepositoryKey != key.RepositoryKey || !found || !currentLease.ExpiresAt.After(now) || currentLease.HolderID != lease.HolderID || currentLease.FencingToken != lease.FencingToken {
		return ErrManagedConsensusLeaseInvalid
	}
	currentRecord, found := repository.consensusRecords[key.RepositoryKey]
	if !found || !currentRecord.ExpiresAt.After(now) {
		delete(repository.consensusRecords, key.RepositoryKey)
		return ErrManagedConsensusNotFound
	}
	if currentRecord.Revision != expectedRevision {
		return ErrManagedConsensusRevisionConflict
	}
	delete(repository.consensusRecords, key.RepositoryKey)
	return nil
}

func (repository *memoryManagedConsensusRepository) LoadProviderStateBinding(ctx context.Context, key ManagedProviderStateStorageKey) (ManagedProviderStateRecord, error) {
	if err := ctx.Err(); err != nil {
		return ManagedProviderStateRecord{}, err
	}
	repository.mutex.Lock()
	defer repository.mutex.Unlock()
	record, found := repository.providerStateBindings[key.RepositoryKey]
	if !found || !record.ExpiresAt.After(repository.now()) {
		delete(repository.providerStateBindings, key.RepositoryKey)
		return ManagedProviderStateRecord{}, ErrProviderStateBindingNotFound
	}
	return record, nil
}

func (repository *memoryManagedConsensusRepository) RegisterProviderStateBinding(
	ctx context.Context,
	key ManagedProviderStateStorageKey,
	bindingDigest string,
	payload ManagedEncryptedEnvelope,
	ttl time.Duration,
) (ManagedProviderStateRecord, error) {
	if err := ctx.Err(); err != nil {
		return ManagedProviderStateRecord{}, err
	}
	if key.RepositoryKey == "" || bindingDigest == "" || ttl <= 0 {
		return ManagedProviderStateRecord{}, fmt.Errorf("provider state key, binding digest, and positive TTL are required")
	}
	if payload.Purpose != ManagedEncryptionPurposeProviderState || payload.Revision != 1 {
		return ManagedProviderStateRecord{}, fmt.Errorf("encrypted provider state payload has invalid revision or purpose")
	}
	repository.mutex.Lock()
	defer repository.mutex.Unlock()
	now := repository.now()
	current, found := repository.providerStateBindings[key.RepositoryKey]
	if found && current.ExpiresAt.After(now) && current.BindingDigest != bindingDigest {
		return ManagedProviderStateRecord{}, ErrProviderStateBindingConflict
	}
	record := ManagedProviderStateRecord{
		BindingDigest: bindingDigest,
		Payload:       payload,
		ExpiresAt:     now.Add(ttl),
	}
	repository.providerStateBindings[key.RepositoryKey] = record
	return record, nil
}

func (repository *memoryManagedConsensusRepository) DeleteProviderStateBinding(ctx context.Context, key ManagedProviderStateStorageKey, expectedBindingDigest string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	repository.mutex.Lock()
	defer repository.mutex.Unlock()
	current, found := repository.providerStateBindings[key.RepositoryKey]
	if !found || !current.ExpiresAt.After(repository.now()) {
		delete(repository.providerStateBindings, key.RepositoryKey)
		return ErrProviderStateBindingNotFound
	}
	if current.BindingDigest != expectedBindingDigest {
		return ErrProviderStateBindingConflict
	}
	delete(repository.providerStateBindings, key.RepositoryKey)
	return nil
}

func TestManagedConsensusRepositoryEnforcesRevisionLeaseAndFencing(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	repository := newMemoryManagedConsensusRepository(func() time.Time { return now })
	key := ManagedConversationStorageKey{RepositoryKey: "conversation-key"}

	lease, err := repository.AcquireConsensusLease(context.Background(), key, "request-1", time.Minute)
	require.NoError(t, err)
	assert.Equal(t, uint64(1), lease.FencingToken)
	_, err = repository.AcquireConsensusLease(context.Background(), key, "request-2", time.Minute)
	require.ErrorIs(t, err, ErrManagedConsensusLeaseHeld)

	firstRecord, err := repository.CompareAndSwapConsensus(context.Background(), key, 0, lease, managedStoreTestEnvelope(ManagedEncryptionPurposeConsensusState, 1), 10*time.Minute)
	require.NoError(t, err)
	assert.Equal(t, uint64(1), firstRecord.Revision)
	loaded, err := repository.LoadConsensus(context.Background(), key)
	require.NoError(t, err)
	assert.Equal(t, firstRecord, loaded)

	results := make(chan error, 2)
	for i := 0; i < 2; i++ {
		go func() {
			_, compareErr := repository.CompareAndSwapConsensus(context.Background(), key, 1, lease, managedStoreTestEnvelope(ManagedEncryptionPurposeConsensusState, 2), 10*time.Minute)
			results <- compareErr
		}()
	}
	var successes int
	var conflicts int
	for i := 0; i < 2; i++ {
		result := <-results
		if result == nil {
			successes++
		} else if errors.Is(result, ErrManagedConsensusRevisionConflict) {
			conflicts++
		}
	}
	assert.Equal(t, 1, successes)
	assert.Equal(t, 1, conflicts)

	now = now.Add(2 * time.Minute)
	newLease, err := repository.AcquireConsensusLease(context.Background(), key, "request-2", time.Minute)
	require.NoError(t, err)
	assert.Greater(t, newLease.FencingToken, lease.FencingToken)
	_, err = repository.CompareAndSwapConsensus(context.Background(), key, 2, lease, managedStoreTestEnvelope(ManagedEncryptionPurposeConsensusState, 3), time.Minute)
	require.ErrorIs(t, err, ErrManagedConsensusLeaseInvalid)
	thirdRecord, err := repository.CompareAndSwapConsensus(context.Background(), key, 2, newLease, managedStoreTestEnvelope(ManagedEncryptionPurposeConsensusState, 3), time.Minute)
	require.NoError(t, err)
	assert.Equal(t, newLease.FencingToken, thirdRecord.FencingToken)

	now = now.Add(2 * time.Minute)
	_, err = repository.LoadConsensus(context.Background(), key)
	require.ErrorIs(t, err, ErrManagedConsensusNotFound)
}

func TestManagedConsensusRepositoryRenewsAndReleasesOnlyCurrentLease(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	repository := newMemoryManagedConsensusRepository(func() time.Time { return now })
	key := ManagedConversationStorageKey{RepositoryKey: "renew-key"}
	lease, err := repository.AcquireConsensusLease(context.Background(), key, "request", time.Minute)
	require.NoError(t, err)

	now = now.Add(30 * time.Second)
	renewed, err := repository.RenewConsensusLease(context.Background(), lease, 2*time.Minute)
	require.NoError(t, err)
	assert.Equal(t, lease.FencingToken, renewed.FencingToken)
	assert.Equal(t, now.Add(2*time.Minute), renewed.ExpiresAt)

	staleLease := renewed
	staleLease.FencingToken++
	require.ErrorIs(t, repository.ReleaseConsensusLease(context.Background(), staleLease), ErrManagedConsensusLeaseInvalid)
	require.NoError(t, repository.ReleaseConsensusLease(context.Background(), renewed))
}

func TestManagedConsensusRepositoryProviderBindingIsOwnerIsolatedAndConflictSafe(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	repository := newMemoryManagedConsensusRepository(func() time.Time { return now })
	key := ManagedProviderStateStorageKey{RepositoryKey: "owner-a-state"}
	payload := managedStoreTestEnvelope(ManagedEncryptionPurposeProviderState, 1)

	record, err := repository.RegisterProviderStateBinding(context.Background(), key, "binding-a", payload, time.Minute)
	require.NoError(t, err)
	assert.Equal(t, "binding-a", record.BindingDigest)
	loaded, err := repository.LoadProviderStateBinding(context.Background(), key)
	require.NoError(t, err)
	assert.Equal(t, record, loaded)

	_, err = repository.RegisterProviderStateBinding(context.Background(), key, "binding-b", payload, time.Minute)
	require.ErrorIs(t, err, ErrProviderStateBindingConflict)
	_, err = repository.LoadProviderStateBinding(context.Background(), ManagedProviderStateStorageKey{RepositoryKey: "owner-b-state"})
	require.ErrorIs(t, err, ErrProviderStateBindingNotFound)

	now = now.Add(2 * time.Minute)
	_, err = repository.LoadProviderStateBinding(context.Background(), key)
	require.ErrorIs(t, err, ErrProviderStateBindingNotFound)
}

func TestManagedProviderStateBindingValidationRequiresPinnedTarget(t *testing.T) {
	binding := ManagedProviderStateBinding{
		Version:            ManagedConsensusStateVersion,
		OwnerHMAC:          "owner-hmac",
		StateReferenceHMAC: "state-hmac",
		Target: ManagedProviderTargetBinding{
			BindingLevel:          BindingLevelCredential,
			RelayFormat:           types.RelayFormatOpenAIResponses,
			ChannelID:             31,
			ChannelType:           1,
			UpstreamModel:         "gpt-4o",
			MultiKeyIndex:         2,
			CredentialFingerprint: "credential-hmac",
			ReasonCodes:           []string{"responses_previous_response_id"},
		},
		CreatedAtUnix: 1_800_000_000,
		ExpiresAtUnix: 1_800_003_600,
	}
	require.NoError(t, binding.Validate())

	withoutCredential := binding
	withoutCredential.Target.CredentialFingerprint = ""
	require.ErrorContains(t, withoutCredential.Validate(), "credential fingerprint")
}

func TestManagedConsensusStateValidationRejectsUnversionedOrStaleState(t *testing.T) {
	state := ManagedConsensusState{
		Version:  ManagedConsensusStateVersion,
		Revision: 4,
		Mode:     "managed_consensus",
		TaskConsensus: ConsensusSummary{
			Version:             ConsensusSummaryVersion,
			TaskGoal:            []ConsensusFact{},
			Decisions:           []ConsensusFact{},
			MustPreserve:        []ConsensusFact{},
			OpenQuestions:       []ConsensusFact{},
			UserPreferences:     []ConsensusFact{},
			DomainTerms:         map[string]ConsensusFact{},
			CompletedSteps:      []ConsensusFact{},
			PendingSteps:        []ConsensusFact{},
			ArtifactRefs:        []ConsensusFact{},
			ToolResultSummaries: []ConsensusFact{},
			SourceRanges:        []SummarySourceRange{},
			SourceDigest:        "source-digest",
		},
		SourceDigest:  "source-digest",
		PolicyVersion: "policy-v1",
		CreatedAtUnix: 1_800_000_000,
		UpdatedAtUnix: 1_800_000_030,
	}
	require.NoError(t, state.Validate())

	stale := state
	stale.UpdatedAtUnix = stale.CreatedAtUnix - 1
	require.ErrorContains(t, stale.Validate(), "must not precede")
	wrongMode := state
	wrongMode.Mode = "stateless_full_context"
	require.ErrorContains(t, wrongMode.Validate(), "managed_consensus")
}

func managedStoreTestEnvelope(purpose ManagedEncryptionPurpose, revision uint64) ManagedEncryptedEnvelope {
	return ManagedEncryptedEnvelope{
		Version:    managedEncryptedEnvelopeVersion,
		Algorithm:  "AES-256-GCM",
		KeyVersion: "test-key",
		Purpose:    purpose,
		Revision:   revision,
		Nonce:      "nonce",
		Ciphertext: "ciphertext",
	}
}
