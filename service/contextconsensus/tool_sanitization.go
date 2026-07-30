package contextconsensus

import (
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/QuantumNous/new-api/common"
)

const (
	ToolResultSanitizerVersion               = 1
	maximumToolSanitizationPolicies          = 256
	maximumToolSanitizationRules             = 32
	maximumToolSanitizationInputBytes        = 64 * 1024
	maximumToolSanitizationOutputBytes       = 16 * 1024
	maximumToolSanitizationDepth             = 8
	maximumToolSanitizationPointerBytes      = 256
	maximumToolSanitizationOutputFieldBytes  = 64
	maximumToolSanitizationStringBytes       = 256
	maximumToolSanitizationNumberBytes       = 128
	maximumToolSanitizationAllowedStringEnum = 64
)

var (
	ErrToolSanitizationPolicyInvalid     = errors.New("tool result sanitization policy is invalid")
	ErrToolSanitizationPolicyNotFound    = errors.New("tool result sanitization policy was not found")
	ErrToolSanitizationEvidenceInvalid   = errors.New("tool result sanitization evidence is invalid")
	ErrToolSanitizationInputInvalid      = errors.New("tool result sanitization input is invalid")
	ErrToolSanitizationLimitExceeded     = errors.New("tool result sanitization limit was exceeded")
	ErrToolSanitizationSensitiveValue    = errors.New("tool result sanitization rejected a sensitive value")
	toolSanitizationSensitiveIdentifiers = map[string]struct{}{
		"api_key": {}, "apikey": {}, "authorization": {}, "cookie": {}, "credential": {}, "file_id": {}, "file_uri": {},
		"password": {}, "passwd": {}, "private_key": {}, "secret": {}, "signature": {}, "token": {}, "uri": {}, "url": {},
	}
)

type ToolResultScalarType string

const (
	ToolResultScalarString  ToolResultScalarType = "string"
	ToolResultScalarNumber  ToolResultScalarType = "number"
	ToolResultScalarBoolean ToolResultScalarType = "boolean"
)

type ToolResultProjectionRule struct {
	JSONPointer         string
	OutputField         string
	ValueType           ToolResultScalarType
	MaxStringBytes      int
	MaxNumberBytes      int
	AllowedStringValues []string
}

type ToolResultSanitizationPolicy struct {
	SanitizerVersion   int
	Version            string
	ToolIdentityDigest string
	SchemaDigest       string
	MaxInputBytes      int
	MaxOutputBytes     int
	MaxDepth           int
	Rules              []ToolResultProjectionRule
}

type ToolResultSanitizationRequest struct {
	SanitizerVersion int
	PolicyVersion    string
	Evidence         ToolCompactionStructuralEvidence
	Result           any
}

type ToolResultSanitizationOutput struct {
	sanitizerVersion   int
	policyVersion      string
	policyDigest       string
	sourceResultDigest string
	projectionDigest   string
	fields             map[string]json.RawMessage
	integrityDigest    string
}

type toolResultSanitizationSerializableOutput struct {
	SanitizerVersion   int                        `json:"sanitizer_version"`
	PolicyVersion      string                     `json:"policy_version"`
	PolicyDigest       string                     `json:"policy_digest"`
	SourceResultDigest string                     `json:"source_result_digest"`
	ProjectionDigest   string                     `json:"projection_digest"`
	Fields             map[string]json.RawMessage `json:"fields"`
}

func (output ToolResultSanitizationOutput) String() string {
	return fmt.Sprintf("ToolResultSanitizationOutput{SanitizerVersion:%d PolicyVersion:%s Fields:%d}", output.sanitizerVersion, output.policyVersion, len(output.fields))
}

func (output ToolResultSanitizationOutput) GoString() string { return output.String() }

func (output ToolResultSanitizationOutput) MarshalJSON() ([]byte, error) {
	if err := output.Validate(); err != nil {
		return nil, err
	}
	return common.Marshal(output.serializable())
}

