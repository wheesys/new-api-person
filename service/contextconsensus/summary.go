package contextconsensus

import (
	"encoding/json"
	"fmt"
	"reflect"
	"sort"
	"strings"

	"github.com/QuantumNous/new-api/common"
)

const (
	ConsensusSummaryVersion   = 1
	ConsensusSummaryVersionV2 = 2
)

type ConsensusProvenance string

const (
	ConsensusProvenancePolicy            ConsensusProvenance = "policy"
	ConsensusProvenanceUserConfirmed     ConsensusProvenance = "user_confirmed"
	ConsensusProvenanceAssistantInferred ConsensusProvenance = "assistant_inferred"
	ConsensusProvenanceToolObserved      ConsensusProvenance = "tool_observed"
)

type ConsensusFact struct {
	Field        string              `json:"field"`
	Value        string              `json:"value"`
	Provenance   ConsensusProvenance `json:"provenance"`
	SourceRange  SummarySourceRange  `json:"source_range"`
	SourceDigest string              `json:"source_digest"`
	Confidence   *float64            `json:"confidence"`
}

type ConsensusSummary struct {
	Version             int                      `json:"version"`
	TaskGoal            []ConsensusFact          `json:"task_goal"`
	CurrentPhase        string                   `json:"current_phase"`
	Decisions           []ConsensusFact          `json:"decisions"`
	MustPreserve        []ConsensusFact          `json:"must_preserve"`
	OpenQuestions       []ConsensusFact          `json:"open_questions"`
	UserPreferences     []ConsensusFact          `json:"user_preferences"`
	DomainTerms         map[string]ConsensusFact `json:"domain_terms"`
	CompletedSteps      []ConsensusFact          `json:"completed_steps"`
	PendingSteps        []ConsensusFact          `json:"pending_steps"`
	ArtifactRefs        []ConsensusFact          `json:"artifact_refs"`
	ToolResultSummaries []ConsensusFact          `json:"tool_result_summaries"`
	SourceRanges        []SummarySourceRange     `json:"source_ranges"`
	SourceDigest        string                   `json:"source_digest"`
}

var consensusSummaryFields = []string{
	"version",
	"task_goal",
	"current_phase",
	"decisions",
	"must_preserve",
	"open_questions",
	"user_preferences",
	"domain_terms",
	"completed_steps",
	"pending_steps",
	"artifact_refs",
	"tool_result_summaries",
	"source_ranges",
	"source_digest",
}

var consensusFactFields = []string{
	"field",
	"value",
	"provenance",
	"source_range",
	"source_digest",
	"confidence",
}

var summarySourceRangeFields = []string{
	"start_sequence",
	"end_sequence",
	"source_digest",
}

func ParseAndValidateConsensusSummaryV1(data []byte, plan CompactionPlan) (ConsensusSummary, error) {
	summary, err := parseConsensusSummaryV1Shape(data)
	if err != nil {
		return ConsensusSummary{}, err
	}
	if err := ValidateConsensusSummaryV1(summary, plan); err != nil {
		return ConsensusSummary{}, err
	}
	return summary, nil
}

func ParseAndValidateConsensusSummaryV2(data []byte, plan CompactionPlan) (ConsensusSummary, error) {
	if err := common.ValidateJsonUniqueObjectKeys(data); err != nil {
		return ConsensusSummary{}, fmt.Errorf("consensus summary v2 contains duplicate object keys")
	}
	summary, err := parseConsensusSummaryV1Shape(data)
	if err != nil {
		return ConsensusSummary{}, err
	}
	if err := ValidateConsensusSummaryV2(summary, plan); err != nil {
		return ConsensusSummary{}, err
	}
	return summary, nil
}

