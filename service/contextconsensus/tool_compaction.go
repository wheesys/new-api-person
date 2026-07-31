package contextconsensus

import (
	"encoding/hex"
	"errors"
	"fmt"
	"strings"

	"github.com/QuantumNous/new-api/relaykit/types"
)

var ErrToolCompactionEvidenceInvalid = errors.New("tool compaction structural evidence is invalid")

const (
	ToolCompactionDiagnosticSchemaVersion = 1

	ToolCompactionDiagnosticNotApplicable        = "not_applicable"
	ToolCompactionDiagnosticReadyForSanitization = "ready_for_sanitization"
	ToolCompactionDiagnosticBlocked              = "blocked"

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
	ToolCompactionReasonDigestInvalid       = "tool_compaction_digest_invalid"
	ToolCompactionReasonSequenceInvalid     = "tool_compaction_sequence_invalid"
)

const toolCompactionReasonCodeCount = 13

// ToolCompactionDiagnostic is the only tool-compaction assessment that may be
// persisted. It intentionally excludes structural evidence and digests.
type ToolCompactionDiagnostic struct {
	SchemaVersion int      `json:"schema_version"`
	Status        string   `json:"status"`
	ReasonCodes   []string `json:"reason_codes"`
}

func NewToolCompactionDiagnostic(envelope *ContextEnvelope, applicable bool) ToolCompactionDiagnostic {
	diagnostic := ToolCompactionDiagnostic{
		SchemaVersion: ToolCompactionDiagnosticSchemaVersion,
		Status:        ToolCompactionDiagnosticNotApplicable,
		ReasonCodes:   []string{},
	}
	if !applicable {
		return diagnostic
	}

	assessment := AssessSingleSerialToolCompaction(envelope)
	if assessment.ReadyForSanitization {
		diagnostic.Status = ToolCompactionDiagnosticReadyForSanitization
		return diagnostic
	}
	diagnostic.Status = ToolCompactionDiagnosticBlocked
	diagnostic.ReasonCodes = append([]string{}, assessment.ReasonCodes...)
	return diagnostic
}

func (diagnostic ToolCompactionDiagnostic) Validate() error {
	if diagnostic.SchemaVersion != ToolCompactionDiagnosticSchemaVersion {
		return fmt.Errorf("unsupported tool compaction diagnostic schema")
	}
	if len(diagnostic.ReasonCodes) > toolCompactionReasonCodeCount {
		return fmt.Errorf("too many tool compaction diagnostic reason codes")
	}
	seen := make(map[string]struct{}, len(diagnostic.ReasonCodes))
	for _, reasonCode := range diagnostic.ReasonCodes {
		if !validToolCompactionReasonCode(reasonCode) {
			return fmt.Errorf("unknown tool compaction diagnostic reason code")
		}
		if _, duplicate := seen[reasonCode]; duplicate {
			return fmt.Errorf("duplicate tool compaction diagnostic reason code")
		}
		seen[reasonCode] = struct{}{}
	}

	switch diagnostic.Status {
	case ToolCompactionDiagnosticNotApplicable, ToolCompactionDiagnosticReadyForSanitization:
		if len(diagnostic.ReasonCodes) != 0 {
			return fmt.Errorf("tool compaction diagnostic status cannot include reason codes")
		}
	case ToolCompactionDiagnosticBlocked:
		if len(diagnostic.ReasonCodes) == 0 {
			return fmt.Errorf("blocked tool compaction diagnostic requires a reason code")
		}
	default:
		return fmt.Errorf("unknown tool compaction diagnostic status")
	}
	return nil
}

func validToolCompactionReasonCode(reasonCode string) bool {
	switch reasonCode {
	case ToolCompactionReasonEnvelopeUnavailable,
		ToolCompactionReasonProtocolUnsupported,
		ToolCompactionReasonProviderBound,
		ToolCompactionReasonMediaPresent,
		ToolCompactionReasonSchemaMissing,
		ToolCompactionReasonGraphAmbiguous,
		ToolCompactionReasonExchangeCount,
		ToolCompactionReasonExchangeIncomplete,
		ToolCompactionReasonOpaqueState,
		ToolCompactionReasonIdentityMissing,
		ToolCompactionReasonDigestMissing,
		ToolCompactionReasonDigestInvalid,
		ToolCompactionReasonSequenceInvalid:
		return true
	default:
		return false
	}
}

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
	integrityDigest    string
}

func (evidence ToolCompactionStructuralEvidence) Validate() error {
	if evidence.Protocol != types.RelayFormatOpenAI || evidence.CallSequence < 0 || evidence.ResultSequence <= evidence.CallSequence ||
		evidence.Status != ToolExchangeCompleted || !validToolCompactionDigest(evidence.CallIdentityDigest) ||
		!validToolCompactionDigest(evidence.ToolIdentityDigest) || !validToolCompactionDigest(evidence.ArgumentsDigest) ||
		!validToolCompactionDigest(evidence.ResultDigest) || !validToolCompactionDigest(evidence.SchemaDigest) ||
		evidence.integrityDigest == "" || evidence.integrityDigest != digestValue(evidence) {
		return ErrToolCompactionEvidenceInvalid
	}
	return nil
}

func validToolCompactionDigest(value string) bool {
	if len(value) != 64 || strings.ToLower(value) != value {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil
}

type ToolCompactionStructuralAssessment struct {
	ReadyForSanitization bool                              `json:"ready_for_sanitization"`
	ReasonCodes          []string                          `json:"reason_codes,omitempty"`
	Evidence             *ToolCompactionStructuralEvidence `json:"evidence,omitempty"`
}

// AssessSingleSerialToolCompaction checks the structural prerequisites for the
// separate sanitizer and tool-aware compaction authorization stages.
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
	if (exchange.ArgumentsDigest != "" && !validToolCompactionDigest(exchange.ArgumentsDigest)) ||
		(exchange.ResultDigest != "" && !validToolCompactionDigest(exchange.ResultDigest)) ||
		(envelope.ToolState.SchemaDigest != "" && !validToolCompactionDigest(envelope.ToolState.SchemaDigest)) {
		reasonCodes = append(reasonCodes, ToolCompactionReasonDigestInvalid)
	}
	if exchange.ResultSequence == nil || *exchange.ResultSequence <= exchange.Sequence {
		reasonCodes = append(reasonCodes, ToolCompactionReasonSequenceInvalid)
	}
	if len(reasonCodes) > 0 {
		return ToolCompactionStructuralAssessment{ReasonCodes: reasonCodes}
	}

	evidence := ToolCompactionStructuralEvidence{
		Protocol:           envelope.Protocol,
		CallSequence:       exchange.Sequence,
		ResultSequence:     *exchange.ResultSequence,
		Status:             exchange.Status,
		CallIdentityDigest: digestString(exchange.CallID),
		ToolIdentityDigest: digestString(exchange.FunctionName),
		ArgumentsDigest:    exchange.ArgumentsDigest,
		ResultDigest:       exchange.ResultDigest,
		SchemaDigest:       envelope.ToolState.SchemaDigest,
	}
	evidence.integrityDigest = digestValue(evidence)
	return ToolCompactionStructuralAssessment{
		ReadyForSanitization: true,
		Evidence:             &evidence,
	}
}
