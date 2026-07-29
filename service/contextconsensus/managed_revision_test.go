package contextconsensus

import (
	"math"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestManagedRevisionPlanBindsEveryTransitionDimension(t *testing.T) {
	output, err := managedAssistantTextResult(types.RelayFormatOpenAI, "completed", "stop", []byte(`{"choices":[]}`))
	require.NoError(t, err)
	evidence := ManagedRevisionEvidence{
		Protocol:                types.RelayFormatOpenAI,
		IncrementalSourceDigest: "incremental-digest",
		CurrentUserText:         "implement the change",
		AssistantOutput:         output,
		PolicyVersion:           "context-consensus-v1",
	}
	first, err := BuildManagedRevisionPlan(evidence)
	require.NoError(t, err)
	second, err := BuildManagedRevisionPlan(evidence)
	require.NoError(t, err)
	assert.Equal(t, first.SourceDigest, second.SourceDigest)
	assert.Equal(t, "incremental-digest", first.Lineage.IncrementalSourceDigest)
	assert.NotEqual(t, first.Lineage.IncrementalSourceDigest, first.UserRange.SourceDigest)
	assert.Equal(t, output.OutputDigest, first.Lineage.AssistantOutputDigest)

	evidence.PolicyVersion = "context-consensus-v2"
	changed, err := BuildManagedRevisionPlan(evidence)
	require.NoError(t, err)
	assert.NotEqual(t, first.SourceDigest, changed.SourceDigest)
}

func TestManagedRevisionPromptRejectsEvidencePlanMismatch(t *testing.T) {
	output, err := managedAssistantTextResult(types.RelayFormatOpenAI, "completed", "stop", []byte(`{"choices":[]}`))
	require.NoError(t, err)
	evidence := ManagedRevisionEvidence{
		Protocol: types.RelayFormatOpenAI, IncrementalSourceDigest: "incremental-digest",
		CurrentUserText: "implement the change", AssistantOutput: output, PolicyVersion: "context-consensus-v1",
	}
	plan, err := BuildManagedRevisionPlan(evidence)
	require.NoError(t, err)
	evidence.CurrentUserText = "different request"

	_, err = BuildManagedRevisionPrompt("summary-model", evidence, plan, 256)

	require.ErrorContains(t, err, "does not match")
}

func TestManagedRevisionSummaryOnlyAcceptsPreviousFactsOrCurrentEvidence(t *testing.T) {
	confidence := 0.9
	previousRange := SummarySourceRange{StartSequence: 1, EndSequence: 1, SourceDigest: "old-user"}
	previousSummary := emptyConsensusSummary()
	previousSummary.Version = ConsensusSummaryVersion
	previousSummary.SourceDigest = "old-state"
	previousSummary.SourceRanges = []SummarySourceRange{previousRange}
	previousSummary.TaskGoal = []ConsensusFact{{
		Field: "goal", Value: "keep compatibility", Provenance: ConsensusProvenanceUserConfirmed,
		SourceRange: previousRange, SourceDigest: previousRange.SourceDigest, Confidence: &confidence,
	}}
	previousState := &ManagedConsensusState{
		Version: ManagedConsensusStateVersion, Revision: 1, Mode: "managed_consensus",
		TaskConsensus: previousSummary, SourceDigest: "old-state", PolicyVersion: "context-consensus-v1",
		CreatedAtUnix: 1_800_000_000, UpdatedAtUnix: 1_800_000_000,
	}
	output, err := managedAssistantTextResult(types.RelayFormatOpenAI, "done", "stop", []byte(`{"ok":true}`))
	require.NoError(t, err)
	plan, err := BuildManagedRevisionPlan(ManagedRevisionEvidence{
		Protocol: types.RelayFormatOpenAI, PreviousState: previousState,
		IncrementalSourceDigest: "new-user", CurrentUserText: "continue",
		AssistantOutput: output, PolicyVersion: "context-consensus-v1",
	})
	require.NoError(t, err)

	next := previousSummary
	next.SourceDigest = plan.SourceDigest
	next.SourceRanges = append([]SummarySourceRange(nil), plan.SourceRanges...)
	next.CompletedSteps = []ConsensusFact{{
		Field: "result", Value: "done", Provenance: ConsensusProvenanceAssistantInferred,
		SourceRange: plan.AssistantRange, SourceDigest: plan.AssistantRange.SourceDigest, Confidence: &confidence,
	}}
	encoded, err := common.Marshal(next)
	require.NoError(t, err)
	validated, err := ParseAndValidateManagedRevisionSummary(encoded, plan)
	require.NoError(t, err)
	assert.Equal(t, "done", validated.CompletedSteps[0].Value)

	next.TaskGoal = append([]ConsensusFact(nil), next.TaskGoal...)
	next.TaskGoal[0].Value = "silently changed"
	encoded, err = common.Marshal(next)
	require.NoError(t, err)
	_, err = ParseAndValidateManagedRevisionSummary(encoded, plan)
	require.ErrorContains(t, err, "current user evidence")
}

func TestBuildNextManagedConsensusStatePreservesCreationTime(t *testing.T) {
	previous := managedInjectionTestState()
	output, err := managedAssistantTextResult(types.RelayFormatOpenAI, "done", "stop", []byte(`{"ok":true}`))
	require.NoError(t, err)
	plan, err := BuildManagedRevisionPlan(ManagedRevisionEvidence{
		Protocol: types.RelayFormatOpenAI, PreviousState: &previous,
		IncrementalSourceDigest: "new-user", CurrentUserText: "continue",
		AssistantOutput: output, PolicyVersion: "context-consensus-v1",
	})
	require.NoError(t, err)
	summary := emptyConsensusSummary()
	summary.Version = ConsensusSummaryVersion
	summary.SourceDigest = plan.SourceDigest
	summary.SourceRanges = append([]SummarySourceRange(nil), plan.SourceRanges...)
	now := time.Unix(previous.CreatedAtUnix+60, 0)
	state, err := BuildNextManagedConsensusState(plan, summary, now)
	require.NoError(t, err)
	assert.Equal(t, uint64(2), state.Revision)
	assert.Equal(t, previous.CreatedAtUnix, state.CreatedAtUnix)
	assert.Equal(t, now.Unix(), state.UpdatedAtUnix)
	assert.Nil(t, state.ProviderBinding)
	assert.Equal(t, output.OutputDigest, state.Lineage.AssistantOutputDigest)
}

func TestManagedRevisionPlanRejectsRevisionOverflow(t *testing.T) {
	previous := managedInjectionTestState()
	previous.Revision = math.MaxUint64
	output, err := managedAssistantTextResult(types.RelayFormatOpenAI, "done", "stop", []byte(`{"ok":true}`))
	require.NoError(t, err)

	_, err = BuildManagedRevisionPlan(ManagedRevisionEvidence{
		Protocol: types.RelayFormatOpenAI, PreviousState: &previous,
		IncrementalSourceDigest: "new-user", CurrentUserText: "continue",
		AssistantOutput: output, PolicyVersion: "context-consensus-v1",
	})

	require.ErrorContains(t, err, "cannot advance")
}

func TestExtractManagedCurrentUserTextExcludesMediaAndTools(t *testing.T) {
	tests := []struct {
		name     string
		protocol types.RelayFormat
		body     string
		want     string
	}{
		{name: "chat", protocol: types.RelayFormatOpenAI, body: `{"messages":[{"role":"user","content":[{"type":"text","text":"chat text"},{"type":"image_url","image_url":{"url":"data:image/png;base64,AAAA"}}]}],"tools":[{"type":"function","function":{"name":"lookup"}}]}`, want: "chat text"},
		{name: "responses", protocol: types.RelayFormatOpenAIResponses, body: `{"input":[{"type":"message","role":"user","content":[{"type":"input_text","text":"responses text"},{"type":"input_image","image_url":"data:image/png;base64,AAAA"}]}]}`, want: "responses text"},
		{name: "claude", protocol: types.RelayFormatClaude, body: `{"messages":[{"role":"user","content":[{"type":"text","text":"claude text"},{"type":"image","source":{"type":"base64","media_type":"image/png","data":"AAAA"}}]}]}`, want: "claude text"},
		{name: "gemini", protocol: types.RelayFormatGemini, body: `{"contents":[{"role":"user","parts":[{"text":"gemini text"},{"inlineData":{"mimeType":"image/png","data":"AAAA"}}]}]}`, want: "gemini text"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			text, err := ExtractManagedCurrentUserText(test.protocol, []byte(test.body))
			require.NoError(t, err)
			assert.Equal(t, test.want, text)
			assert.NotContains(t, text, "AAAA")
			assert.NotContains(t, text, "lookup")
		})
	}
}
