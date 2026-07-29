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
	summary := contextconsensus.ConsensusSummary{
		Version:  contextconsensus.ConsensusSummaryVersion,
		TaskGoal: []contextconsensus.ConsensusFact{}, Decisions: []contextconsensus.ConsensusFact{},
		MustPreserve: []contextconsensus.ConsensusFact{}, OpenQuestions: []contextconsensus.ConsensusFact{},
		UserPreferences: []contextconsensus.ConsensusFact{}, DomainTerms: map[string]contextconsensus.ConsensusFact{},
		CompletedSteps: []contextconsensus.ConsensusFact{}, PendingSteps: []contextconsensus.ConsensusFact{},
		ArtifactRefs: []contextconsensus.ConsensusFact{}, ToolResultSummaries: []contextconsensus.ConsensusFact{},
		SourceRanges: append([]contextconsensus.SummarySourceRange(nil), plan.SourceRanges...), SourceDigest: plan.SourceDigest,
	}
	return plan, summary
}