func parseConsensusSummaryV1Shape(data []byte) (ConsensusSummary, error) {
	var rawSummary map[string]json.RawMessage
	if err := common.Unmarshal(data, &rawSummary); err != nil {
		return ConsensusSummary{}, fmt.Errorf("decode consensus summary: %w", err)
	}
	if rawSummary == nil {
		return ConsensusSummary{}, fmt.Errorf("consensus summary must be a JSON object")
	}
	if err := rejectForbiddenReasoningFields(data); err != nil {
		return ConsensusSummary{}, err
	}
	if err := validateExactObjectFields(rawSummary, consensusSummaryFields, "summary"); err != nil {
		return ConsensusSummary{}, err
	}
	if err := validateJSONFieldType(rawSummary["version"], "number", "summary.version"); err != nil {
		return ConsensusSummary{}, err
	}
	if err := validateJSONFieldType(rawSummary["current_phase"], "string", "summary.current_phase"); err != nil {
		return ConsensusSummary{}, err
	}
	if err := validateJSONFieldType(rawSummary["source_digest"], "string", "summary.source_digest"); err != nil {
		return ConsensusSummary{}, err
	}

	factArrayFields := []string{
		"task_goal",
		"decisions",
		"must_preserve",
		"open_questions",
		"user_preferences",
		"completed_steps",
		"pending_steps",
		"artifact_refs",
		"tool_result_summaries",
	}
	for _, fieldName := range factArrayFields {
		var facts []json.RawMessage
		if err := common.Unmarshal(rawSummary[fieldName], &facts); err != nil || facts == nil {
			return ConsensusSummary{}, fmt.Errorf("summary.%s must be an array", fieldName)
		}
		for index, rawFact := range facts {
			if err := validateRawConsensusFact(rawFact, fmt.Sprintf("summary.%s[%d]", fieldName, index)); err != nil {
				return ConsensusSummary{}, err
			}
		}
	}

	var domainTerms map[string]json.RawMessage
	if err := common.Unmarshal(rawSummary["domain_terms"], &domainTerms); err != nil || domainTerms == nil {
		return ConsensusSummary{}, fmt.Errorf("summary.domain_terms must be an object")
	}
	for term, rawFact := range domainTerms {
		if strings.TrimSpace(term) == "" {
			return ConsensusSummary{}, fmt.Errorf("summary.domain_terms contains an empty term")
		}
		if err := validateRawConsensusFact(rawFact, fmt.Sprintf("summary.domain_terms[%q]", term)); err != nil {
			return ConsensusSummary{}, err
		}
	}

	var sourceRanges []json.RawMessage
	if err := common.Unmarshal(rawSummary["source_ranges"], &sourceRanges); err != nil || sourceRanges == nil {
		return ConsensusSummary{}, fmt.Errorf("summary.source_ranges must be an array")
	}
	for index, rawSourceRange := range sourceRanges {
		if err := validateRawSourceRange(rawSourceRange, fmt.Sprintf("summary.source_ranges[%d]", index)); err != nil {
			return ConsensusSummary{}, err
		}

	}

	var summary ConsensusSummary
	if err := common.Unmarshal(data, &summary); err != nil {
		return ConsensusSummary{}, fmt.Errorf("decode typed consensus summary: %w", err)
	}
	return summary, nil
}

func ValidateConsensusSummaryV1(summary ConsensusSummary, plan CompactionPlan) error {
	if err := validateConsensusSummaryEnvelope(summary, plan, ConsensusSummaryVersion); err != nil {
		return err
	}
	if len(summary.ToolResultSummaries) > 0 {
		return fmt.Errorf("tool result summaries are not supported by compaction version 1")
	}

	factGroups := [][]ConsensusFact{
		summary.TaskGoal,
		summary.Decisions,
		summary.MustPreserve,
		summary.OpenQuestions,
		summary.UserPreferences,
		summary.CompletedSteps,
		summary.PendingSteps,
		summary.ArtifactRefs,
	}
	for _, facts := range factGroups {
		for _, fact := range facts {
			if err := validateConsensusFact(fact, plan); err != nil {
				return err
			}
		}
	}
	domainTerms := make([]string, 0, len(summary.DomainTerms))
	for term := range summary.DomainTerms {
		domainTerms = append(domainTerms, term)
	}
	sort.Strings(domainTerms)
	for _, term := range domainTerms {
		if err := validateConsensusFact(summary.DomainTerms[term], plan); err != nil {
			return fmt.Errorf("domain term %q: %w", term, err)
		}
	}
	return nil
}

