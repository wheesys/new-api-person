package contextconsensus

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/relaykit/types"
)

const ManagedOutcomeMaximumResponseBytes = 2 * 1024 * 1024

const (
	ManagedOutcomePhaseIntent               = model.ManagedContextOutcomePhaseIntent
	ManagedOutcomePhaseMainDispatched       = model.ManagedContextOutcomePhaseMainDispatched
	ManagedOutcomePhaseMainSettled          = model.ManagedContextOutcomePhaseMainSettled
	ManagedOutcomePhaseSummaryDispatched    = model.ManagedContextOutcomePhaseSummaryDispatched
	ManagedOutcomePhaseSettledPendingCommit = model.ManagedContextOutcomePhaseSettledPendingCommit
	ManagedOutcomePhaseCommitted            = model.ManagedContextOutcomePhaseCommitted
	ManagedOutcomePhaseTerminalFailed       = model.ManagedContextOutcomePhaseTerminalFailed
	ManagedOutcomePhaseExpired              = model.ManagedContextOutcomePhaseExpired
)

var ErrManagedOutcomeUnknown = model.ErrManagedContextOutcomeUnknown

type ManagedOutcomeRequest struct {
	Owner                   ManagedConsensusOwner
	ExternalContextID       string
	IdempotencyKey          string
	ExpectedRevision        uint64
	Protocol                types.RelayFormat
	IncrementalSourceDigest string
	PolicyVersion           string
	TTL                     time.Duration
}

type ManagedOutcomeResponse struct {
	Status      int    `json:"status"`
	ContentType string `json:"content_type"`
	Body        []byte `json:"body"`
}

type ManagedSummaryExecutionSnapshot struct {
	Evidence             ManagedRevisionEvidence   `json:"evidence"`
	Plan                 ManagedRevisionPlan       `json:"plan"`
	SummaryRequest       *dto.GeneralOpenAIRequest `json:"summary_request"`
	CompactionModel      string                    `json:"compaction_model"`
	ModelPool            []string                  `json:"model_pool"`
	AllowedChannelIDs    []int                     `json:"allowed_channel_ids"`
	MaximumSummaryTokens int                       `json:"maximum_summary_tokens"`
	MaximumInputTokens   int                       `json:"maximum_input_tokens"`
	MaximumQuota         int                       `json:"maximum_quota"`
	TimeoutSeconds       int                       `json:"timeout_seconds"`
}

func (snapshot ManagedSummaryExecutionSnapshot) Validate() error {
	if snapshot.SummaryRequest == nil || strings.TrimSpace(snapshot.CompactionModel) == "" || len(snapshot.ModelPool) == 0 || len(snapshot.AllowedChannelIDs) == 0 ||
		strings.TrimSpace(snapshot.Evidence.PolicyVersion) == "" || strings.TrimSpace(snapshot.Plan.SourceDigest) == "" ||
		snapshot.MaximumSummaryTokens <= 0 || snapshot.MaximumInputTokens <= 0 || snapshot.MaximumQuota <= 0 || snapshot.TimeoutSeconds <= 0 {
		return fmt.Errorf("managed outcome summary execution snapshot is invalid")
	}
	return nil
}

type ManagedOutcomeSession struct {
	runtime     *ManagedConsensusRuntime
	storageKeys []ManagedOutcomeStorageKey
	record      *model.ManagedContextOutcome
	fingerprint string
}

