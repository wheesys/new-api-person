package contextconsensus

import (
	"testing"

	"github.com/QuantumNous/new-api/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestInjectManagedConsensusPreservesClientBodyAndUsesUserLevelData(t *testing.T) {
	tests := []struct {
		name                string
		protocol            types.RelayFormat
		body                string
		summaryPath         string
		instructionPath     string
		instructionExpected string
		currentPath         string
		currentExpected     string
	}{
		{
			name:                "chat",
			protocol:            types.RelayFormatOpenAI,
			body:                `{"model":"gpt","messages":[{"role":"system","content":"system-safe"},{"role":"user","content":"current-user"}],"tools":[{"type":"function","function":{"name":"lookup"}}]}`,
			summaryPath:         "messages.1.content",
			instructionPath:     "messages.0.content",
			instructionExpected: "system-safe",
			currentPath:         "messages.2.content",
			currentExpected:     "current-user",
		},
		{
			name:                "responses",
			protocol:            types.RelayFormatOpenAIResponses,
			body:                `{"model":"gpt","instructions":"developer-safe","input":[{"type":"message","role":"user","content":[{"type":"input_text","text":"current-user"}]}],"tools":[{"type":"function","name":"lookup"}]}`,
			summaryPath:         "input.0.content.0.text",
			instructionPath:     "instructions",
			instructionExpected: "developer-safe",
			currentPath:         "input.1.content.0.text",
			currentExpected:     "current-user",
		},
		{
			name:                "claude",
			protocol:            types.RelayFormatClaude,
			body:                `{"model":"claude","system":"system-safe","messages":[{"role":"user","content":[{"type":"text","text":"current-user"}]}],"max_tokens":10,"tools":[{"name":"lookup","input_schema":{"type":"object"}}]}`,
			summaryPath:         "messages.0.content.0.text",
			instructionPath:     "system",
			instructionExpected: "system-safe",
			currentPath:         "messages.0.content.1.text",
			currentExpected:     "current-user",
		},
		{
			name:                "gemini",
			protocol:            types.RelayFormatGemini,
			body:                `{"systemInstruction":{"role":"system","parts":[{"text":"system-safe"}]},"contents":[{"role":"user","parts":[{"text":"current-user"}]}],"tools":[{"functionDeclarations":[{"name":"lookup"}]}]}`,
			summaryPath:         "contents.0.parts.0.text",
			instructionPath:     "systemInstruction.parts.0.text",
			instructionExpected: "system-safe",
			currentPath:         "contents.0.parts.1.text",
			currentExpected:     "current-user",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			rewritten, err := InjectManagedConsensus(InjectManagedConsensusRequest{
				Protocol: test.protocol,
				Body:     []byte(test.body),
				State:    managedInjectionTestState(),
			})
			require.NoError(t, err)
			summary := gjson.GetBytes(rewritten, test.summaryPath).String()
			assert.Contains(t, summary, consensusSummaryPreamble)
			assert.Contains(t, summary, `"source_digest":"stored-source"`)
			assert.Equal(t, test.instructionExpected, gjson.GetBytes(rewritten, test.instructionPath).String())
			assert.Equal(t, test.currentExpected, gjson.GetBytes(rewritten, test.currentPath).String())
			assert.True(t, gjson.GetBytes(rewritten, "tools").Exists())
		})
	}
}

func TestInjectManagedConsensusConvertsResponsesStringInputWithoutDroppingIt(t *testing.T) {
	rewritten, err := InjectManagedConsensus(InjectManagedConsensusRequest{
		Protocol: types.RelayFormatOpenAIResponses,
		Body:     []byte(`{"model":"gpt","input":"current-user"}`),
		State:    managedInjectionTestState(),
	})
	require.NoError(t, err)
	assert.Contains(t, gjson.GetBytes(rewritten, "input.0.content.0.text").String(), consensusSummaryPreamble)
	assert.Equal(t, "current-user", gjson.GetBytes(rewritten, "input.1.content.0.text").String())
}

func TestInjectManagedConsensusRejectsInvalidStateOrMissingUserTurn(t *testing.T) {
	invalidState := managedInjectionTestState()
	invalidState.Revision = 0
	_, err := InjectManagedConsensus(InjectManagedConsensusRequest{
		Protocol: types.RelayFormatOpenAI,
		Body:     []byte(`{"messages":[{"role":"user","content":"current"}]}`),
		State:    invalidState,
	})
	require.ErrorContains(t, err, "revision")

	_, err = InjectManagedConsensus(InjectManagedConsensusRequest{
		Protocol: types.RelayFormatOpenAI,
		Body:     []byte(`{"messages":[{"role":"system","content":"safe"}]}`),
		State:    managedInjectionTestState(),
	})
	require.ErrorContains(t, err, "requires a user message")
}

func managedInjectionTestState() ManagedConsensusState {
	return ManagedConsensusState{
		Version:  ManagedConsensusStateVersion,
		Revision: 1,
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
			SourceDigest:        "stored-source",
		},
		SourceDigest:  "stored-source",
		PolicyVersion: "context-consensus-v1",
		CreatedAtUnix: 1_800_000_000,
		UpdatedAtUnix: 1_800_000_000,
	}
}
