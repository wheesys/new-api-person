package controller

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/service/contextconsensus"
	"github.com/QuantumNous/new-api/types"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type managedCommitBarrierStub struct {
	commitError         error
	state               contextconsensus.ManagedConsensusState
	providerCommit      *contextconsensus.ManagedProviderStateCommit
	clientBodyAtCommit  func() int
	observedClientBytes int
}

func (stub *managedCommitBarrierStub) CommitWithRecovery(_ context.Context, state contextconsensus.ManagedConsensusState, _ time.Duration) (contextconsensus.ManagedConsensusCommitResult, error) {
	stub.state = state
	if stub.clientBodyAtCommit != nil {
		stub.observedClientBytes = stub.clientBodyAtCommit()
	}
	return contextconsensus.ManagedConsensusCommitResult{}, stub.commitError
}

func (stub *managedCommitBarrierStub) CommitWithProviderStateRecovery(_ context.Context, state contextconsensus.ManagedConsensusState, providerCommit contextconsensus.ManagedProviderStateCommit, _ time.Duration) (contextconsensus.ManagedConsensusCommitResult, error) {
	stub.state = state
	stub.providerCommit = &providerCommit
	if stub.clientBodyAtCommit != nil {
		stub.observedClientBytes = stub.clientBodyAtCommit()
	}
	return contextconsensus.ManagedConsensusCommitResult{}, stub.commitError
}

func TestCommitAndFlushManagedRevisionWritesOnlyAfterCAS(t *testing.T) {
	recorder := httptest.NewRecorder()
	contextValue, _ := gin.CreateTestContext(recorder)
	contextValue.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	buffer, err := newManagedResponseBuffer(contextValue.Writer, 4096)
	require.NoError(t, err)
	_, err = buffer.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"done"},"finish_reason":"stop"}]}`))
	require.NoError(t, err)
	plan, summary := managedCommitBarrierFixture(t)
	committer := &managedCommitBarrierStub{clientBodyAtCommit: recorder.Body.Len}

	newAPIError := commitAndFlushManagedRevision(contextValue, committer, plan, summary, buffer, time.Hour, time.Unix(1_800_000_000, 0))

	require.Nil(t, newAPIError)
	assert.Equal(t, uint64(1), committer.state.Revision)
	assert.Zero(t, committer.observedClientBytes)
	assert.Equal(t, "1", recorder.Header().Get("X-New-Api-Context-Revision"))
	assert.Contains(t, recorder.Body.String(), `"content":"done"`)
}

func TestCommitAndFlushManagedRevisionDiscardsBufferedResponseOnCASFailure(t *testing.T) {
	recorder := httptest.NewRecorder()
	contextValue, _ := gin.CreateTestContext(recorder)
	contextValue.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	buffer, err := newManagedResponseBuffer(contextValue.Writer, 4096)
	require.NoError(t, err)
	_, err = buffer.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"must stay hidden"},"finish_reason":"stop"}]}`))
	require.NoError(t, err)
	plan, summary := managedCommitBarrierFixture(t)
	committer := &managedCommitBarrierStub{commitError: contextconsensus.ErrManagedConsensusCommitFailed}

	newAPIError := commitAndFlushManagedRevision(contextValue, committer, plan, summary, buffer, time.Hour, time.Unix(1_800_000_000, 0))

	require.NotNil(t, newAPIError)
	assert.Equal(t, http.StatusServiceUnavailable, newAPIError.StatusCode)
	assert.Equal(t, types.ErrorCodeManagedContextCommitFailed, newAPIError.GetErrorCode())
	assert.Empty(t, recorder.Body.Bytes())
	assert.Empty(t, recorder.Header().Get("X-New-Api-Context-Revision"))
}

func TestCommitAndFlushManagedRevisionUsesAtomicProviderStateCommit(t *testing.T) {
	recorder := httptest.NewRecorder()
	contextValue, _ := gin.CreateTestContext(recorder)
	contextValue.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
	buffer, err := newManagedResponseBuffer(contextValue.Writer, 4096)
	require.NoError(t, err)
	_, err = buffer.Write([]byte(`{"id":"resp_atomic","object":"response","status":"completed"}`))
	require.NoError(t, err)
	providerCommit := managedCommitBarrierProviderStateFixture()
	plan, err := contextconsensus.BuildManagedRevisionPlan(contextconsensus.ManagedRevisionEvidence{
		Protocol: types.RelayFormatOpenAIResponses, IncrementalSourceDigest: "incremental", CurrentUserText: "continue",
		AssistantOutput: contextconsensus.ManagedAssistantOutput{
			Protocol: types.RelayFormatOpenAIResponses, Text: "done", FinishReason: "completed", OutputDigest: "assistant-output",
		},
		PolicyVersion: "context-consensus-v1", ProviderStateCommit: &providerCommit,
	})
	require.NoError(t, err)
	summary := managedCommitBarrierSummary(plan)
	committer := &managedCommitBarrierStub{clientBodyAtCommit: recorder.Body.Len}

	newAPIError := commitAndFlushManagedRevision(contextValue, committer, plan, summary, buffer, time.Hour, time.Unix(1_800_000_000, 0))

	require.Nil(t, newAPIError)
	require.NotNil(t, committer.providerCommit)
	assert.Equal(t, providerCommit.BindingDigest, committer.providerCommit.BindingDigest)
	assert.Equal(t, providerCommit.Link(), *committer.state.ProviderState)
	assert.Zero(t, committer.observedClientBytes)
}