func ReserveManagedOutcome(ctx context.Context, runtime *ManagedConsensusRuntime, request ManagedOutcomeRequest) (*ManagedOutcomeSession, error) {
	if runtime == nil || runtime.Cipher == nil || request.TTL <= 0 || request.Protocol == "" ||
		strings.TrimSpace(request.IncrementalSourceDigest) == "" || strings.TrimSpace(request.PolicyVersion) == "" || request.ExpectedRevision >= math.MaxInt64 {
		return nil, fmt.Errorf("managed outcome request is incomplete")
	}
	storageKeys, err := runtime.outcomeStorageKeys(request.Owner, request.ExternalContextID, request.IdempotencyKey, request.ExpectedRevision)
	if err != nil {
		return nil, err
	}
	fingerprintInput := struct {
		ExpectedRevision        uint64            `json:"expected_revision"`
		Protocol                types.RelayFormat `json:"protocol"`
		IncrementalSourceDigest string            `json:"incremental_source_digest"`
		PolicyVersion           string            `json:"policy_version"`
	}{request.ExpectedRevision, request.Protocol, request.IncrementalSourceDigest, request.PolicyVersion}
	encoded, err := common.Marshal(fingerprintInput)
	if err != nil {
		return nil, err
	}
	fingerprint := digestBytes(encoded)
	candidates := make([]model.ManagedContextOutcomeLookupCandidate, 0, len(storageKeys))
	for _, key := range storageKeys {
		candidates = append(candidates, model.ManagedContextOutcomeLookupCandidate{
			LookupHMAC: key.LookupHMAC, RevisionIntentHMAC: key.RevisionIntentHMAC,
			OwnerHMAC: key.OwnerHMAC, ConversationHMAC: key.ConversationHMAC, KeyVersion: key.KeyVersion,
		})
	}
	record, _, err := model.ReserveManagedContextOutcome(ctx, model.ManagedContextOutcomeIdentity{
		Candidates: candidates, ExpectedRevision: request.ExpectedRevision, RequestFingerprint: fingerprint,
	}, time.Now().Add(request.TTL))
	if err != nil {
		return nil, err
	}
	session := &ManagedOutcomeSession{runtime: runtime, storageKeys: storageKeys, record: record, fingerprint: fingerprint}
	if record.LookupKeyVersion != storageKeys[0].KeyVersion {
		if err := session.migrateToActiveKey(ctx); err != nil {
			return nil, err
		}
	}
	return session, nil
}

func (session *ManagedOutcomeSession) ID() int64                           { return session.record.Id }
func (session *ManagedOutcomeSession) Fingerprint() string                 { return session.fingerprint }
func (session *ManagedOutcomeSession) Phase() string                       { return session.record.Phase }
func (session *ManagedOutcomeSession) Record() model.ManagedContextOutcome { return *session.record }

func (session *ManagedOutcomeSession) Reload(ctx context.Context) error {
	record, err := model.GetManagedContextOutcome(ctx, session.record.Id, session.fingerprint)
	if err != nil {
		return err
	}
	session.record = record
	return nil
}

func (session *ManagedOutcomeSession) MarkMainDispatched(ctx context.Context) error {
	return session.advance(ctx, model.ManagedContextOutcomePhaseIntent, model.ManagedContextOutcomePhaseMainDispatched)
}

func (session *ManagedOutcomeSession) MarkSummaryDispatched(ctx context.Context) error {
	return session.advance(ctx, model.ManagedContextOutcomePhaseMainSettled, model.ManagedContextOutcomePhaseSummaryDispatched)
}

func (session *ManagedOutcomeSession) MarkCommitted(ctx context.Context) error {
	return session.advance(ctx, model.ManagedContextOutcomePhaseSettledPendingCommit, model.ManagedContextOutcomePhaseCommitted)
}

func (session *ManagedOutcomeSession) advance(ctx context.Context, expectedPhase, nextPhase string) error {
	if err := model.AdvanceManagedContextOutcomePhase(ctx, session.record.Id, session.fingerprint, expectedPhase, nextPhase); err != nil {
		return err
	}
	return session.Reload(ctx)
}

func (session *ManagedOutcomeSession) MainCheckpoint(ctx context.Context, response ManagedOutcomeResponse, assistant ManagedAssistantOutput, snapshot ManagedSummaryExecutionSnapshot) (*model.ManagedContextOutcomeCheckpoint, error) {
	if len(response.Body) > ManagedOutcomeMaximumResponseBytes || response.Status < 200 || response.Status >= 300 {
		return nil, fmt.Errorf("managed outcome response is invalid")
	}
	if err := snapshot.Validate(); err != nil {
		return nil, err
	}
	assistantJSON, err := common.Marshal(assistant)
	if err != nil {
		return nil, err
	}
	evidenceAssistantJSON, err := common.Marshal(snapshot.Evidence.AssistantOutput)
	if err != nil {
		return nil, err
	}
	if !bytes.Equal(assistantJSON, evidenceAssistantJSON) {
		return nil, fmt.Errorf("managed outcome assistant does not match summary evidence")
	}
	responsePayload, err := session.encrypt(ctx, ManagedEncryptionPurposeOutcomeResponse, response)
	if err != nil {
		return nil, err
	}
	assistantPayload, err := session.encrypt(ctx, ManagedEncryptionPurposeOutcomeAssistant, assistant)
	if err != nil {
		return nil, err
	}
	summaryExecutionPayload, err := session.encrypt(ctx, ManagedEncryptionPurposeOutcomeSummaryExecution, snapshot)
	if err != nil {
		return nil, err
	}
	return &model.ManagedContextOutcomeCheckpoint{
		OutcomeId: session.record.Id, RequestFingerprint: session.fingerprint,
		ExpectedPhase: model.ManagedContextOutcomePhaseMainDispatched, NextPhase: model.ManagedContextOutcomePhaseMainSettled,
		ResponseStatus: response.Status, ResponseContentType: safeManagedOutcomeContentType(response.ContentType),
		ResponsePayload: responsePayload, AssistantPayload: assistantPayload, SummaryExecutionPayload: summaryExecutionPayload,
	}, nil
}

