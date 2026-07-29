package contextconsensus

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"math"
	"sync"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/relaykit/types"
)

type ManagedConsensusCommitResult struct {
	Record    ManagedConsensusRecord
	Recovered bool
}

type BeginManagedConsensusSessionRequest struct {
	Owner             ManagedConsensusOwner
	ExternalContextID string
	ExpectedRevision  uint64
	HolderID          string
	LeaseTTL          time.Duration
}

type ManagedConsensusSession struct {
	mutex            sync.Mutex
	runtime          *ManagedConsensusRuntime
	storageKey       ManagedConversationStorageKey
	lease            ManagedConsensusLease
	expectedRevision uint64
	leaseTTL         time.Duration
	state            *ManagedConsensusState
	closed           bool
	committed        bool
	released         bool
}

func BeginManagedConsensusSession(ctx context.Context, runtime *ManagedConsensusRuntime, request BeginManagedConsensusSessionRequest) (*ManagedConsensusSession, error) {
	if runtime == nil || runtime.Cipher == nil || runtime.KeyDeriver == nil || runtime.Repository == nil {
		return nil, fmt.Errorf("managed consensus runtime is unavailable")
	}
	storageKey, err := runtime.KeyDeriver.DeriveConversationStorageKey(request.Owner, request.ExternalContextID)
	if err != nil {
		return nil, err
	}
	lease, err := runtime.Repository.AcquireConsensusLease(ctx, storageKey, request.HolderID, request.LeaseTTL)
	if err != nil {
		return nil, err
	}
	session := &ManagedConsensusSession{
		runtime:          runtime,
		storageKey:       storageKey,
		lease:            lease,
		expectedRevision: request.ExpectedRevision,
		leaseTTL:         request.LeaseTTL,
	}
	loadedRecord, loadErr := runtime.Repository.LoadConsensus(ctx, storageKey)
	if request.ExpectedRevision == 0 {
		if loadErr == nil {
			session.releaseAfterFailedBegin(ctx)
			return nil, ErrManagedConsensusRevisionConflict
		}
		if errors.Is(loadErr, ErrManagedConsensusNotFound) {
			return session, nil
		}
		session.releaseAfterFailedBegin(ctx)
		return nil, loadErr
	}
	if loadErr != nil {
		session.releaseAfterFailedBegin(ctx)
		return nil, loadErr
	}
	if loadedRecord.Revision != request.ExpectedRevision {
		session.releaseAfterFailedBegin(ctx)
		return nil, ErrManagedConsensusRevisionConflict
	}
	var state ManagedConsensusState
	if err := runtime.Cipher.DecryptJSON(ctx, ManagedEncryptionContext{
		RepositoryKey: storageKey.RepositoryKey,
		Purpose:       ManagedEncryptionPurposeConsensusState,
		Revision:      loadedRecord.Revision,
	}, loadedRecord.Payload, &state); err != nil {
		session.releaseAfterFailedBegin(ctx)
		return nil, err
	}
	if err := state.Validate(); err != nil {
		session.releaseAfterFailedBegin(ctx)
		return nil, err
	}
	if state.Revision != loadedRecord.Revision {
		session.releaseAfterFailedBegin(ctx)
		return nil, fmt.Errorf("managed consensus state revision does not match repository metadata")
	}
	session.state = &state
	return session, nil
}

func (session *ManagedConsensusSession) State() (*ManagedConsensusState, error) {
	if session == nil {
		return nil, fmt.Errorf("managed consensus session is required")
	}
	session.mutex.Lock()
	defer session.mutex.Unlock()
	if session.state == nil {
		return nil, nil
	}
	encoded, err := common.Marshal(session.state)
	if err != nil {
		return nil, fmt.Errorf("clone managed consensus state: %w", err)
	}
	var stateCopy ManagedConsensusState
	if err := common.Unmarshal(encoded, &stateCopy); err != nil {
		return nil, fmt.Errorf("clone managed consensus state: %w", err)
	}
	return &stateCopy, nil
}

func (session *ManagedConsensusSession) Inject(body []byte, protocol types.RelayFormat) ([]byte, error) {
	state, err := session.State()
	if err != nil {
		return nil, err
	}
	return PrepareManagedIncrementalRequest(protocol, body, state)
}

func (session *ManagedConsensusSession) Renew(ctx context.Context, ttl time.Duration) error {
	if session == nil {
		return fmt.Errorf("managed consensus session is required")
	}
	session.mutex.Lock()
	defer session.mutex.Unlock()
	if session.closed || session.committed {
		return ErrManagedConsensusLeaseInvalid
	}
	renewed, err := session.runtime.Repository.RenewConsensusLease(ctx, session.lease, ttl)
	if err != nil {
		session.closed = true
		return err
	}
	session.lease = renewed
	return nil
}

