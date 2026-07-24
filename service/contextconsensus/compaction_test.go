package contextconsensus

import (
	"testing"

	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBuildCompactionPlanCoversOnlyEarlyCompleteTextTurns(t *testing.T) {
	body := []byte(`{
  "model":"gpt-5",
  "messages":[
    {"role":"system","content":"Keep policy."},
    {"role":"developer","content":"Return concise output."},
    {"role":"user","content":"old user one"},
    {"role":"assistant","content":"old assistant one"},
    {"role":"user","content":"old user two"},
    {"role":"assistant","content":"old assistant two"},
    {"role":"user","content":"recent user"},
    {"role":"assistant","content":"recent assistant"},
    {"role":"user","content":"current user"}
  ],
  "response_format":{"type":"json_schema","json_schema":{"name":"answer","schema":{"type":"object"}}}
}`)
	envelope, err := Extract(ExtractionRequest{Protocol: types.RelayFormatOpenAI, Body: body})
	require.NoError(t, err)

	plan, err := BuildCompactionPlan(CompactionPlanRequest{
		Protocol: types.RelayFormatOpenAI,
		Body:     body,
		Envelope: envelope,
		Policy:   enabledCompactionPolicy(1),
	})
	require.NoError(t, err)
	assert.Equal(t, []int{2, 3, 4, 5}, segmentSequences(plan.CoveredSegments))
	assert.Equal(t, 2, plan.SummaryInsertBefore)
	assert.Len(t, plan.CoveredRanges, 2)
	assert.Len(t, plan.ImmutableSegments, 2)
	assert.Contains(t, segmentPaths(plan.PreservedSegments), "messages[6]")
	assert.Contains(t, segmentPaths(plan.PreservedSegments), "messages[7]")
	assert.Contains(t, segmentPaths(plan.PreservedSegments), "messages[8]")
	assert.Contains(t, segmentPaths(plan.PreservedSegments), "response_format")
}

func TestBuildCompactionPlanRejectsUnsafeState(t *testing.T) {
	body := []byte(`{
  "model":"gpt-5",
  "messages":[
    {"role":"user","content":"old"},
    {"role":"assistant","content":"answer"},
    {"role":"user","content":"recent"},
    {"role":"assistant","content":"answer"},
    {"role":"user","content":"current"}
  ]
}`)
	baseEnvelope, err := Extract(ExtractionRequest{Protocol: types.RelayFormatOpenAI, Body: body})
	require.NoError(t, err)

	tests := []struct {
		name          string
		mutate        func(*ContextEnvelope)
		errorContains string
	}{
		{
			name: "provider state",
			mutate: func(envelope *ContextEnvelope) {
				envelope.ProviderBinding.BindingLevel = BindingLevelProvider
			},
			errorContains: "provider-bound context",
		},
		{
			name: "tool definition",
			mutate: func(envelope *ContextEnvelope) {
				envelope.ToolState.SchemaDigest = "tool-schema-digest"
			},
			errorContains: "tool context",
		},
		{
			name: "media",
			mutate: func(envelope *ContextEnvelope) {
				envelope.MediaState.ImageCount = 1
			},
			errorContains: "media context",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			envelopeCopy := *baseEnvelope
			test.mutate(&envelopeCopy)
			_, buildErr := BuildCompactionPlan(CompactionPlanRequest{
				Protocol: types.RelayFormatOpenAI,
				Body:     body,
				Envelope: &envelopeCopy,
				Policy:   enabledCompactionPolicy(1),
			})
			require.ErrorContains(t, buildErr, test.errorContains)
		})
	}
}

func enabledCompactionPolicy(preservedRecentTurns int) CompactionPolicySnapshot {
	return CompactionPolicy{
		SystemEnabled:        true,
		PolicyVersion:        "context-compaction-v1",
		PreservedRecentTurns: preservedRecentTurns,
		TargetInputTokens:    4000,
		MaxSummaryTokens:     500,
	}.Snapshot(true, true)
}

func segmentSequences(segments []ContextSegment) []int {
	sequences := make([]int, 0, len(segments))
	for _, segment := range segments {
		sequences = append(sequences, segment.Sequence)
	}
	return sequences
}

func segmentPaths(segments []ContextSegment) []string {
	paths := make([]string, 0, len(segments))
	for _, segment := range segments {
		paths = append(paths, segment.Path)
	}
	return paths
}