func (session *ManagedOutcomeSession) SummaryCheckpoint(ctx context.Context, summary ConsensusSummary, nextState ManagedConsensusState) (*model.ManagedContextOutcomeCheckpoint, error) {
	if summary.Version != ConsensusSummaryVersion {
		return nil, fmt.Errorf("managed outcome summary result is invalid")
	}
	if err := nextState.Validate(); err != nil {
		return nil, fmt.Errorf("managed outcome next state is invalid: %w", err)
	}
	if nextState.Revision != session.record.ExpectedRevision+1 {
		return nil, fmt.Errorf("managed outcome next state revision is invalid")
	}
	summaryJSON, err := common.Marshal(summary)
	if err != nil {
		return nil, err
	}
	nextSummaryJSON, err := common.Marshal(nextState.TaskConsensus)
	if err != nil {
		return nil, err
	}
	if !bytes.Equal(summaryJSON, nextSummaryJSON) {
		return nil, fmt.Errorf("managed outcome summary does not match next state")
	}
	summaryPayload, err := session.encrypt(ctx, ManagedEncryptionPurposeOutcomeSummaryResult, summary)
	if err != nil {
		return nil, err
	}
	nextStatePayload, err := session.encrypt(ctx, ManagedEncryptionPurposeOutcomeNextState, nextState)
	if err != nil {
		return nil, err
	}
	return &model.ManagedContextOutcomeCheckpoint{
		OutcomeId: session.record.Id, RequestFingerprint: session.fingerprint,
		ExpectedPhase: model.ManagedContextOutcomePhaseSummaryDispatched, NextPhase: model.ManagedContextOutcomePhaseSettledPendingCommit,
		SummaryResultPayload: summaryPayload, NextStatePayload: nextStatePayload,
	}, nil
}

func (session *ManagedOutcomeSession) Response(ctx context.Context) (ManagedOutcomeResponse, error) {
	var value ManagedOutcomeResponse
	err := session.decrypt(ctx, ManagedEncryptionPurposeOutcomeResponse, session.record.ResponsePayload, &value)
	if err == nil {
		if value.Status < 200 || value.Status >= 300 || len(value.Body) > ManagedOutcomeMaximumResponseBytes {
			return ManagedOutcomeResponse{}, fmt.Errorf("managed outcome response is invalid")
		}
		value.ContentType = safeManagedOutcomeContentType(value.ContentType)
		if value.Status != session.record.ResponseStatus || value.ContentType != session.record.ResponseContentType {
			return ManagedOutcomeResponse{}, fmt.Errorf("managed outcome response metadata does not match its checkpoint")
		}
	}
	return value, err
}

func (session *ManagedOutcomeSession) Assistant(ctx context.Context) (ManagedAssistantOutput, error) {
	var value ManagedAssistantOutput
	return value, session.decrypt(ctx, ManagedEncryptionPurposeOutcomeAssistant, session.record.AssistantPayload, &value)
}

func (session *ManagedOutcomeSession) SummaryExecution(ctx context.Context) (ManagedSummaryExecutionSnapshot, error) {
	var value ManagedSummaryExecutionSnapshot
	if err := session.decrypt(ctx, ManagedEncryptionPurposeOutcomeSummaryExecution, session.record.SummaryExecutionPayload, &value); err != nil {
		return value, err
	}
	rebuiltPlan, err := BuildManagedRevisionPlan(value.Evidence)
	if err != nil {
		return value, fmt.Errorf("rebuild managed outcome summary plan: %w", err)
	}
	if rebuiltPlan.SourceDigest != value.Plan.SourceDigest {
		return value, fmt.Errorf("managed outcome summary plan does not match its frozen snapshot")
	}
	value.Plan = rebuiltPlan
	if err := value.Validate(); err != nil {
		return value, err
	}
	return value, nil
}