func (session *ManagedConsensusSession) Commit(ctx context.Context, state ManagedConsensusState, ttl time.Duration) (ManagedConsensusRecord, error) {
	if session == nil {
		return ManagedConsensusRecord{}, fmt.Errorf("managed consensus session is required")
	}
	session.mutex.Lock()
	defer session.mutex.Unlock()
	if session.closed || session.committed {
		return ManagedConsensusRecord{}, ErrManagedConsensusLeaseInvalid
	}
	if session.expectedRevision == math.MaxUint64 || state.Revision != session.expectedRevision+1 {
		return ManagedConsensusRecord{}, ErrManagedConsensusRevisionConflict
	}
	if err := state.Validate(); err != nil {
		return ManagedConsensusRecord{}, err
	}
	payload, err := session.runtime.Cipher.EncryptJSON(ctx, ManagedEncryptionContext{
		RepositoryKey: session.storageKey.RepositoryKey,
		Purpose:       ManagedEncryptionPurposeConsensusState,
		Revision:      state.Revision,
	}, state)
	if err != nil {
		return ManagedConsensusRecord{}, err
	}
	record, err := session.runtime.Repository.CompareAndSwapConsensus(
		ctx,
		session.storageKey,
		session.expectedRevision,
		session.lease,
		payload,
		ttl,
	)
	if err != nil {
		return ManagedConsensusRecord{}, err
	}
	session.committed = true
	session.state = &state
	session.releaseLeaseWithoutChangingCommit(ctx)
	return record, nil
}

// CommitWithRecovery resolves an ambiguous repository error while the same
// fenced lease is still held. It performs at most one CAS retry and never
// treats a different payload at the next revision as this request's commit.
func (session *ManagedConsensusSession) CommitWithRecovery(ctx context.Context, state ManagedConsensusState, ttl time.Duration) (ManagedConsensusCommitResult, error) {
	if session == nil {
		return ManagedConsensusCommitResult{}, fmt.Errorf("managed consensus session is required")
	}
	session.mutex.Lock()
	defer session.mutex.Unlock()
	if session.closed || session.committed {
		return ManagedConsensusCommitResult{}, ErrManagedConsensusLeaseInvalid
	}
	if session.expectedRevision == math.MaxUint64 || state.Revision != session.expectedRevision+1 {
		return ManagedConsensusCommitResult{}, ErrManagedConsensusRevisionConflict
	}
	if err := state.Validate(); err != nil {
		return ManagedConsensusCommitResult{}, err
	}
	payload, err := session.runtime.Cipher.EncryptJSON(ctx, ManagedEncryptionContext{
		RepositoryKey: session.storageKey.RepositoryKey,
		Purpose:       ManagedEncryptionPurposeConsensusState,
		Revision:      state.Revision,
	}, state)
	if err != nil {
		return ManagedConsensusCommitResult{}, err
	}

	commit := func(commitContext context.Context) (ManagedConsensusRecord, error) {
		return session.runtime.Repository.CompareAndSwapConsensus(commitContext, session.storageKey, session.expectedRevision, session.lease, payload, ttl)
	}
	record, commitErr := commit(ctx)
	if commitErr == nil {
		session.finishCommit(ctx, state)
		return ManagedConsensusCommitResult{Record: record}, nil
	}
	if managedCommitErrorIsDefinitive(commitErr) {
		return ManagedConsensusCommitResult{}, fmt.Errorf("%w: %v", ErrManagedConsensusCommitFailed, commitErr)
	}

	recoveryContext, cancelRecovery := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Second)
	defer cancelRecovery()
	recovered, currentAtExpected, resolvedRecord, resolveErr := session.resolveCommitOutcome(recoveryContext, state)
	if recovered {
		session.finishCommit(ctx, state)
		return ManagedConsensusCommitResult{Record: resolvedRecord, Recovered: true}, nil
	}
	if resolveErr != nil || !currentAtExpected {
		return ManagedConsensusCommitResult{}, fmt.Errorf("%w: %v", ErrManagedConsensusOutcomeUnknown, errors.Join(commitErr, resolveErr))
	}
	if err := ctx.Err(); err != nil {
		return ManagedConsensusCommitResult{}, fmt.Errorf("%w: %v", ErrManagedConsensusCommitFailed, err)
	}

	renewedLease, renewErr := session.runtime.Repository.RenewConsensusLease(recoveryContext, session.lease, session.leaseTTL)
	if renewErr != nil {
		if managedCommitErrorIsDefinitive(renewErr) {
			return ManagedConsensusCommitResult{}, fmt.Errorf("%w: %v", ErrManagedConsensusCommitFailed, renewErr)
		}
		return ManagedConsensusCommitResult{}, fmt.Errorf("%w: %v", ErrManagedConsensusOutcomeUnknown, renewErr)
	}
	session.lease = renewedLease
	record, retryErr := commit(recoveryContext)
	if retryErr == nil {
		session.finishCommit(ctx, state)
		return ManagedConsensusCommitResult{Record: record}, nil
	}
	if managedCommitErrorIsDefinitive(retryErr) {
		return ManagedConsensusCommitResult{}, fmt.Errorf("%w: %v", ErrManagedConsensusCommitFailed, retryErr)
	}
	recovered, currentAtExpected, resolvedRecord, resolveErr = session.resolveCommitOutcome(recoveryContext, state)
	if recovered {
		session.finishCommit(ctx, state)
		return ManagedConsensusCommitResult{Record: resolvedRecord, Recovered: true}, nil
	}
	if resolveErr == nil && currentAtExpected {
		return ManagedConsensusCommitResult{}, fmt.Errorf("%w: %v", ErrManagedConsensusCommitFailed, retryErr)
	}
	return ManagedConsensusCommitResult{}, fmt.Errorf("%w: %v", ErrManagedConsensusOutcomeUnknown, errors.Join(retryErr, resolveErr))
}