func ValidateConsensusSummaryV2(summary ConsensusSummary, plan CompactionPlan) error {
	if plan.SummaryVersion != ConsensusSummaryVersionV2 || !plan.ToolContextPresent {
		return fmt.Errorf("consensus summary v2 requires a tool compaction plan")
	}
	if err := validateConsensusSummaryEnvelope(summary, plan, ConsensusSummaryVersionV2); err != nil {
		return err
	}
	if summary.CurrentPhase != "" {
		return fmt.Errorf("consensus summary v2 current phase must be empty")
	}
	expectedToolFacts, err := toolCompactionExpectedFacts(plan)
	if err != nil {
		return err
	}
	if !reflect.DeepEqual(summary.ToolResultSummaries, expectedToolFacts) {
		return fmt.Errorf("tool result summaries do not match the sanitized projection")
	}

	factGroups := [][]ConsensusFact{
		summary.TaskGoal,
		summary.Decisions,
		summary.MustPreserve,
		summary.OpenQuestions,
		summary.UserPreferences,
		summary.CompletedSteps,
		summary.PendingSteps,
		summary.ArtifactRefs,
	}
	for _, facts := range factGroups {
		for _, fact := range facts {
			if err := validateConsensusFactV2(fact, plan); err != nil {
				return err
			}
		}
	}
	domainTerms := make([]string, 0, len(summary.DomainTerms))
	for term := range summary.DomainTerms {
		domainTerms = append(domainTerms, term)
	}
	sort.Strings(domainTerms)
	for _, term := range domainTerms {
		if err := validateConsensusFactV2(summary.DomainTerms[term], plan); err != nil {
			return fmt.Errorf("domain term %q: %w", term, err)
		}
	}
	return nil
}

func validateConsensusSummaryEnvelope(summary ConsensusSummary, plan CompactionPlan, version int) error {
	if summary.Version != version {
		return fmt.Errorf("consensus summary version must be %d", version)
	}
	if summary.SourceDigest == "" || summary.SourceDigest != plan.SourceDigest {
		return fmt.Errorf("consensus summary source digest does not match compaction plan")
	}
	if !reflect.DeepEqual(summary.SourceRanges, plan.CoveredRanges) {
		return fmt.Errorf("consensus summary source ranges do not match compaction plan")
	}
	if summary.TaskGoal == nil || summary.Decisions == nil || summary.MustPreserve == nil || summary.OpenQuestions == nil || summary.UserPreferences == nil || summary.DomainTerms == nil || summary.CompletedSteps == nil || summary.PendingSteps == nil || summary.ArtifactRefs == nil || summary.ToolResultSummaries == nil || summary.SourceRanges == nil {
		return fmt.Errorf("consensus summary collection fields must not be null")
	}
	return nil
}

func validateConsensusFact(fact ConsensusFact, plan CompactionPlan) error {
	segments, err := validateConsensusFactBase(fact, plan)
	if err != nil {
		return err
	}
	switch fact.Provenance {
	case ConsensusProvenanceUserConfirmed:
		if !allSegmentsMatch(segments, func(segment ContextSegment) bool { return normalizedHistoryRole(segment.Role) == "user" }) {
			return fmt.Errorf("user_confirmed fact must cite only user segments")
		}
	case ConsensusProvenanceAssistantInferred:
		if !allSegmentsMatch(segments, func(segment ContextSegment) bool { return normalizedHistoryRole(segment.Role) == "assistant" }) {
			return fmt.Errorf("assistant_inferred fact must cite only assistant segments")
		}
	case ConsensusProvenancePolicy:
		return fmt.Errorf("policy provenance cannot cite compacted user-level history")
	case ConsensusProvenanceToolObserved:
		return fmt.Errorf("tool_observed provenance is not supported by compaction version 1")
	default:
		return fmt.Errorf("unsupported consensus fact provenance %q", fact.Provenance)
	}
	return nil
}