func (session *ManagedOutcomeSession) NextState(ctx context.Context) (ManagedConsensusState, error) {
	var value ManagedConsensusState
	if err := session.decrypt(ctx, ManagedEncryptionPurposeOutcomeNextState, session.record.NextStatePayload, &value); err != nil {
		return value, err
	}
	if err := value.Validate(); err != nil {
		return value, err
	}
	if value.Revision != session.record.ExpectedRevision+1 {
		return value, fmt.Errorf("managed outcome next state revision is invalid")
	}
	return value, nil
}

func (session *ManagedOutcomeSession) encrypt(ctx context.Context, purpose ManagedEncryptionPurpose, value any) ([]byte, error) {
	envelope, err := session.runtime.Cipher.EncryptJSON(ctx, ManagedEncryptionContext{
		RepositoryKey: session.storageKeys[0].RepositoryKey, Purpose: purpose, Revision: session.record.ExpectedRevision + 1,
	}, value)
	if err != nil {
		return nil, err
	}
	return common.Marshal(envelope)
}

func (session *ManagedOutcomeSession) decrypt(ctx context.Context, purpose ManagedEncryptionPurpose, payload []byte, destination any) error {
	if len(payload) == 0 {
		return fmt.Errorf("managed outcome payload is unavailable")
	}
	var envelope ManagedEncryptedEnvelope
	if err := common.Unmarshal(payload, &envelope); err != nil {
		return err
	}
	storageKey, found := session.storageKeyForVersion(envelope.KeyVersion)
	if !found {
		return fmt.Errorf("managed outcome encryption key version is unavailable")
	}
	return session.runtime.decryptJSON(ctx, ManagedEncryptionContext{
		RepositoryKey: storageKey.RepositoryKey, Purpose: purpose, Revision: session.record.ExpectedRevision + 1,
	}, envelope, destination)
}

func (session *ManagedOutcomeSession) storageKeyForVersion(version string) (ManagedOutcomeStorageKey, bool) {
	for _, key := range session.storageKeys {
		if key.KeyVersion == version {
			return key, true
		}
	}
	return ManagedOutcomeStorageKey{}, false
}