func (output ToolResultSanitizationOutput) serializable() toolResultSanitizationSerializableOutput {
	return toolResultSanitizationSerializableOutput{
		SanitizerVersion:   output.sanitizerVersion,
		PolicyVersion:      output.policyVersion,
		PolicyDigest:       output.policyDigest,
		SourceResultDigest: output.sourceResultDigest,
		ProjectionDigest:   output.projectionDigest,
		Fields:             output.fields,
	}
}

func (output ToolResultSanitizationOutput) Validate() error {
	encodedFields, err := common.Marshal(output.fields)
	if err != nil || output.sanitizerVersion != ToolResultSanitizerVersion ||
		!validToolSanitizationIdentifier(output.policyVersion, maximumToolSanitizationOutputFieldBytes) ||
		!validToolCompactionDigest(output.policyDigest) || !validToolCompactionDigest(output.sourceResultDigest) ||
		!validToolCompactionDigest(output.projectionDigest) || digestBytes(encodedFields) != output.projectionDigest ||
		output.integrityDigest == "" || output.integrityDigest != digestValue(output.serializable()) {
		return ErrToolSanitizationEvidenceInvalid
	}
	return nil
}

func (output ToolResultSanitizationOutput) SanitizerVersion() int { return output.sanitizerVersion }
func (output ToolResultSanitizationOutput) PolicyVersion() string { return output.policyVersion }
func (output ToolResultSanitizationOutput) PolicyDigest() string  { return output.policyDigest }
func (output ToolResultSanitizationOutput) SourceResultDigest() string {
	return output.sourceResultDigest
}
func (output ToolResultSanitizationOutput) ProjectionDigest() string { return output.projectionDigest }

func (output ToolResultSanitizationOutput) Fields() map[string]json.RawMessage {
	fields := make(map[string]json.RawMessage, len(output.fields))
	for name, value := range output.fields {
		fields[name] = append(json.RawMessage(nil), value...)
	}
	return fields
}

type registeredToolResultSanitizationPolicy struct {
	policy      ToolResultSanitizationPolicy
	digest      string
	pointerRoot *toolSanitizationPointerNode
}

type toolSanitizationPointerNode struct {
	children map[string]*toolSanitizationPointerNode
	scalar   bool
}

type ToolResultSanitizationRegistry struct {
	policies map[string]registeredToolResultSanitizationPolicy
}

func NewToolResultSanitizationRegistry(policies []ToolResultSanitizationPolicy) (*ToolResultSanitizationRegistry, error) {
	if len(policies) > maximumToolSanitizationPolicies {
		return nil, ErrToolSanitizationLimitExceeded
	}
	registry := &ToolResultSanitizationRegistry{policies: make(map[string]registeredToolResultSanitizationPolicy, len(policies))}
	for _, policy := range policies {
		validated, digest, err := validateToolResultSanitizationPolicy(policy)
		if err != nil {
			return nil, err
		}
		key := toolResultSanitizationPolicyKey(validated.SanitizerVersion, validated.Version, validated.ToolIdentityDigest, validated.SchemaDigest)
		if _, exists := registry.policies[key]; exists {
			return nil, ErrToolSanitizationPolicyInvalid
		}
		registry.policies[key] = registeredToolResultSanitizationPolicy{
			policy: validated, digest: digest, pointerRoot: newToolSanitizationPointerTree(validated.Rules),
		}
	}
	return registry, nil
}

