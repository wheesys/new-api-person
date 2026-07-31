package contextconsensus

import (
	"encoding/hex"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/QuantumNous/new-api/relaykit/types"
)

var ErrToolCompactionEvidenceInvalid = errors.New("tool compaction structural evidence is invalid")

const (
	ToolCompactionDiagnosticSchemaVersion       = 2
	ToolCompactionDiagnosticLegacySchemaVersion = 1
	MaximumToolCompactionGroupExchanges         = 8

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

	assessment := AssessToolCompactionGroup(envelope)
	if assessment.ReadyForSanitization {
		diagnostic.Status = ToolCompactionDiagnosticReadyForSanitization
		return diagnostic
	}
	diagnostic.Status = ToolCompactionDiagnosticBlocked
	diagnostic.ReasonCodes = append([]string{}, assessment.ReasonCodes...)
	return diagnostic
}

func (diagnostic ToolCompactionDiagnostic) Validate() error {
	if diagnostic.SchemaVersion != ToolCompactionDiagnosticLegacySchemaVersion && diagnostic.SchemaVersion != ToolCompactionDiagnosticSchemaVersion {
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

// ToolCompactionGroupEvidence seals one complete call-derived tool group in
// call order. Raw identities and payloads remain private to the envelope.
type ToolCompactionGroupEvidence struct {
	Protocol        types.RelayFormat                  `json:"protocol"`
	GroupIndex      int                                `json:"group_index"`
	CallSequence    int                                `json:"call_sequence"`
	ResultSequences []int                              `json:"result_sequences"`
	Status          ToolExchangeStatus                 `json:"status"`
	Exchanges       []ToolCompactionStructuralEvidence `json:"exchanges"`
	integrityDigest string
}

func (evidence ToolCompactionGroupEvidence) Validate() error {
	if evidence.Protocol != types.RelayFormatOpenAI || evidence.GroupIndex < 0 || evidence.CallSequence < 0 ||
		evidence.Status != ToolExchangeCompleted || len(evidence.Exchanges) == 0 ||
		len(evidence.Exchanges) > MaximumToolCompactionGroupExchanges || len(evidence.ResultSequences) != len(evidence.Exchanges) ||
		evidence.integrityDigest == "" || evidence.integrityDigest != digestValue(evidence) {
		return ErrToolCompactionEvidenceInvalid
	}
	resultSequences := append([]int(nil), evidence.ResultSequences...)
	sort.Ints(resultSequences)
	for index, exchange := range evidence.Exchanges {
		if err := exchange.Validate(); err != nil || exchange.Protocol != evidence.Protocol ||
			exchange.CallSequence != evidence.CallSequence || exchange.ResultSequence != evidence.ResultSequences[index] {
			return ErrToolCompactionEvidenceInvalid
		}
		if resultSequences[index] != evidence.CallSequence+index+1 {
			return ErrToolCompactionEvidenceInvalid
		}
	}
	return nil
}

type ToolCompactionGroupAssessment struct {
	ReadyForSanitization bool                         `json:"ready_for_sanitization"`
	ReasonCodes          []string                     `json:"reason_codes,omitempty"`
	Evidence             *ToolCompactionGroupEvidence `json:"evidence,omitempty"`
}

// AssessToolCompactionGroup verifies that the request contains exactly one
// bounded, stable-ID OpenAI Chat tool group that can be sanitized atomically.
func AssessToolCompactionGroup(envelope *ContextEnvelope) ToolCompactionGroupAssessment {
	if envelope == nil {
		return ToolCompactionGroupAssessment{ReasonCodes: []string{ToolCompactionReasonEnvelopeUnavailable}}
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

	groups := envelope.ToolState.Groups
	if len(groups) == 0 && len(envelope.ToolState.Exchanges) == 1 {
		exchange := envelope.ToolState.Exchanges[0]
		groups = []ToolGraphGroup{{
			Protocol: exchange.Protocol, CallContainerSequence: exchange.Sequence,
			CallSequenceStart: exchange.Sequence, CallSequenceEnd: exchange.Sequence,
			ExchangeIndexes: []int{0}, Status: exchange.Status, IdentityMode: ToolIdentityModeStableID,
		}}
	}
	if len(groups) != 1 || len(groups[0].ExchangeIndexes) == 0 || len(groups[0].ExchangeIndexes) > MaximumToolCompactionGroupExchanges ||
		len(groups[0].ExchangeIndexes) != len(envelope.ToolState.Exchanges) {
		reasonCodes = append(reasonCodes, ToolCompactionReasonExchangeCount)
		return ToolCompactionGroupAssessment{ReasonCodes: reasonCodes}
	}

	group := groups[0]
	if group.Protocol != types.RelayFormatOpenAI || group.IdentityMode != ToolIdentityModeStableID {
		reasonCodes = appendToolCompactionReasonCode(reasonCodes, ToolCompactionReasonProtocolUnsupported)
	}
	if group.Status != ToolExchangeCompleted || group.CallSequenceStart != group.CallSequenceEnd {
		reasonCodes = appendToolCompactionReasonCode(reasonCodes, ToolCompactionReasonExchangeIncomplete)
	}

	exchangeEvidence := make([]ToolCompactionStructuralEvidence, 0, len(group.ExchangeIndexes))
	resultSequences := make([]int, 0, len(group.ExchangeIndexes))
	seenExchangeIndexes := make(map[int]struct{}, len(group.ExchangeIndexes))
	for _, exchangeIndex := range group.ExchangeIndexes {
		if exchangeIndex < 0 || exchangeIndex >= len(envelope.ToolState.Exchanges) {
			reasonCodes = appendToolCompactionReasonCode(reasonCodes, ToolCompactionReasonExchangeIncomplete)
			continue
		}
		if _, duplicate := seenExchangeIndexes[exchangeIndex]; duplicate {
			reasonCodes = appendToolCompactionReasonCode(reasonCodes, ToolCompactionReasonExchangeIncomplete)
			continue
		}
		seenExchangeIndexes[exchangeIndex] = struct{}{}
		exchange := envelope.ToolState.Exchanges[exchangeIndex]
		if exchange.GroupIndex != 0 || exchange.Protocol != envelope.Protocol {
			reasonCodes = appendToolCompactionReasonCode(reasonCodes, ToolCompactionReasonProtocolUnsupported)
		}
		if exchange.Status != ToolExchangeCompleted || !exchange.RawCallPresent || !exchange.RawResultPresent {
			reasonCodes = appendToolCompactionReasonCode(reasonCodes, ToolCompactionReasonExchangeIncomplete)
		}
		if exchange.OpaqueStatePresent {
			reasonCodes = appendToolCompactionReasonCode(reasonCodes, ToolCompactionReasonOpaqueState)
		}
		if strings.TrimSpace(exchange.CallID) == "" || strings.TrimSpace(exchange.FunctionName) == "" {
			reasonCodes = appendToolCompactionReasonCode(reasonCodes, ToolCompactionReasonIdentityMissing)
		}
		if strings.TrimSpace(exchange.ArgumentsDigest) == "" || strings.TrimSpace(exchange.ResultDigest) == "" {
			reasonCodes = appendToolCompactionReasonCode(reasonCodes, ToolCompactionReasonDigestMissing)
		}
		if (exchange.ArgumentsDigest != "" && !validToolCompactionDigest(exchange.ArgumentsDigest)) ||
			(exchange.ResultDigest != "" && !validToolCompactionDigest(exchange.ResultDigest)) ||
			(envelope.ToolState.SchemaDigest != "" && !validToolCompactionDigest(envelope.ToolState.SchemaDigest)) {
			reasonCodes = appendToolCompactionReasonCode(reasonCodes, ToolCompactionReasonDigestInvalid)
		}
		if exchange.Sequence != group.CallSequenceStart || exchange.ResultSequence == nil || *exchange.ResultSequence <= exchange.Sequence {
			reasonCodes = appendToolCompactionReasonCode(reasonCodes, ToolCompactionReasonSequenceInvalid)
			continue
		}
		evidence := ToolCompactionStructuralEvidence{
			Protocol: envelope.Protocol, CallSequence: exchange.Sequence, ResultSequence: *exchange.ResultSequence,
			Status: exchange.Status, CallIdentityDigest: digestString(exchange.CallID), ToolIdentityDigest: digestString(exchange.FunctionName),
			ArgumentsDigest: exchange.ArgumentsDigest, ResultDigest: exchange.ResultDigest, SchemaDigest: envelope.ToolState.SchemaDigest,
		}
		evidence.integrityDigest = digestValue(evidence)
		exchangeEvidence = append(exchangeEvidence, evidence)
		resultSequences = append(resultSequences, *exchange.ResultSequence)
	}
	if len(reasonCodes) > 0 {
		return ToolCompactionGroupAssessment{ReasonCodes: reasonCodes}
	}

	evidence := ToolCompactionGroupEvidence{
		Protocol: envelope.Protocol, GroupIndex: 0, CallSequence: group.CallSequenceStart,
		ResultSequences: resultSequences, Status: group.Status, Exchanges: exchangeEvidence,
	}
	evidence.integrityDigest = digestValue(evidence)
	if err := evidence.Validate(); err != nil {
		return ToolCompactionGroupAssessment{ReasonCodes: []string{ToolCompactionReasonSequenceInvalid}}
	}
	return ToolCompactionGroupAssessment{ReadyForSanitization: true, Evidence: &evidence}
}

// AssessSingleSerialToolCompaction checks the structural prerequisites for the
// separate sanitizer and tool-aware compaction authorization stages.
func AssessSingleSerialToolCompaction(envelope *ContextEnvelope) ToolCompactionStructuralAssessment {
	assessment := AssessToolCompactionGroup(envelope)
	if !assessment.ReadyForSanitization || assessment.Evidence == nil {
		return ToolCompactionStructuralAssessment{ReasonCodes: assessment.ReasonCodes}
	}
	if len(assessment.Evidence.Exchanges) != 1 {
		return ToolCompactionStructuralAssessment{ReasonCodes: []string{ToolCompactionReasonExchangeCount}}
	}
	evidence := assessment.Evidence.Exchanges[0]
	return ToolCompactionStructuralAssessment{ReadyForSanitization: true, Evidence: &evidence}
}

func appendToolCompactionReasonCode(values []string, value string) []string {
	for _, existing := range values {
		if existing == value {
			return values
		}
	}
	return append(values, value)
}
