package contextconsensus

import (
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestStaticToolSanitizationPolicyProviderSelectsOneActivePolicy(t *testing.T) {
	result := `{"status":"ok","count":2,"active":true}`
	evidence := toolSanitizationEvidenceForTest(result)
	policy := validToolSanitizationPolicyForTest(evidence)
	provider, err := NewStaticToolSanitizationPolicyProvider([]ToolResultSanitizationPolicy{policy})
	require.NoError(t, err)

	projection, err := provider.Sanitize(evidence, result)
	require.NoError(t, err)
	require.NoError(t, projection.Validate())
	assert.Equal(t, policy.Version, projection.PolicyVersion())

	secondPolicy := policy
	secondPolicy.Version = "tool-policy-v2"
	_, err = NewStaticToolSanitizationPolicyProvider([]ToolResultSanitizationPolicy{policy, secondPolicy})
	require.ErrorIs(t, err, ErrToolSanitizationPolicyInvalid)

	missingProvider, err := NewStaticToolSanitizationPolicyProvider(nil)
	require.NoError(t, err)
	_, err = missingProvider.Sanitize(evidence, result)
	require.ErrorIs(t, err, ErrToolSanitizationPolicyNotFound)
}

func TestToolCompactionV2PromptAndRewriteKeepRawToolContextSealed(t *testing.T) {
	body, plan := toolCompactionV2Fixture(t, 1, true)
	require.Equal(t, ConsensusSummaryVersionV2, plan.SummaryVersion)
	require.NotNil(t, plan.ToolAtomicRange)
	assert.Equal(t, SummarySourceRange{StartSequence: 2, EndSequence: 5, SourceDigest: plan.ToolAtomicRange.SourceDigest}, *plan.ToolAtomicRange)

	prompt, err := BuildToolCompactionPromptV2(CompactionPromptRequest{
		Model: "summary-model", Protocol: types.RelayFormatOpenAI, Body: body, Plan: plan,
	})
	require.NoError(t, err)
	promptBody, err := common.Marshal(prompt)
	require.NoError(t, err)
	assert.Contains(t, string(promptBody), "initial request")
	assert.Contains(t, string(promptBody), "tool_result_summaries")
	for _, rawValue := range []string{"call-private-id", "private-account", "raw final assistant secret"} {
		assert.NotContains(t, string(promptBody), rawValue)
	}

	summary := validToolCompactionSummaryV2(t, plan)
	summaryBody, err := common.Marshal(summary)
	require.NoError(t, err)
	rewrittenBody, err := RewriteRequestWithConsensus(RewriteCompactedRequest{
		Protocol: types.RelayFormatOpenAI, Body: body, Plan: plan, SummaryBody: summaryBody,
	})
	require.NoError(t, err)
	for _, removedValue := range []string{"call-private-id", "private-account", "raw final assistant secret"} {
		assert.NotContains(t, string(rewrittenBody), removedValue)
	}
	assert.Contains(t, string(rewrittenBody), "lookup_private_account")
	assert.Contains(t, string(rewrittenBody), "recent question")
	var rewrittenRequest dto.GeneralOpenAIRequest
	require.NoError(t, common.Unmarshal(rewrittenBody, &rewrittenRequest))
	require.NotEmpty(t, rewrittenRequest.Messages)
	assert.Contains(t, rewrittenRequest.Messages[0].StringContent(), consensusSummaryPreamble)
}

func TestToolCompactionWithoutRegisteredPolicyStopsBeforeToolTurn(t *testing.T) {
	_, plan := toolCompactionV2Fixture(t, 1, false)

	assert.Equal(t, ConsensusSummaryVersion, plan.SummaryVersion)
	assert.Nil(t, plan.ToolAtomicRange)
	require.Len(t, plan.CoveredRanges, 1)
	assert.Equal(t, 0, plan.CoveredRanges[0].StartSequence)
	assert.Equal(t, 1, plan.CoveredRanges[0].EndSequence)
}

func TestConsensusSummaryV2RejectsToolFactTampering(t *testing.T) {
	_, plan := toolCompactionV2Fixture(t, 1, true)
	validSummary := validToolCompactionSummaryV2(t, plan)
	encoded, err := common.Marshal(validSummary)
	require.NoError(t, err)
	_, err = ParseAndValidateConsensusSummaryV2(encoded, plan)
	require.NoError(t, err)

	summary := validToolCompactionSummaryV2(t, plan)
	summary.ToolResultSummaries[0].Value = "tampered"
	encoded, err = common.Marshal(summary)
	require.NoError(t, err)
	_, err = ParseAndValidateConsensusSummaryV2(encoded, plan)
	require.ErrorContains(t, err, "sanitized projection")

	summary = validToolCompactionSummaryV2(t, plan)
	summary.ToolResultSummaries[0], summary.ToolResultSummaries[2] = summary.ToolResultSummaries[2], summary.ToolResultSummaries[0]
	encoded, err = common.Marshal(summary)
	require.NoError(t, err)
	_, err = ParseAndValidateConsensusSummaryV2(encoded, plan)
	require.ErrorContains(t, err, "sanitized projection")

	summary = validToolCompactionSummaryV2(t, plan)
	summary.CurrentPhase = "invented tool conclusion"
	encoded, err = common.Marshal(summary)
	require.NoError(t, err)
	_, err = ParseAndValidateConsensusSummaryV2(encoded, plan)
	require.ErrorContains(t, err, "current phase must be empty")

	hiddenRange, err := NewSummarySourceRange(plan, plan.ToolFinalSequence, plan.ToolFinalSequence)
	require.NoError(t, err)
	confidence := 1.0
	summary = validToolCompactionSummaryV2(t, plan)
	summary.Decisions = []ConsensusFact{{
		Field: "decision", Value: "hidden", Provenance: ConsensusProvenanceAssistantInferred,
		SourceRange: hiddenRange, SourceDigest: hiddenRange.SourceDigest, Confidence: &confidence,
	}}
	encoded, err = common.Marshal(summary)
	require.NoError(t, err)
	_, err = ParseAndValidateConsensusSummaryV2(encoded, plan)
	require.ErrorContains(t, err, "hidden tool segments")
}

func TestToolCompactionRejectsAssistantToolCallWithHiddenContent(t *testing.T) {
	body, _ := toolCompactionV2Fixture(t, 1, true)
	body = []byte(strings.Replace(string(body),
		`{"role":"assistant","tool_calls"`,
		`{"role":"assistant","content":"hidden call text","tool_calls"`, 1))
	envelope, err := Extract(ExtractionRequest{Protocol: types.RelayFormatOpenAI, Body: body})
	require.NoError(t, err)
	assessment := AssessSingleSerialToolCompaction(envelope)
	require.True(t, assessment.ReadyForSanitization)
	var request dto.GeneralOpenAIRequest
	require.NoError(t, common.Unmarshal(body, &request))

	_, _, _, err = extractChatCompactionTurns(request.Messages, *assessment.Evidence)
	require.ErrorContains(t, err, "required user/call/result/final shape")
}

func toolCompactionV2Fixture(t *testing.T, preservedRecentTurns int, registered bool) ([]byte, CompactionPlan) {
	t.Helper()
	body := []byte(`{
  "model":"gpt-5",
  "messages":[
    {"role":"user","content":"initial request"},
    {"role":"assistant","content":"initial answer"},
    {"role":"user","content":"lookup account"},
    {"role":"assistant","tool_calls":[{"id":"call-private-id","type":"function","function":{"name":"lookup_private_account","arguments":"{\"account\":\"private-account\"}"}}]},
    {"role":"tool","tool_call_id":"call-private-id","content":"{\"status\":\"ok\",\"count\":2,\"active\":true}"},
    {"role":"assistant","content":"raw final assistant secret"},
    {"role":"user","content":"recent question"},
    {"role":"assistant","content":"recent answer"},
    {"role":"user","content":"continue"}
  ],
  "tools":[{"type":"function","function":{"name":"lookup_private_account","parameters":{"type":"object"}}}]
}`)
	envelope, err := Extract(ExtractionRequest{Protocol: types.RelayFormatOpenAI, Body: body})
	require.NoError(t, err)
	assessment := AssessSingleSerialToolCompaction(envelope)
	require.True(t, assessment.ReadyForSanitization)
	require.NotNil(t, assessment.Evidence)

	provider, err := NewStaticToolSanitizationPolicyProvider(nil)
	require.NoError(t, err)
	if registered {
		provider, err = NewStaticToolSanitizationPolicyProvider([]ToolResultSanitizationPolicy{
			validToolSanitizationPolicyForTest(*assessment.Evidence),
		})
		require.NoError(t, err)
	}
	policy := enabledCompactionPolicy(preservedRecentTurns)
	policy.AllowToolResultCompaction = true
	plan, err := BuildToolCompactionPlanV2(ToolCompactionPlanRequest{
		CompactionPlanRequest: CompactionPlanRequest{
			Protocol: types.RelayFormatOpenAI, Body: body, Envelope: envelope, Policy: policy,
		},
		PolicyProvider: provider,
	})
	require.NoError(t, err)
	return body, plan
}

func validToolCompactionSummaryV2(t *testing.T, plan CompactionPlan) ConsensusSummary {
	t.Helper()
	toolFacts, err := toolCompactionExpectedFacts(plan)
	require.NoError(t, err)
	return ConsensusSummary{
		Version:             ConsensusSummaryVersionV2,
		TaskGoal:            []ConsensusFact{},
		CurrentPhase:        "",
		Decisions:           []ConsensusFact{},
		MustPreserve:        []ConsensusFact{},
		OpenQuestions:       []ConsensusFact{},
		UserPreferences:     []ConsensusFact{},
		DomainTerms:         map[string]ConsensusFact{},
		CompletedSteps:      []ConsensusFact{},
		PendingSteps:        []ConsensusFact{},
		ArtifactRefs:        []ConsensusFact{},
		ToolResultSummaries: toolFacts,
		SourceRanges:        append([]SummarySourceRange(nil), plan.CoveredRanges...),
		SourceDigest:        plan.SourceDigest,
	}
}