func (registry *ToolResultSanitizationRegistry) Sanitize(request ToolResultSanitizationRequest) (ToolResultSanitizationOutput, error) {
	if registry == nil || registry.policies == nil {
		return ToolResultSanitizationOutput{}, ErrToolSanitizationPolicyNotFound
	}
	if err := request.Evidence.Validate(); err != nil {
		return ToolResultSanitizationOutput{}, ErrToolSanitizationEvidenceInvalid
	}
	registered, found := registry.policies[toolResultSanitizationPolicyKey(request.SanitizerVersion, request.PolicyVersion, request.Evidence.ToolIdentityDigest, request.Evidence.SchemaDigest)]
	if !found {
		return ToolResultSanitizationOutput{}, ErrToolSanitizationPolicyNotFound
	}
	encodedSource, err := common.Marshal(request.Result)
	if err != nil {
		return ToolResultSanitizationOutput{}, ErrToolSanitizationInputInvalid
	}
	if len(encodedSource) > registered.policy.MaxInputBytes {
		return ToolResultSanitizationOutput{}, ErrToolSanitizationLimitExceeded
	}
	if digestBytes(encodedSource) != request.Evidence.ResultDigest {
		return ToolResultSanitizationOutput{}, ErrToolSanitizationEvidenceInvalid
	}
	encodedResult := encodedSource
	if common.GetJsonType(encodedSource) == "string" {
		var structuredResult string
		if err := common.Unmarshal(encodedSource, &structuredResult); err != nil {
			return ToolResultSanitizationOutput{}, ErrToolSanitizationInputInvalid
		}
		if len(structuredResult) > registered.policy.MaxInputBytes {
			return ToolResultSanitizationOutput{}, ErrToolSanitizationLimitExceeded
		}
		encodedResult = []byte(structuredResult)
	}
	if common.GetJsonType(encodedResult) != "object" {
		return ToolResultSanitizationOutput{}, ErrToolSanitizationInputInvalid
	}
	var validatedResult json.RawMessage
	if err := common.ValidateJsonUniqueObjectKeys(encodedResult); err != nil {
		return ToolResultSanitizationOutput{}, ErrToolSanitizationInputInvalid
	}
	if err := common.Unmarshal(encodedResult, &validatedResult); err != nil {
		return ToolResultSanitizationOutput{}, ErrToolSanitizationInputInvalid
	}
	encodedResult = validatedResult
	depth, err := toolSanitizationJSONDepth(encodedResult, registered.policy.MaxDepth)
	if err != nil {
		return ToolResultSanitizationOutput{}, ErrToolSanitizationInputInvalid
	}
	if depth > registered.policy.MaxDepth {
		return ToolResultSanitizationOutput{}, ErrToolSanitizationLimitExceeded
	}
	if err := validateToolSanitizationShape(encodedResult, registered.pointerRoot); err != nil {
		return ToolResultSanitizationOutput{}, err
	}

	fields := make(map[string]json.RawMessage, len(registered.policy.Rules))
	for _, rule := range registered.policy.Rules {
		value, err := resolveToolSanitizationJSONPointer(encodedResult, rule.JSONPointer)
		if err != nil {
			return ToolResultSanitizationOutput{}, ErrToolSanitizationInputInvalid
		}
		projected, err := sanitizeToolResultScalar(value, rule)
		if err != nil {
			return ToolResultSanitizationOutput{}, err
		}
		fields[rule.OutputField] = projected
	}
	encodedFields, err := common.Marshal(fields)
	if err != nil {
		return ToolResultSanitizationOutput{}, ErrToolSanitizationInputInvalid
	}
	output := ToolResultSanitizationOutput{
		sanitizerVersion:   ToolResultSanitizerVersion,
		policyVersion:      registered.policy.Version,
		policyDigest:       registered.digest,
		sourceResultDigest: request.Evidence.ResultDigest,
		projectionDigest:   digestBytes(encodedFields),
		fields:             fields,
	}
	encodedOutput, err := common.Marshal(output.serializable())
	if err != nil {
		return ToolResultSanitizationOutput{}, ErrToolSanitizationInputInvalid
	}
	if len(encodedOutput) > registered.policy.MaxOutputBytes {
		return ToolResultSanitizationOutput{}, ErrToolSanitizationLimitExceeded
	}
	output.integrityDigest = digestValue(output.serializable())
	return output, nil
}

