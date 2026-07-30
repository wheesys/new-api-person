package contextconsensus

import (
	"strings"

	"github.com/QuantumNous/new-api/types"
)

const (
	ToolCompactionReasonEnvelopeUnavailable = "tool_compaction_envelope_unavailable"
	ToolCompactionReasonProtocolUnsupported = "tool_compaction_protocol_unsupported"
	ToolCompactionReasonProviderBound       = "tool_compaction_provider_bound"
	ToolCompactionReasonMediaPresent        = "tool_compaction_media_present"
	ToolCompactionReasonSchemaMissing       = "tool_compaction_schema_missing"
	ToolCompactionReasonGraphAmbiguous      = "tool_compaction_graph_ambiguous"
	ToolCompactionReasonExchangeCount       = "tool_compaction_exchange_count_unsupported"
	ToolCompactionReasonExchangeIncomplete  = "tool_compaction_exchange_incomplete"
	ToolCompactionReasonOpaqueState         = "tool_compaction_opaque_state"
	ToolCompactionReasonIdentityMissing     = "tool_compaction_identity_missing"
	ToolCompactionReasonDigestMissing       = "tool_compaction_digest_missing"
	ToolCompactionReasonSequenceInvalid     = "tool_compaction_sequence_invalid"
)

// ToolCompactionStructuralEvidence contains only digests and causal metadata.
// It is not authorization to send tool content to a compaction model.
type ToolCompactionStructuralEvidence struct {
	Protocol           types.RelayFormat  `json:"protocol"`
	CallSequence       int                `json:"call_sequence"`
	ResultSequence     int                `json:"result_sequence"`
	Status             ToolExchangeStatus `json:"status"`
	CallIdentityDigest string             `json:"call_identity_digest"`
	ToolIdentityDigest string             `json:"tool_identity_digest"`
	ArgumentsDigest    string             `json:"arguments_digest"`
	ResultDigest       string             `json:"result_digest"`
	SchemaDigest       string             `json:"schema_digest"`
}

type ToolCompactionStructuralAssessment struct {
	ReadyForSanitization bool                              `json:"ready_for_sanitization"`
	ReasonCodes          []string                          `json:"reason_codes,omitempty"`
	Evidence             *ToolCompactionStructuralEvidence `json:"evidence,omitempty"`
}

// AssessSingleSerialToolCompaction checks only the structural prerequisites
// for a future sanitizer. Runtime compaction remains disabled for every tool graph.
func AssessSingleSerialToolCompaction(envelope *ContextEnvelope) ToolCompactionStructuralAssessment {
	if envelope == nil {
		return ToolCompactionStructuralAssessment{ReasonCodes: []string{ToolCompactionReasonEnvelopeUnavailable}}
	}

	reasonCodes := make([]string, 0, 8)
	if envelope.Protocol != types.RelayFormatOpenAI {
		reasonCodes = append(reasonCodes, ToolCompactionReasonProtocolUnsupported)
	}
	if envelope.ProviderBinding.Required() {
		reasonCodes = append(reasonCodes, ToolCompactionReasonProviderBound)
	}
	if envelope.MediaState.TotalCount() > 0 || envelope.MediaState.InlineCount > 0 {
		reasonCodes = append(reasonCodes, ToolCompactionReasonMediaPresent)
	}
	if strings.TrimSpace(envelope.ToolState.SchemaDigest) == "" {
		reasonCodes = append(reasonCodes, ToolCompactionReasonSchemaMissing)
	}
	if len(envelope.ToolState.AmbiguousFunctionNames) > 0 {
		reasonCodes = append(reasonCodes, ToolCompactionReasonGraphAmbiguous)
	}
	if len(envelope.ToolState.Exchanges) != 1 {
		reasonCodes = append(reasonCodes, ToolCompactionReasonExchangeCount)
		return ToolCompactionStructuralAssessment{ReasonCodes: reasonCodes}
	}

	exchange := envelope.ToolState.Exchanges[0]
	if envelope.Protocol == types.RelayFormatOpenAI && exchange.Protocol != envelope.Protocol {
		reasonCodes = append(reasonCodes, ToolCompactionReasonProtocolUnsupported)
	}
	if exchange.Status != ToolExchangeCompleted || !exchange.RawCallPresent || !exchange.RawResultPresent {
		reasonCodes = append(reasonCodes, ToolCompactionReasonExchangeIncomplete)
	}
	if exchange.OpaqueStatePresent {
		reasonCodes = append(reasonCodes, ToolCompactionReasonOpaqueState)
	}
	if strings.TrimSpace(exchange.CallID) == "" || strings.TrimSpace(exchange.FunctionName) == "" {
		reasonCodes = append(reasonCodes, ToolCompactionReasonIdentityMissing)
	}
	if strings.TrimSpace(exchange.ArgumentsDigest) == "" || strings.TrimSpace(exchange.ResultDigest) == "" {
		reasonCodes = append(reasonCodes, ToolCompactionReasonDigestMissing)
	}
	if exchange.ResultSequence == nil || *exchange.ResultSequence <= exchange.Sequence {
		reasonCodes = append(reasonCodes, ToolCompactionReasonSequenceInvalid)
	}
	if len(reasonCodes) > 0 {
		return ToolCompactionStructuralAssessment{ReasonCodes: reasonCodes}
	}

	return ToolCompactionStructuralAssessment{
		ReadyForSanitization: true,
		Evidence: &ToolCompactionStructuralEvidence{
			Protocol:           envelope.Protocol,
			CallSequence:       exchange.Sequence,
			ResultSequence:     *exchange.ResultSequence,
			Status:             exchange.Status,
			CallIdentityDigest: digestString(exchange.CallID),
			ToolIdentityDigest: digestString(exchange.FunctionName),
			ArgumentsDigest:    exchange.ArgumentsDigest,
			ResultDigest:       exchange.ResultDigest,
			SchemaDigest:       envelope.ToolState.SchemaDigest,
		},
	}
}