func validateConsensusFactBase(fact ConsensusFact, plan CompactionPlan) ([]ContextSegment, error) {
	if strings.TrimSpace(fact.Field) == "" {
		return nil, fmt.Errorf("consensus fact field is required")
	}
	if strings.TrimSpace(fact.Value) == "" {
		return nil, fmt.Errorf("consensus fact value is required")
	}
	if fact.Confidence == nil || *fact.Confidence < 0 || *fact.Confidence > 1 {
		return nil, fmt.Errorf("consensus fact confidence must be between zero and one")
	}
	expectedRange, err := NewSummarySourceRange(plan, fact.SourceRange.StartSequence, fact.SourceRange.EndSequence)
	if err != nil {
		return nil, fmt.Errorf("consensus fact source range: %w", err)
	}
	if fact.SourceRange != expectedRange || fact.SourceDigest != expectedRange.SourceDigest {
		return nil, fmt.Errorf("consensus fact source digest does not match its source range")
	}
	return coveredSegmentsInRange(plan, fact.SourceRange), nil
}

func validateConsensusFactV2(fact ConsensusFact, plan CompactionPlan) error {
	segments, err := validateConsensusFactBase(fact, plan)
	if err != nil {
		return err
	}
	for _, segment := range segments {
		if segment.Sequence == plan.ToolCallSequence || segment.Sequence == plan.ToolResultSequence || segment.Sequence == plan.ToolFinalSequence {
			return fmt.Errorf("ordinary summary fact cannot cite hidden tool segments")
		}
	}
	switch fact.Provenance {
	case ConsensusProvenanceUserConfirmed:
		if !allSegmentsMatch(segments, func(segment ContextSegment) bool { return normalizedHistoryRole(segment.Role) == "user" }) {
			return fmt.Errorf("user_confirmed fact must cite only user segments")
		}
	case ConsensusProvenanceAssistantInferred:
		if !allSegmentsMatch(segments, func(segment ContextSegment) bool { return normalizedHistoryRole(segment.Role) == "assistant" }) {
			return fmt.Errorf("assistant_inferred fact must cite only assistant segments")
		}
	case ConsensusProvenancePolicy:
		return fmt.Errorf("policy provenance cannot cite compacted user-level history")
	case ConsensusProvenanceToolObserved:
		return fmt.Errorf("tool_observed provenance is only allowed in tool result summaries")
	default:
		return fmt.Errorf("unsupported consensus fact provenance %q", fact.Provenance)
	}
	return nil
}

func toolCompactionExpectedFacts(plan CompactionPlan) ([]ConsensusFact, error) {
	if plan.ToolAtomicRange == nil || plan.toolProjection == nil || plan.ToolProjectionDigest == "" {
		return nil, fmt.Errorf("tool compaction plan has no sanitized projection")
	}
	if err := plan.toolProjection.Validate(); err != nil || plan.toolProjection.ProjectionDigest() != plan.ToolProjectionDigest {
		return nil, fmt.Errorf("tool compaction projection integrity check failed")
	}
	fields := plan.toolProjection.Fields()
	fieldNames := make([]string, 0, len(fields))
	for fieldName := range fields {
		fieldNames = append(fieldNames, fieldName)
	}
	sort.Strings(fieldNames)
	facts := make([]ConsensusFact, 0, len(fieldNames))
	for _, fieldName := range fieldNames {
		confidence := 1.0
		facts = append(facts, ConsensusFact{
			Field:        fieldName,
			Value:        common.JsonRawMessageToString(fields[fieldName]),
			Provenance:   ConsensusProvenanceToolObserved,
			SourceRange:  *plan.ToolAtomicRange,
			SourceDigest: plan.ToolAtomicRange.SourceDigest,
			Confidence:   &confidence,
		})
	}
	return facts, nil
}

func coveredSegmentsInRange(plan CompactionPlan, sourceRange SummarySourceRange) []ContextSegment {
	segments := make([]ContextSegment, 0, sourceRange.EndSequence-sourceRange.StartSequence+1)
	for _, segment := range plan.CoveredSegments {
		if segment.Sequence >= sourceRange.StartSequence && segment.Sequence <= sourceRange.EndSequence {
			segments = append(segments, segment)
		}
	}
	return segments
}