func validateToolResultSanitizationPolicy(policy ToolResultSanitizationPolicy) (ToolResultSanitizationPolicy, string, error) {
	if policy.SanitizerVersion != ToolResultSanitizerVersion || strings.TrimSpace(policy.Version) != policy.Version ||
		!validToolSanitizationIdentifier(policy.Version, maximumToolSanitizationOutputFieldBytes) ||
		!validToolCompactionDigest(policy.ToolIdentityDigest) || !validToolCompactionDigest(policy.SchemaDigest) ||
		policy.MaxInputBytes <= 0 || policy.MaxInputBytes > maximumToolSanitizationInputBytes ||
		policy.MaxOutputBytes <= 0 || policy.MaxOutputBytes > maximumToolSanitizationOutputBytes ||
		policy.MaxDepth <= 0 || policy.MaxDepth > maximumToolSanitizationDepth ||
		len(policy.Rules) == 0 || len(policy.Rules) > maximumToolSanitizationRules {
		return ToolResultSanitizationPolicy{}, "", ErrToolSanitizationPolicyInvalid
	}

	validatedRules := make([]ToolResultProjectionRule, 0, len(policy.Rules))
	seenPointers := make(map[string]struct{}, len(policy.Rules))
	seenOutputFields := make(map[string]struct{}, len(policy.Rules))
	pointerRoot := &toolSanitizationPointerNode{children: make(map[string]*toolSanitizationPointerNode)}
	for _, rule := range policy.Rules {
		validated, err := validateToolResultProjectionRule(rule, policy.MaxDepth)
		if err != nil {
			return ToolResultSanitizationPolicy{}, "", err
		}
		if _, exists := seenPointers[validated.JSONPointer]; exists {
			return ToolResultSanitizationPolicy{}, "", ErrToolSanitizationPolicyInvalid
		}
		segments, _ := parseToolSanitizationJSONPointer(validated.JSONPointer)
		if !insertToolSanitizationPointer(pointerRoot, segments) {
			return ToolResultSanitizationPolicy{}, "", ErrToolSanitizationPolicyInvalid
		}
		if _, exists := seenOutputFields[validated.OutputField]; exists {
			return ToolResultSanitizationPolicy{}, "", ErrToolSanitizationPolicyInvalid
		}
		seenPointers[validated.JSONPointer] = struct{}{}
		seenOutputFields[validated.OutputField] = struct{}{}
		validatedRules = append(validatedRules, validated)
	}
	sortToolResultProjectionRules(validatedRules)
	policy.Rules = validatedRules
	digest := digestValue(struct {
		SanitizerVersion int
		Policy           ToolResultSanitizationPolicy
	}{ToolResultSanitizerVersion, policy})
	return policy, digest, nil
}

func validateToolResultProjectionRule(rule ToolResultProjectionRule, maximumDepth int) (ToolResultProjectionRule, error) {
	if strings.TrimSpace(rule.JSONPointer) != rule.JSONPointer || strings.TrimSpace(rule.OutputField) != rule.OutputField {
		return ToolResultProjectionRule{}, ErrToolSanitizationPolicyInvalid
	}
	segments, err := parseToolSanitizationJSONPointer(rule.JSONPointer)
	if err != nil || len(rule.JSONPointer) > maximumToolSanitizationPointerBytes || len(segments) > maximumDepth ||
		!validToolSanitizationIdentifier(rule.OutputField, maximumToolSanitizationOutputFieldBytes) ||
		toolSanitizationSensitiveIdentifier(rule.OutputField) {
		return ToolResultProjectionRule{}, ErrToolSanitizationPolicyInvalid
	}
	for _, segment := range segments {
		if toolSanitizationSensitiveIdentifier(segment) {
			return ToolResultProjectionRule{}, ErrToolSanitizationPolicyInvalid
		}
	}

	switch rule.ValueType {
	case ToolResultScalarString:
		if rule.MaxStringBytes <= 0 || rule.MaxStringBytes > maximumToolSanitizationStringBytes || rule.MaxNumberBytes != 0 ||
			len(rule.AllowedStringValues) == 0 || len(rule.AllowedStringValues) > maximumToolSanitizationAllowedStringEnum {
			return ToolResultProjectionRule{}, ErrToolSanitizationPolicyInvalid
		}
		seenValues := make(map[string]struct{}, len(rule.AllowedStringValues))
		allowedValues := make([]string, 0, len(rule.AllowedStringValues))
		for _, value := range rule.AllowedStringValues {
			if value == "" || !utf8.ValidString(value) || len(value) > rule.MaxStringBytes ||
				!validToolSanitizationIdentifier(value, rule.MaxStringBytes) || toolSanitizationSensitiveString(value) {
				return ToolResultProjectionRule{}, ErrToolSanitizationPolicyInvalid
			}
			if _, exists := seenValues[value]; exists {
				return ToolResultProjectionRule{}, ErrToolSanitizationPolicyInvalid
			}
			seenValues[value] = struct{}{}
			allowedValues = append(allowedValues, value)
		}
		rule.AllowedStringValues = allowedValues
	case ToolResultScalarNumber:
		if rule.MaxStringBytes != 0 || rule.MaxNumberBytes <= 0 || rule.MaxNumberBytes > maximumToolSanitizationNumberBytes ||
			len(rule.AllowedStringValues) != 0 {
			return ToolResultProjectionRule{}, ErrToolSanitizationPolicyInvalid
		}
	case ToolResultScalarBoolean:
		if rule.MaxStringBytes != 0 || rule.MaxNumberBytes != 0 || len(rule.AllowedStringValues) != 0 {
			return ToolResultProjectionRule{}, ErrToolSanitizationPolicyInvalid
		}
	default:
		return ToolResultProjectionRule{}, ErrToolSanitizationPolicyInvalid
	}
	return rule, nil
}

