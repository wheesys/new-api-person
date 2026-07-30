package contextconsensus

import (
	"bytes"
	"context"
	"encoding/base64"
	"math"
	"strings"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/types"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func prepareManagedOutcomeServiceTest(t *testing.T) {
	t.Helper()
	originalDB := model.DB
	database, err := gorm.Open(sqlite.Open("file:"+t.Name()+"?mode=memory&cache=shared"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, database.AutoMigrate(&model.ManagedContextOutcome{}, &model.BillingOperation{}))
	model.DB = database
	t.Cleanup(func() { model.DB = originalDB })
}

func managedOutcomeServiceRequest() ManagedOutcomeRequest {
	return ManagedOutcomeRequest{
		Owner:             ManagedConsensusOwner{UserID: 7, TokenID: 11, EndpointFamily: "chat"},
		ExternalContextID: "opaque-context", IdempotencyKey: "stable-request-key-123456",
		ExpectedRevision: 3, Protocol: types.RelayFormatOpenAI,
		IncrementalSourceDigest: "body-digest", PolicyVersion: "context-consensus-v1", TTL: time.Hour,
	}
}

func managedOutcomeSummarySnapshot(t *testing.T, assistantText, assistantDigest string) ManagedSummaryExecutionSnapshot {
	t.Helper()
	evidence := ManagedRevisionEvidence{
		Protocol: types.RelayFormatOpenAI, IncrementalSourceDigest: "body-digest", CurrentUserText: "current",
		AssistantOutput: ManagedAssistantOutput{Protocol: types.RelayFormatOpenAI, Text: assistantText, OutputDigest: assistantDigest},
		PolicyVersion:   "context-consensus-v1",
	}
	plan, err := BuildManagedRevisionPlan(evidence)
	require.NoError(t, err)
	return ManagedSummaryExecutionSnapshot{
		Evidence: evidence,
		Plan:     plan, SummaryRequest: &dto.GeneralOpenAIRequest{},
		CompactionModel: "summary-model", ModelPool: []string{"summary-model"}, AllowedChannelIDs: []int{1},
		MaximumSummaryTokens: 64, MaximumInputTokens: 1024, MaximumQuota: 100, TimeoutSeconds: 30,
	}
}

func TestManagedOutcomeEncryptsPayloadAndEnforcesResponseLimit(t *testing.T) {
	prepareManagedOutcomeServiceTest(t)
	runtime, err := newManagedConsensusRuntime([]byte(strings.Repeat("a", 32)), "v1", nil, nil)
	require.NoError(t, err)
	session, err := ReserveManagedOutcome(context.Background(), runtime, managedOutcomeServiceRequest())
	require.NoError(t, err)
	require.NoError(t, session.MarkMainDispatched(context.Background()))
	secretBody := []byte(`{"message":"private-plaintext"}`)
	summarySnapshot := managedOutcomeSummarySnapshot(t, "private-assistant", "assistant-digest")
	checkpoint, err := session.MainCheckpoint(context.Background(), ManagedOutcomeResponse{Status: 200, ContentType: "application/json; charset=utf-8", Body: secretBody}, ManagedAssistantOutput{
		Protocol: types.RelayFormatOpenAI, Text: "private-assistant", OutputDigest: "assistant-digest",
	}, summarySnapshot)
	require.NoError(t, err)
	assert.NotContains(t, string(checkpoint.ResponsePayload), "private-plaintext")
	assert.NotContains(t, string(checkpoint.AssistantPayload), "private-assistant")
	assert.Equal(t, "application/json", checkpoint.ResponseContentType)
	require.NoError(t, model.DB.Model(&model.ManagedContextOutcome{}).Where("id = ?", session.ID()).Updates(map[string]interface{}{
		"phase": model.ManagedContextOutcomePhaseMainSettled, "response_payload": checkpoint.ResponsePayload,
		"assistant_payload": checkpoint.AssistantPayload, "summary_execution_payload": checkpoint.SummaryExecutionPayload,
	}).Error)
	require.NoError(t, session.Reload(context.Background()))
	restoredSnapshot, err := session.SummaryExecution(context.Background())
	require.NoError(t, err)
	assert.True(t, restoredSnapshot.Plan.hasCurrentUserText)
	assert.Equal(t, summarySnapshot.Plan.SourceDigest, restoredSnapshot.Plan.SourceDigest)

	_, err = session.MainCheckpoint(context.Background(), ManagedOutcomeResponse{Status: 200, ContentType: "text/plain", Body: make([]byte, ManagedOutcomeMaximumResponseBytes+1)}, ManagedAssistantOutput{}, managedOutcomeSummarySnapshot(t, "assistant", "assistant-digest"))
	require.Error(t, err)
}

func TestManagedOutcomeRejectsRevisionOutsideCrossDatabaseRange(t *testing.T) {
	prepareManagedOutcomeServiceTest(t)
	runtime, err := newManagedConsensusRuntime([]byte(strings.Repeat("a", 32)), "v1", nil, nil)
	require.NoError(t, err)
	request := managedOutcomeServiceRequest()
	request.ExpectedRevision = math.MaxInt64

	_, err = ReserveManagedOutcome(context.Background(), runtime, request)
	require.Error(t, err)
	var count int64
	require.NoError(t, model.DB.Model(&model.ManagedContextOutcome{}).Count(&count).Error)
	assert.Zero(t, count)
}

func TestManagedOutcomeKeyRotationReencryptsWithoutExtendingExpiry(t *testing.T) {
	prepareManagedOutcomeServiceTest(t)
	oldKey := []byte(strings.Repeat("o", 32))
	newKey := []byte(strings.Repeat("n", 32))
	oldRuntime, err := newManagedConsensusRuntime(oldKey, "old", nil, nil)
	require.NoError(t, err)
	request := managedOutcomeServiceRequest()
	oldSession, err := ReserveManagedOutcome(context.Background(), oldRuntime, request)
	require.NoError(t, err)
	require.NoError(t, oldSession.MarkMainDispatched(context.Background()))
	checkpoint, err := oldSession.MainCheckpoint(context.Background(), ManagedOutcomeResponse{Status: 200, ContentType: "application/json", Body: []byte(`{"ok":true}`)}, ManagedAssistantOutput{
		Protocol: types.RelayFormatOpenAI, Text: "assistant", OutputDigest: "digest",
	}, managedOutcomeSummarySnapshot(t, "assistant", "digest"))
	require.NoError(t, err)
	require.NoError(t, model.DB.Model(&model.ManagedContextOutcome{}).Where("id = ?", oldSession.ID()).Updates(map[string]interface{}{
		"phase": model.ManagedContextOutcomePhaseMainSettled, "response_status": 200,
		"response_content_type": "application/json", "response_payload": checkpoint.ResponsePayload,
		"assistant_payload":         checkpoint.AssistantPayload,
		"summary_execution_payload": checkpoint.SummaryExecutionPayload,
	}).Error)
	require.NoError(t, oldSession.Reload(context.Background()))
	originalExpiry := oldSession.Record().ExpiresAt
	oldStorage := oldSession.storageKeys[0]
	billingOperation := &model.BillingOperation{
		LookupHMAC: oldStorage.BillingLookupHMAC, OwnerHMAC: oldStorage.OwnerHMAC, ConversationHMAC: oldStorage.ConversationHMAC,
		LookupKeyVersion: oldStorage.KeyVersion, ExpectedRevision: request.ExpectedRevision, Purpose: "main",
		Fingerprint: "billing-fingerprint", State: model.BillingOperationStateSettled, UserId: 7, TokenId: 11, ChannelId: 13,
		PricingSnapshot: `{}`, BillingMode: "fixed",
	}
	require.NoError(t, model.DB.Create(billingOperation).Error)

	previous := []managedConsensusPreviousKey{{Version: "old", Key: base64.StdEncoding.EncodeToString(oldKey)}}
	rotatedRuntime, err := newManagedConsensusRuntime(newKey, "new", previous, nil)
	require.NoError(t, err)
	rotated, err := ReserveManagedOutcome(context.Background(), rotatedRuntime, request)
	require.NoError(t, err)
	assert.Equal(t, "new", rotated.Record().LookupKeyVersion)
	assert.Equal(t, originalExpiry.UnixNano(), rotated.Record().ExpiresAt.UnixNano())
	response, err := rotated.Response(context.Background())
	require.NoError(t, err)
	assert.JSONEq(t, `{"ok":true}`, string(response.Body))
	assert.NotEqual(t, checkpoint.ResponsePayload, rotated.Record().ResponsePayload)
	require.NoError(t, model.DB.First(billingOperation, billingOperation.Id).Error)
	assert.Equal(t, rotated.storageKeys[0].BillingLookupHMAC, billingOperation.LookupHMAC)
	assert.Equal(t, "new", billingOperation.LookupKeyVersion)
}

func TestManagedOutcomeKeyRotationDualLookupFailsClosed(t *testing.T) {
	prepareManagedOutcomeServiceTest(t)
	request := managedOutcomeServiceRequest()
	oldKey := []byte(strings.Repeat("o", 32))
	newKey := []byte(strings.Repeat("n", 32))
	oldRuntime, err := newManagedConsensusRuntime(oldKey, "old", nil, nil)
	require.NoError(t, err)
	_, err = ReserveManagedOutcome(context.Background(), oldRuntime, request)
	require.NoError(t, err)
	newRuntime, err := newManagedConsensusRuntime(newKey, "new", nil, nil)
	require.NoError(t, err)
	_, err = ReserveManagedOutcome(context.Background(), newRuntime, request)
	require.NoError(t, err)
	combinedRuntime, err := newManagedConsensusRuntime(newKey, "new", []managedConsensusPreviousKey{{Version: "old", Key: base64.StdEncoding.EncodeToString(oldKey)}}, nil)
	require.NoError(t, err)
	_, err = ReserveManagedOutcome(context.Background(), combinedRuntime, request)
	assert.ErrorIs(t, err, model.ErrManagedContextOutcomeLookupConflict)
}

func TestConfirmManagedOutcomeCommitMatchesOnlyExactNextState(t *testing.T) {
	prepareManagedOutcomeServiceTest(t)
	now := time.Unix(1_800_000_000, 0)
	repository := newMemoryManagedConsensusRepository(func() time.Time { return now })
	runtime, err := newManagedConsensusRuntime([]byte(strings.Repeat("c", 32)), "v1", nil, repository)
	require.NoError(t, err)
	request := managedOutcomeServiceRequest()
	state := managedSessionTestState(request.ExpectedRevision+1, now)
	keys, err := runtime.conversationStorageKeys(request.Owner, request.ExternalContextID)
	require.NoError(t, err)
	payload, err := runtime.Cipher.EncryptJSON(context.Background(), ManagedEncryptionContext{
		RepositoryKey: keys[0].RepositoryKey, Purpose: ManagedEncryptionPurposeConsensusState, Revision: state.Revision,
	}, state)
	require.NoError(t, err)
	repository.consensusRecords[keys[0].RepositoryKey] = ManagedConsensusRecord{Revision: state.Revision, Payload: payload, ExpiresAt: now.Add(time.Hour)}
	committed, currentAtExpected, err := runtime.ConfirmManagedOutcomeCommit(context.Background(), request.Owner, request.ExternalContextID, request.ExpectedRevision, state, nil)
	require.NoError(t, err)
	assert.True(t, committed)
	assert.False(t, currentAtExpected)
	changed := state
	changed.SourceDigest = "different"
	committed, _, err = runtime.ConfirmManagedOutcomeCommit(context.Background(), request.Owner, request.ExternalContextID, request.ExpectedRevision, changed, nil)
	require.NoError(t, err)
	assert.False(t, committed)
	assert.False(t, bytes.Equal(mustManagedJSON(t, state), mustManagedJSON(t, changed)))
}

func TestConfirmManagedOutcomeCommitRequiresExactProviderState(t *testing.T) {
	prepareManagedOutcomeServiceTest(t)
	now := time.Unix(1_800_000_000, 0)
	repository := newMemoryManagedConsensusRepository(func() time.Time { return now })
	runtime, err := newManagedConsensusRuntime([]byte(strings.Repeat("c", 32)), "v1", nil, repository)
	require.NoError(t, err)
	request := managedOutcomeServiceRequest()
	conversationKeys, err := runtime.conversationStorageKeys(request.Owner, request.ExternalContextID)
	require.NoError(t, err)
	providerKey, err := runtime.KeyDeriver.DeriveProviderStateStorageKey(request.Owner, "resp_confirmed_state")
	require.NoError(t, err)
	target := ManagedProviderTargetBinding{
		BindingLevel: BindingLevelCredential, RelayFormat: types.RelayFormatOpenAIResponses,
		ChannelID: 7, ChannelType: 1, OriginModel: "gpt-public", UpstreamModel: "gpt-upstream",
		MultiKeyIndex: 0, CredentialFingerprint: "credential-hmac", FingerprintKeyVersion: "v1",
		ReasonCodes: []string{managedOpenAIResponseStateReason},
	}
	binding := ManagedProviderStateBinding{
		Version: ManagedConsensusStateVersion, OwnerHMAC: providerKey.OwnerHMAC,
		ConversationHMAC: conversationKeys[0].ConversationHMAC, ProducedRevision: request.ExpectedRevision + 1,
		StateReferenceHMAC: providerKey.StateReferenceHMAC, Target: target,
		CreatedAtUnix: now.Unix(), ExpiresAtUnix: now.Add(time.Hour).Unix(),
	}
	providerPayload, err := runtime.Cipher.EncryptJSON(context.Background(), ManagedEncryptionContext{
		RepositoryKey: providerKey.RepositoryKey, Purpose: ManagedEncryptionPurposeProviderState, Revision: 1,
	}, binding)
	require.NoError(t, err)
	encodedBinding, err := common.Marshal(binding)
	require.NoError(t, err)
	providerCommit := ManagedProviderStateCommit{
		StateReference: "resp_confirmed_state", StorageKey: providerKey, Binding: binding,
		BindingDigest: digestBytes(encodedBinding), Payload: providerPayload, ExpiresAtUnix: binding.ExpiresAtUnix,
	}
	state := managedSessionTestState(request.ExpectedRevision+1, now)
	state.ProviderBinding = &target
	link := providerCommit.Link()
	state.ProviderState = &link
	consensusPayload, err := runtime.Cipher.EncryptJSON(context.Background(), ManagedEncryptionContext{
		RepositoryKey: conversationKeys[0].RepositoryKey, Purpose: ManagedEncryptionPurposeConsensusState, Revision: state.Revision,
	}, state)
	require.NoError(t, err)
	repository.consensusRecords[conversationKeys[0].RepositoryKey] = ManagedConsensusRecord{
		Revision: state.Revision, Payload: consensusPayload, ExpiresAt: now.Add(time.Hour),
	}
	repository.providerStateBindings[providerKey.RepositoryKey] = ManagedProviderStateRecord{
		BindingDigest: providerCommit.BindingDigest, Payload: providerPayload, ExpiresAt: now.Add(time.Hour),
	}

	committed, currentAtExpected, err := runtime.ConfirmManagedOutcomeCommit(
		context.Background(), request.Owner, request.ExternalContextID, request.ExpectedRevision, state, &providerCommit,
	)
	require.NoError(t, err)
	assert.True(t, committed)
	assert.False(t, currentAtExpected)

	delete(repository.providerStateBindings, providerKey.RepositoryKey)
	_, _, err = runtime.ConfirmManagedOutcomeCommit(
		context.Background(), request.Owner, request.ExternalContextID, request.ExpectedRevision, state, &providerCommit,
	)
	require.ErrorIs(t, err, ErrProviderStateBindingNotFound)
}

func mustManagedJSON(t *testing.T, value any) []byte {
	t.Helper()
	encoded, err := common.Marshal(value)
	require.NoError(t, err)
	return encoded
}