func (session *ManagedConsensusSession) resolveCommitOutcome(ctx context.Context, candidate ManagedConsensusState) (bool, bool, ManagedConsensusRecord, error) {
	record, err := session.runtime.Repository.LoadConsensus(ctx, session.storageKey)
	if errors.Is(err, ErrManagedConsensusNotFound) && session.expectedRevision == 0 {
		return false, true, ManagedConsensusRecord{}, nil
	}
	if err != nil {
		return false, false, ManagedConsensusRecord{}, err
	}
	if record.Revision == session.expectedRevision {
		return false, true, record, nil
	}
	if record.Revision != candidate.Revision || record.FencingToken != session.lease.FencingToken {
		return false, false, record, fmt.Errorf("managed consensus repository advanced to an unrelated revision")
	}
	var stored ManagedConsensusState
	if err := session.runtime.Cipher.DecryptJSON(ctx, ManagedEncryptionContext{
		RepositoryKey: session.storageKey.RepositoryKey,
		Purpose:       ManagedEncryptionPurposeConsensusState,
		Revision:      record.Revision,
	}, record.Payload, &stored); err != nil {
		return false, false, record, err
	}
	if err := stored.Validate(); err != nil {
		return false, false, record, err
	}
	storedJSON, err := common.Marshal(stored)
	if err != nil {
		return false, false, record, err
	}
	candidateJSON, err := common.Marshal(candidate)
	if err != nil {
		return false, false, record, err
	}
	if !bytes.Equal(storedJSON, candidateJSON) {
		return false, false, record, fmt.Errorf("managed consensus next revision payload differs from candidate")
	}
	return true, false, record, nil
}

func managedCommitErrorIsDefinitive(err error) bool {
	return errors.Is(err, ErrManagedConsensusRevisionConflict) || errors.Is(err, ErrManagedConsensusLeaseInvalid)
}

func (session *ManagedConsensusSession) finishCommit(ctx context.Context, state ManagedConsensusState) {
	session.committed = true
	session.state = &state
	session.releaseLeaseWithoutChangingCommit(ctx)
}

func (session *ManagedConsensusSession) Close(ctx context.Context) error {
	if session == nil {
		return nil
	}
	session.mutex.Lock()
	defer session.mutex.Unlock()
	if session.released {
		return nil
	}
	session.closed = true
	err := session.runtime.Repository.ReleaseConsensusLease(ctx, session.lease)
	if err == nil || errors.Is(err, ErrManagedConsensusLeaseInvalid) {
		session.released = true
	}
	return err
}

func (session *ManagedConsensusSession) releaseAfterFailedBegin(ctx context.Context) {
	if session == nil || session.runtime == nil || session.runtime.Repository == nil {
		return
	}
	releaseContext, cancel := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Second)
	defer cancel()
	_ = session.runtime.Repository.ReleaseConsensusLease(releaseContext, session.lease)
}

func (session *ManagedConsensusSession) releaseLeaseWithoutChangingCommit(ctx context.Context) {
	releaseContext, cancel := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Second)
	defer cancel()
	if err := session.runtime.Repository.ReleaseConsensusLease(releaseContext, session.lease); err == nil || errors.Is(err, ErrManagedConsensusLeaseInvalid) {
		session.released = true
	}
}