func sanitizeToolResultScalar(value json.RawMessage, rule ToolResultProjectionRule) (json.RawMessage, error) {
	if common.GetJsonType(value) != string(rule.ValueType) {
		return nil, ErrToolSanitizationInputInvalid
	}
	switch rule.ValueType {
	case ToolResultScalarString:
		var decoded string
		if err := common.Unmarshal(value, &decoded); err != nil || len(decoded) > rule.MaxStringBytes || toolSanitizationSensitiveString(decoded) {
			return nil, ErrToolSanitizationSensitiveValue
		}
		for _, allowed := range rule.AllowedStringValues {
			if decoded == allowed {
				return append(json.RawMessage(nil), value...), nil
			}
		}
		return nil, ErrToolSanitizationSensitiveValue
	case ToolResultScalarNumber:
		if len(value) > rule.MaxNumberBytes {
			return nil, ErrToolSanitizationLimitExceeded
		}
		var number json.Number
		if err := common.Unmarshal(value, &number); err != nil {
			return nil, ErrToolSanitizationInputInvalid
		}
		return append(json.RawMessage(nil), value...), nil
	case ToolResultScalarBoolean:
		var boolean bool
		if err := common.Unmarshal(value, &boolean); err != nil {
			return nil, ErrToolSanitizationInputInvalid
		}
		return append(json.RawMessage(nil), value...), nil
	default:
		return nil, ErrToolSanitizationPolicyInvalid
	}
}

func resolveToolSanitizationJSONPointer(root json.RawMessage, pointer string) (json.RawMessage, error) {
	segments, err := parseToolSanitizationJSONPointer(pointer)
	if err != nil {
		return nil, err
	}
	current := append(json.RawMessage(nil), root...)
	for _, segment := range segments {
		switch common.GetJsonType(current) {
		case "object":
			var object map[string]json.RawMessage
			if err := common.Unmarshal(current, &object); err != nil {
				return nil, ErrToolSanitizationInputInvalid
			}
			value, found := object[segment]
			if !found {
				return nil, ErrToolSanitizationInputInvalid
			}
			current = value
		case "array":
			if segment == "" || (len(segment) > 1 && segment[0] == '0') {
				return nil, ErrToolSanitizationInputInvalid
			}
			index, err := strconv.Atoi(segment)
			if err != nil || index < 0 {
				return nil, ErrToolSanitizationInputInvalid
			}
			var array []json.RawMessage
			if err := common.Unmarshal(current, &array); err != nil || index >= len(array) {
				return nil, ErrToolSanitizationInputInvalid
			}
			current = array[index]
		default:
			return nil, ErrToolSanitizationInputInvalid
		}
	}
	return append(json.RawMessage(nil), current...), nil
}