func allSegmentsMatch(segments []ContextSegment, predicate func(ContextSegment) bool) bool {
	if len(segments) == 0 {
		return false
	}
	for _, segment := range segments {
		if !predicate(segment) {
			return false
		}
	}
	return true
}

func validateRawConsensusFact(data json.RawMessage, path string) error {
	var rawFact map[string]json.RawMessage
	if err := common.Unmarshal(data, &rawFact); err != nil || rawFact == nil {
		return fmt.Errorf("%s must be an object", path)
	}
	if err := validateExactObjectFields(rawFact, consensusFactFields, path); err != nil {
		return err
	}
	stringFields := []string{"field", "value", "provenance", "source_digest"}
	for _, fieldName := range stringFields {
		if err := validateJSONFieldType(rawFact[fieldName], "string", path+"."+fieldName); err != nil {
			return err
		}
	}
	if err := validateJSONFieldType(rawFact["confidence"], "number", path+".confidence"); err != nil {
		return err
	}
	return validateRawSourceRange(rawFact["source_range"], path+".source_range")
}

func validateRawSourceRange(data json.RawMessage, path string) error {
	var rawSourceRange map[string]json.RawMessage
	if err := common.Unmarshal(data, &rawSourceRange); err != nil || rawSourceRange == nil {
		return fmt.Errorf("%s must be an object", path)
	}
	if err := validateExactObjectFields(rawSourceRange, summarySourceRangeFields, path); err != nil {
		return err
	}
	if err := validateJSONFieldType(rawSourceRange["start_sequence"], "number", path+".start_sequence"); err != nil {
		return err
	}
	if err := validateJSONFieldType(rawSourceRange["end_sequence"], "number", path+".end_sequence"); err != nil {
		return err
	}
	return validateJSONFieldType(rawSourceRange["source_digest"], "string", path+".source_digest")
}

func validateJSONFieldType(value json.RawMessage, expectedType, path string) error {
	actualType := common.GetJsonType(value)
	if actualType != expectedType {
		return fmt.Errorf("%s must be a JSON %s, got %s", path, expectedType, actualType)
	}
	return nil
}

func validateExactObjectFields(object map[string]json.RawMessage, expectedFields []string, path string) error {
	expected := make(map[string]struct{}, len(expectedFields))
	for _, field := range expectedFields {
		expected[field] = struct{}{}
		if _, present := object[field]; !present {
			return fmt.Errorf("%s is missing required field %q", path, field)
		}
	}
	for field := range object {
		if _, allowed := expected[field]; !allowed {
			return fmt.Errorf("%s contains unsupported field %q", path, field)
		}
	}
	return nil
}

func rejectForbiddenReasoningFields(data []byte) error {
	var value any
	if err := common.Unmarshal(data, &value); err != nil {
		return fmt.Errorf("decode consensus summary for safety validation: %w", err)
	}
	forbidden := map[string]struct{}{
		"analysis":         {},
		"chain_of_thought": {},
		"hidden_reasoning": {},
		"reasoning":        {},
		"thoughts":         {},
	}
	return walkSummaryFields(value, "summary", forbidden)
}

func walkSummaryFields(value any, path string, forbidden map[string]struct{}) error {
	switch typed := value.(type) {
	case map[string]any:
		for field, child := range typed {
			normalizedField := strings.ToLower(strings.TrimSpace(field))
			if _, blocked := forbidden[normalizedField]; blocked {
				return fmt.Errorf("%s contains forbidden hidden reasoning field %q", path, field)
			}
			if err := walkSummaryFields(child, path+"."+field, forbidden); err != nil {
				return err
			}
		}
	case []any:
		for index, child := range typed {
			if err := walkSummaryFields(child, fmt.Sprintf("%s[%d]", path, index), forbidden); err != nil {
				return err
			}
		}
	}
	return nil
}
