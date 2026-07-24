package contextconsensus

import (
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseAndValidateConsensusSummaryV1EnforcesSourcesAndProvenance(t *testing.T) {
	plan := testCompactionPlan(t, types.RelayFormatOpenAI, chatRewriteBody)
	summary := validConsensusSummary(t, plan)
	body, err := common.Marshal(summary)
	require.NoError(t, err)

	parsed, err := ParseAndValidateConsensusSummaryV1(body, plan)
	require.NoError(t, err)
	assert.Equal(t, ConsensusSummaryVersion, parsed.Version)
	assert.Equal(t, plan.SourceDigest, parsed.SourceDigest)
	assert.Equal(t, plan.CoveredRanges, parsed.SourceRanges)
	assert.Equal(t, ConsensusProvenanceUserConfirmed, parsed.TaskGoal[0].Provenance)
}

func TestParseAndValidateConsensusSummaryV1RejectsUnsafeShape(t *testing.T) {
	plan := testCompactionPlan(t, types.RelayFormatOpenAI, chatRewriteBody)

	tests := []struct {
		name          string
		mutate        func(map[string]any)
		errorContains string
	}{
		{
			name: "extra field",
			mutate: func(value map[string]any) {
				value["unexpected"] = true
			},
			errorContains: "unsupported field",
		},
		{
			name: "hidden reasoning field",
			mutate: func(value map[string]any) {
				value["analysis"] = "private reasoning"
			},
			errorContains: "forbidden hidden reasoning field",
		},
		{
			name: "nested hidden reasoning field",
			mutate: func(value map[string]any) {
				facts := value["task_goal"].([]any)
				facts[0].(map[string]any)["thoughts"] = "private reasoning"
			},
			errorContains: "forbidden hidden reasoning field",
		},
		{
			name: "wrong version type",
			mutate: func(value map[string]any) {
				value["version"] = "1"
			},
			errorContains: "summary.version must be a JSON number",
		},
		{
			name: "null string",
			mutate: func(value map[string]any) {
				value["current_phase"] = nil
			},
			errorContains: "summary.current_phase must be a JSON string",
		},
		{
			name: "source digest mismatch",
			mutate: func(value map[string]any) {
				value["source_digest"] = "different"
			},
			errorContains: "source digest does not match",
		},
		{
			name: "authority escalation",
			mutate: func(value map[string]any) {
				facts := value["task_goal"].([]any)
				facts[0].(map[string]any)["provenance"] = string(ConsensusProvenancePolicy)
			},
			errorContains: "policy provenance cannot cite",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			rawSummary, marshalErr := common.Marshal(validConsensusSummary(t, plan))
			require.NoError(t, marshalErr)
			var value map[string]any
			require.NoError(t, common.Unmarshal(rawSummary, &value))
			test.mutate(value)
			mutated, marshalErr := common.Marshal(value)
			require.NoError(t, marshalErr)
			_, validationErr := ParseAndValidateConsensusSummaryV1(mutated, plan)
			require.ErrorContains(t, validationErr, test.errorContains)
		})
	}
}

func validConsensusSummary(t *testing.T, plan CompactionPlan) ConsensusSummary {
	t.Helper()
	userRange, err := NewSummarySourceRange(plan, plan.CoveredSegments[0].Sequence, plan.CoveredSegments[0].Sequence)
	require.NoError(t, err)
	confidence := 1.0
	return ConsensusSummary{
		Version: ConsensusSummaryVersion,
		TaskGoal: []ConsensusFact{{
			Field:        "task_goal",
			Value:        "finish the requested task",
			Provenance:   ConsensusProvenanceUserConfirmed,
			SourceRange:  userRange,
			SourceDigest: userRange.SourceDigest,
			Confidence:   &confidence,
		}},
		CurrentPhase:        "execution",
		Decisions:           []ConsensusFact{},
		MustPreserve:        []ConsensusFact{},
		OpenQuestions:       []ConsensusFact{},
		UserPreferences:     []ConsensusFact{},
		DomainTerms:         map[string]ConsensusFact{},
		CompletedSteps:      []ConsensusFact{},
		PendingSteps:        []ConsensusFact{},
		ArtifactRefs:        []ConsensusFact{},
		ToolResultSummaries: []ConsensusFact{},
		SourceRanges:        append([]SummarySourceRange(nil), plan.CoveredRanges...),
		SourceDigest:        plan.SourceDigest,
	}
}