func newToolSanitizationPointerTree(rules []ToolResultProjectionRule) *toolSanitizationPointerNode {
	root := &toolSanitizationPointerNode{children: make(map[string]*toolSanitizationPointerNode)}
	for _, rule := range rules {
		segments, _ := parseToolSanitizationJSONPointer(rule.JSONPointer)
		insertToolSanitizationPointer(root, segments)
	}
	return root
}

func insertToolSanitizationPointer(root *toolSanitizationPointerNode, segments []string) bool {
	current := root
	for _, segment := range segments {
		if current.scalar {
			return false
		}
		child := current.children[segment]
		if child == nil {
			child = &toolSanitizationPointerNode{children: make(map[string]*toolSanitizationPointerNode)}
			current.children[segment] = child
		}
		current = child
	}
	if current.scalar || len(current.children) > 0 {
		return false
	}
	current.scalar = true
	return true
}

func validateToolSanitizationShape(value json.RawMessage, node *toolSanitizationPointerNode) error {
	if node == nil {
		return ErrToolSanitizationInputInvalid
	}
	switch common.GetJsonType(value) {
	case "object":
		if node.scalar {
			return ErrToolSanitizationInputInvalid
		}
		var object map[string]json.RawMessage
		if err := common.Unmarshal(value, &object); err != nil {
			return ErrToolSanitizationInputInvalid
		}
		for name, childValue := range object {
			if toolSanitizationSensitiveIdentifier(name) {
				return ErrToolSanitizationSensitiveValue
			}
			if err := validateToolSanitizationShape(childValue, node.children[name]); err != nil {
				return err
			}
		}
	case "array":
		if node.scalar {
			return ErrToolSanitizationInputInvalid
		}
		var array []json.RawMessage
		if err := common.Unmarshal(value, &array); err != nil {
			return ErrToolSanitizationInputInvalid
		}
		for index, childValue := range array {
			if err := validateToolSanitizationShape(childValue, node.children[strconv.Itoa(index)]); err != nil {
				return err
			}
		}
	default:
		if !node.scalar {
			return ErrToolSanitizationInputInvalid
		}
	}
	return nil
}

func parseToolSanitizationJSONPointer(pointer string) ([]string, error) {
	if pointer == "" || pointer[0] != '/' {
		return nil, ErrToolSanitizationPolicyInvalid
	}
	rawSegments := strings.Split(pointer[1:], "/")
	segments := make([]string, 0, len(rawSegments))
	for _, rawSegment := range rawSegments {
		var builder strings.Builder
		for index := 0; index < len(rawSegment); index++ {
			if rawSegment[index] != '~' {
				builder.WriteByte(rawSegment[index])
				continue
			}
			if index+1 >= len(rawSegment) || (rawSegment[index+1] != '0' && rawSegment[index+1] != '1') {
				return nil, ErrToolSanitizationPolicyInvalid
			}
			index++
			if rawSegment[index] == '0' {
				builder.WriteByte('~')
			} else {
				builder.WriteByte('/')
			}
		}
		segment := builder.String()
		if segment == "" || !utf8.ValidString(segment) {
			return nil, ErrToolSanitizationPolicyInvalid
		}
		segments = append(segments, segment)
	}
	return segments, nil
}

func toolSanitizationJSONDepth(value json.RawMessage, maximumDepth int) (int, error) {
	if maximumDepth <= 0 {
		return maximumDepth + 1, nil
	}
	switch common.GetJsonType(value) {
	case "object":
		var object map[string]json.RawMessage
		if err := common.Unmarshal(value, &object); err != nil {
			return 0, err
		}
		maximumChildDepth := 0
		for _, child := range object {
			childDepth, err := toolSanitizationJSONDepth(child, maximumDepth-1)
			if err != nil {
				return 0, err
			}
			if childDepth > maximumChildDepth {
				maximumChildDepth = childDepth
			}
		}
		return maximumChildDepth + 1, nil
	case "array":
		var array []json.RawMessage
		if err := common.Unmarshal(value, &array); err != nil {
			return 0, err
		}
		maximumChildDepth := 0
		for _, child := range array {
			childDepth, err := toolSanitizationJSONDepth(child, maximumDepth-1)
			if err != nil {
				return 0, err
			}
			if childDepth > maximumChildDepth {
				maximumChildDepth = childDepth
			}
		}
		return maximumChildDepth + 1, nil
	case "string", "number", "boolean", "null":
		return 1, nil
	default:
		return 0, ErrToolSanitizationInputInvalid
	}
}