func (session *ManagedOutcomeSession) migrateToActiveKey(ctx context.Context) error {
	previous, found := session.storageKeyForVersion(session.record.LookupKeyVersion)
	if !found {
		return fmt.Errorf("managed outcome previous key version is unavailable")
	}
	active := session.storageKeys[0]
	reencrypt := func(purpose ManagedEncryptionPurpose, payload []byte) ([]byte, error) {
		if len(payload) == 0 {
			return nil, nil
		}
		var raw any
		switch purpose {
		case ManagedEncryptionPurposeOutcomeResponse:
			raw = &ManagedOutcomeResponse{}
		case ManagedEncryptionPurposeOutcomeAssistant:
			raw = &ManagedAssistantOutput{}
		case ManagedEncryptionPurposeOutcomeSummaryExecution:
			raw = &ManagedSummaryExecutionSnapshot{}
		case ManagedEncryptionPurposeOutcomeSummaryResult:
			raw = &ConsensusSummary{}
		case ManagedEncryptionPurposeOutcomeNextState:
			raw = &ManagedConsensusState{}
		default:
			return nil, fmt.Errorf("managed outcome purpose is invalid")
		}
		if err := session.decrypt(ctx, purpose, payload, raw); err != nil {
			return nil, err
		}
		envelope, err := session.runtime.Cipher.EncryptJSON(ctx, ManagedEncryptionContext{RepositoryKey: active.RepositoryKey, Purpose: purpose, Revision: session.record.ExpectedRevision + 1}, raw)
		if err != nil {
			return nil, err
		}
		return common.Marshal(envelope)
	}
	response, err := reencrypt(ManagedEncryptionPurposeOutcomeResponse, session.record.ResponsePayload)
	if err != nil {
		return err
	}
	assistant, err := reencrypt(ManagedEncryptionPurposeOutcomeAssistant, session.record.AssistantPayload)
	if err != nil {
		return err
	}
	summary, err := reencrypt(ManagedEncryptionPurposeOutcomeSummaryExecution, session.record.SummaryExecutionPayload)
	if err != nil {
		return err
	}
	summaryResult, err := reencrypt(ManagedEncryptionPurposeOutcomeSummaryResult, session.record.SummaryResultPayload)
	if err != nil {
		return err
	}
	next, err := reencrypt(ManagedEncryptionPurposeOutcomeNextState, session.record.NextStatePayload)
	if err != nil {
		return err
	}
	err = model.MigrateManagedContextOutcome(ctx, model.ManagedContextOutcomeMigration{
		OutcomeId: session.record.Id, RequestFingerprint: session.fingerprint,
		Previous:        model.ManagedContextOutcomeLookupCandidate{LookupHMAC: previous.LookupHMAC, RevisionIntentHMAC: previous.RevisionIntentHMAC, OwnerHMAC: previous.OwnerHMAC, ConversationHMAC: previous.ConversationHMAC, KeyVersion: previous.KeyVersion},
		Active:          model.ManagedContextOutcomeLookupCandidate{LookupHMAC: active.LookupHMAC, RevisionIntentHMAC: active.RevisionIntentHMAC, OwnerHMAC: active.OwnerHMAC, ConversationHMAC: active.ConversationHMAC, KeyVersion: active.KeyVersion},
		ResponsePayload: response, AssistantPayload: assistant, SummaryExecutionPayload: summary, SummaryResultPayload: summaryResult, NextStatePayload: next,
		PreviousBillingLookupHMAC: previous.BillingLookupHMAC, ActiveBillingLookupHMAC: active.BillingLookupHMAC,
	})
	if err != nil {
		return err
	}
	return session.Reload(ctx)
}

func safeManagedOutcomeContentType(value string) string {
	value = strings.ToLower(strings.TrimSpace(strings.Split(value, ";")[0]))
	switch value {
	case "application/json", "application/problem+json":
		return value
	default:
		return "application/json"
	}
}

func IsManagedOutcomeConflict(err error) bool {
	return errors.Is(err, model.ErrManagedContextOutcomeConflict) || errors.Is(err, model.ErrManagedContextOutcomeLookupConflict)
}

// ConfirmManagedOutcomeCommit compares the exact encrypted Redis revision with
// the database checkpoint. It never advances Redis and is safe after an
// ambiguous database acknowledgement.
func (runtime *ManagedConsensusRuntime) ConfirmManagedOutcomeCommit(ctx context.Context, owner ManagedConsensusOwner, externalContextID string, expectedRevision uint64, candidate ManagedConsensusState) (bool, bool, error) {
	if runtime == nil || runtime.Repository == nil {
		return false, false, fmt.Errorf("managed consensus repository is unavailable")
	}
	keys, err := runtime.conversationStorageKeys(owner, externalContextID)
	if err != nil {
		return false, false, err
	}
	var matchedKey ManagedConversationStorageKey
	var record ManagedConsensusRecord
	existing := 0
	for _, key := range keys {
		loaded, loadErr := runtime.Repository.LoadConsensus(ctx, key)
		if errors.Is(loadErr, ErrManagedConsensusNotFound) {
			continue
		}
		if loadErr != nil {
			return false, false, loadErr
		}
		existing++
		matchedKey, record = key, loaded
	}
	if existing > 1 {
		return false, false, ErrManagedConsensusKeyConflict
	}
	if existing == 0 {
		return false, expectedRevision == 0, nil
	}
	if record.Revision == expectedRevision {
		return false, true, nil
	}
	if record.Revision != candidate.Revision {
		return false, false, nil
	}
	var stored ManagedConsensusState
	if err := runtime.decryptJSON(ctx, ManagedEncryptionContext{RepositoryKey: matchedKey.RepositoryKey, Purpose: ManagedEncryptionPurposeConsensusState, Revision: record.Revision}, record.Payload, &stored); err != nil {
		return false, false, err
	}
	storedJSON, err := common.Marshal(stored)
	if err != nil {
		return false, false, err
	}
	candidateJSON, err := common.Marshal(candidate)
	if err != nil {
		return false, false, err
	}
	return bytes.Equal(storedJSON, candidateJSON), false, nil
}
