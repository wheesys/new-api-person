package contextconsensus

import (
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAssessSingleSerialToolCompactionProducesSafeStructuralEvidence(t *testing.T) {
	body := []byte(`{
  "model":"gpt-5",
  "messages":[
    {"role":"user","content":"lookup account"},
    {"role":"assistant","tool_calls":[{"id":"call-sensitive-id","type":"function","function":{"name":"lookup_private_account","arguments":"{\"account\":\"secret-account\"}"}}]},
    {"role":"tool","tool_call_id":"call-sensitive-id","content":"secret-result-value"},
    {"role":"assistant","content":"lookup complete"},
    {"role":"user","content":"continue"}
  ],
  "tools":[{"type":"function","function":{"name":"lookup_private_account","parameters":{"type":"object"}}}]
}`)
	envelope, err := Extract(ExtractionRequest{Protocol: types.RelayFormatOpenAI, Body: body})
	require.NoError(t, err)

	assessment := AssessSingleSerialToolCompaction(envelope)
	require.True(t, assessment.ReadyForSanitization)
	require.Empty(t, assessment.ReasonCodes)
	require.NotNil(t, assessment.Evidence)
	require.NoError(t, assessment.Evidence.Validate())
	assert.Equal(t, 1, assessment.Evidence.CallSequence)
	assert.Equal(t, 2, assessment.Evidence.ResultSequence)
	assert.Equal(t, ToolExchangeCompleted, assessment.Evidence.Status)

	encoded, err := common.Marshal(assessment)
	require.NoError(t, err)
	for _, sensitiveValue := range []string{"call-sensitive-id", "lookup_private_account", "secret-account", "secret-result-value"} {
		assert.NotContains(t, string(encoded), sensitiveValue)
	}
	var serializedEvidence ToolCompactionStructuralEvidence
	require.NoError(t, common.Unmarshal(encodedEvidence(t, assessment.Evidence), &serializedEvidence))
	require.ErrorIs(t, serializedEvidence.Validate(), ErrToolCompactionEvidenceInvalid)
	tamperedEvidence := *assessment.Evidence
	tamperedEvidence.ResultDigest = digestString("different-result")
	require.ErrorIs(t, tamperedEvidence.Validate(), ErrToolCompactionEvidenceInvalid)

	_, err = BuildCompactionPlan(CompactionPlanRequest{
		Protocol: types.RelayFormatOpenAI,
		Body:     body,
		Envelope: envelope,
		Policy:   enabledCompactionPolicy(1),
	})
	require.ErrorContains(t, err, "tool context cannot be compacted")
}

func encodedEvidence(t *testing.T, evidence *ToolCompactionStructuralEvidence) []byte {
	t.Helper()
	encoded, err := common.Marshal(evidence)
	require.NoError(t, err)
	return encoded
}

func TestAssessSingleSerialToolCompactionFailsClosed(t *testing.T) {
	resultSequence := 2
	baseEnvelope := ContextEnvelope{
		Protocol:        types.RelayFormatOpenAI,
		ProviderBinding: ProtocolBinding{BindingLevel: BindingLevelNone, RelayFormat: types.RelayFormatOpenAI},
		ToolState: ToolGraph{
			SchemaDigest: digestString("schema"),
			Exchanges: []ToolExchange{{
				Protocol: types.RelayFormatOpenAI, Sequence: 1, ResultSequence: &resultSequence,
				CallID: "call-1", FunctionName: "lookup", ArgumentsDigest: digestString("arguments"), ResultDigest: digestString("result"),
				Status: ToolExchangeCompleted, RawCallPresent: true, RawResultPresent: true,
			}},
		},
	}

	tests := []struct {
		name       string
		mutate     func(*ContextEnvelope)
		reasonCode string
	}{
		{name: "unsupported protocol", mutate: func(envelope *ContextEnvelope) { envelope.Protocol = types.RelayFormatClaude }, reasonCode: ToolCompactionReasonProtocolUnsupported},
		{name: "provider bound", mutate: func(envelope *ContextEnvelope) { envelope.ProviderBinding.BindingLevel = BindingLevelCredential }, reasonCode: ToolCompactionReasonProviderBound},
		{name: "media present", mutate: func(envelope *ContextEnvelope) { envelope.MediaState.FileCount = 1 }, reasonCode: ToolCompactionReasonMediaPresent},
		{name: "schema missing", mutate: func(envelope *ContextEnvelope) { envelope.ToolState.SchemaDigest = "" }, reasonCode: ToolCompactionReasonSchemaMissing},
		{name: "ambiguous graph", mutate: func(envelope *ContextEnvelope) { envelope.ToolState.AmbiguousFunctionNames = []string{"lookup"} }, reasonCode: ToolCompactionReasonGraphAmbiguous},
		{name: "multiple exchanges", mutate: func(envelope *ContextEnvelope) {
			envelope.ToolState.Exchanges = append(envelope.ToolState.Exchanges, envelope.ToolState.Exchanges[0])
		}, reasonCode: ToolCompactionReasonExchangeCount},
		{name: "incomplete exchange", mutate: func(envelope *ContextEnvelope) { envelope.ToolState.Exchanges[0].RawResultPresent = false }, reasonCode: ToolCompactionReasonExchangeIncomplete},
		{name: "opaque state", mutate: func(envelope *ContextEnvelope) { envelope.ToolState.Exchanges[0].OpaqueStatePresent = true }, reasonCode: ToolCompactionReasonOpaqueState},
		{name: "identity missing", mutate: func(envelope *ContextEnvelope) { envelope.ToolState.Exchanges[0].CallID = "" }, reasonCode: ToolCompactionReasonIdentityMissing},
		{name: "digest missing", mutate: func(envelope *ContextEnvelope) { envelope.ToolState.Exchanges[0].ResultDigest = "" }, reasonCode: ToolCompactionReasonDigestMissing},
		{name: "digest malformed", mutate: func(envelope *ContextEnvelope) { envelope.ToolState.Exchanges[0].ResultDigest = "not-a-digest" }, reasonCode: ToolCompactionReasonDigestInvalid},
		{name: "sequence invalid", mutate: func(envelope *ContextEnvelope) { envelope.ToolState.Exchanges[0].ResultSequence = nil }, reasonCode: ToolCompactionReasonSequenceInvalid},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			envelope := baseEnvelope
			envelope.ToolState.Exchanges = append([]ToolExchange(nil), baseEnvelope.ToolState.Exchanges...)
			test.mutate(&envelope)

			assessment := AssessSingleSerialToolCompaction(&envelope)
			assert.False(t, assessment.ReadyForSanitization)
			assert.Contains(t, assessment.ReasonCodes, test.reasonCode)
			assert.Nil(t, assessment.Evidence)
		})
	}
}

func TestAssessSingleSerialToolCompactionRejectsMissingEnvelope(t *testing.T) {
	assessment := AssessSingleSerialToolCompaction(nil)

	assert.False(t, assessment.ReadyForSanitization)
	assert.Equal(t, []string{ToolCompactionReasonEnvelopeUnavailable}, assessment.ReasonCodes)
}