func validToolSanitizationIdentifier(value string, maximumBytes int) bool {
	if value == "" || len(value) > maximumBytes {
		return false
	}
	for index, character := range value {
		if (character >= 'a' && character <= 'z') || (character >= 'A' && character <= 'Z') ||
			(index > 0 && character >= '0' && character <= '9') || (index > 0 && (character == '_' || character == '-' || character == '.')) {
			continue
		}
		return false
	}
	return true
}

func toolSanitizationSensitiveIdentifier(value string) bool {
	normalized := strings.ToLower(strings.TrimSpace(value))
	parts := strings.FieldsFunc(normalized, func(character rune) bool {
		return !(unicode.IsLetter(character) || unicode.IsDigit(character))
	})
	joined := strings.Join(parts, "_")
	if _, sensitive := toolSanitizationSensitiveIdentifiers[joined]; sensitive {
		return true
	}
	compact := strings.Join(parts, "")
	for _, sensitiveTerm := range []string{"apikey", "authorization", "cookie", "credential", "fileid", "fileuri", "password", "passwd", "privatekey", "secret", "signature", "token", "uri", "url", "email", "phone", "address", "payment", "card", "account"} {
		if strings.Contains(compact, sensitiveTerm) {
			return true
		}
	}
	for _, part := range parts {
		if part == "authorization" || part == "cookie" || part == "credential" || part == "password" || part == "passwd" ||
			part == "secret" || part == "signature" || part == "token" || part == "url" || part == "uri" {
			return true
		}
	}
	return false
}

func toolSanitizationSensitiveString(value string) bool {
	trimmed := strings.TrimSpace(value)
	lower := strings.ToLower(trimmed)
	if trimmed == "" || strings.ContainsAny(trimmed, "@/?=&\r\n\t") || strings.Contains(lower, "://") ||
		strings.HasPrefix(lower, "data:") || strings.HasPrefix(lower, "file:") || strings.HasPrefix(lower, "file-") ||
		strings.HasPrefix(lower, "file_") || strings.HasPrefix(lower, "bearer") || strings.HasPrefix(lower, "basic") ||
		strings.HasPrefix(lower, "sk-") || strings.HasPrefix(lower, "pk-") || strings.HasPrefix(lower, "key-") ||
		strings.Contains(lower, "-----begin") || looksLikeJWT(trimmed) {
		return true
	}
	for _, character := range trimmed {
		if unicode.IsControl(character) {
			return true
		}
	}
	return false
}

func looksLikeJWT(value string) bool {
	parts := strings.Split(value, ".")
	if len(parts) != 3 {
		return false
	}
	for _, part := range parts {
		if len(part) < 4 {
			return false
		}
		for _, character := range part {
			if !((character >= 'a' && character <= 'z') || (character >= 'A' && character <= 'Z') ||
				(character >= '0' && character <= '9') || character == '-' || character == '_') {
				return false
			}
		}
	}
	return true
}

func sortToolResultProjectionRules(rules []ToolResultProjectionRule) {
	sort.Slice(rules, func(left, right int) bool { return rules[left].OutputField < rules[right].OutputField })
}

func toolResultSanitizationPolicyKey(sanitizerVersion int, policyVersion, toolIdentityDigest, schemaDigest string) string {
	return strconv.Itoa(sanitizerVersion) + "\x00" + policyVersion + "\x00" + toolIdentityDigest + "\x00" + schemaDigest
}