func managedCommitBarrierFixture(t *testing.T) (contextconsensus.ManagedRevisionPlan, contextconsensus.ConsensusSummary) {
	t.Helper()
	plan, err := contextconsensus.BuildManagedRevisionPlan(contextconsensus.ManagedRevisionEvidence{
		Protocol:                types.RelayFormatOpenAI,
		IncrementalSourceDigest: "incremental",
		CurrentUserText:         "continue",
		AssistantOutput: contextconsensus.ManagedAssistantOutput{
			Protocol: types.RelayFormatOpenAI, Text: "done", FinishReason: "stop", OutputDigest: "assistant-output",
		},
		PolicyVersion: "context-consensus-v1",
	})
	require.NoError(t, err)
	return plan, managedCommitBarrierSummary(plan)
}

func managedCommitBarrierSummary(plan contextconsensus.ManagedRevisionPlan) contextconsensus.ConsensusSummary {
	return contextconsensus.ConsensusSummary{
		Version:  contextconsensus.ConsensusSummaryVersion,
		TaskGoal: []contextconsensus.ConsensusFact{}, Decisions: []contextconsensus.ConsensusFact{},
		MustPreserve: []contextconsensus.ConsensusFact{}, OpenQuestions: []contextconsensus.ConsensusFact{},
		UserPreferences: []contextconsensus.ConsensusFact{}, DomainTerms: map[string]contextconsensus.ConsensusFact{},
		CompletedSteps: []contextconsensus.ConsensusFact{}, PendingSteps: []contextconsensus.ConsensusFact{},
		ArtifactRefs: []contextconsensus.ConsensusFact{}, ToolResultSummaries: []contextconsensus.ConsensusFact{},
		SourceRanges: append([]contextconsensus.SummarySourceRange(nil), plan.SourceRanges...), SourceDigest: plan.SourceDigest,
	}
}

func managedCommitBarrierProviderStateFixture() contextconsensus.ManagedProviderStateCommit {
	target := contextconsensus.ManagedProviderTargetBinding{
		BindingLevel: contextconsensus.BindingLevelCredential, RelayFormat: types.RelayFormatOpenAIResponses,
		ChannelID: 7, ChannelType: 1, OriginModel: "gpt-public", UpstreamModel: "gpt-upstream",
		MultiKeyIndex: 0, CredentialFingerprint: "credential-hmac", FingerprintKeyVersion: "v1",
		ReasonCodes: []string{"responses_previous_response_id"},
	}
	binding := contextconsensus.ManagedProviderStateBinding{
		Version: contextconsensus.ManagedConsensusStateVersion, OwnerHMAC: "owner-hmac", ConversationHMAC: "conversation-hmac",
		ProducedRevision: 1, StateReferenceHMAC: "state-hmac", Target: target,
		CreatedAtUnix: 1_800_000_000, ExpiresAtUnix: 1_800_003_600,
	}
	return contextconsensus.ManagedProviderStateCommit{
		StateReference: "resp_atomic", StorageKey: contextconsensus.ManagedProviderStateStorageKey{
			RepositoryKey: "new-api:context_consensus:provider_state:v1:owner-hmac:state-hmac",
			OwnerHMAC:     "owner-hmac", StateReferenceHMAC: "state-hmac", KeyVersion: "v1",
		},
		Binding: binding, BindingDigest: "binding-digest", ExpiresAtUnix: binding.ExpiresAtUnix,
		Payload: contextconsensus.ManagedEncryptedEnvelope{
			Version: 1, Algorithm: "AES-256-GCM", KeyVersion: "v1", Purpose: contextconsensus.ManagedEncryptionPurposeProviderState,
			Revision: 1, Nonce: "nonce", Ciphertext: "ciphertext",
		},
	}
}
