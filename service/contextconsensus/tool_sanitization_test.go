package contextconsensus

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestToolResultSanitizationRegistryProjectsOnlyRegisteredSafeScalars(t *testing.T) {
	result := map[string]any{
		"status": "ok",
		"count":  float64(3),
		"active": true,
	}
	evidence := toolSanitizationEvidenceForTest(result)
	policy := validToolSanitizationPolicyForTest(evidence)
	registry, err := NewToolResultSanitizationRegistry([]ToolResultSanitizationPolicy{policy})
	require.NoError(t, err)

	policy.Version = "mutated-after-registration"
	policy.Rules[0].AllowedStringValues[0] = "mutated"
	output, err := registry.Sanitize(ToolResultSanitizationRequest{
		SanitizerVersion: ToolResultSanitizerVersion,
		PolicyVersion:    "tool-policy-v1",
		Evidence:         evidence,
		Result:           result,
	})
	require.NoError(t, err)
	require.NoError(t, output.Validate())
	assert.Equal(t, ToolResultSanitizerVersion, output.SanitizerVersion())
	assert.Equal(t, "tool-policy-v1", output.PolicyVersion())
	assert.Equal(t, evidence.ResultDigest, output.SourceResultDigest())
	fields := output.Fields()
	assert.Len(t, fields, 3)
	assert.JSONEq(t, `"ok"`, string(fields["outcome"]))
	assert.JSONEq(t, `3`, string(fields["item_count"]))
	assert.JSONEq(t, `true`, string(fields["is_active"]))

	encoded, err := common.Marshal(output)
	require.NoError(t, err)
	assert.NotContains(t, string(encoded), "injected")
	assert.NotContains(t, output.String(), "injected")
	assert.Equal(t, output.String(), output.GoString())

	reorderedResult := map[string]any{"active": true, "count": float64(3), "status": "ok"}
	reorderedOutput, err := registry.Sanitize(ToolResultSanitizationRequest{
		SanitizerVersion: ToolResultSanitizerVersion, PolicyVersion: "tool-policy-v1", Evidence: evidence, Result: reorderedResult,
	})
	require.NoError(t, err)
	assert.Equal(t, output.ProjectionDigest(), reorderedOutput.ProjectionDigest())
	assert.Equal(t, output.PolicyDigest(), reorderedOutput.PolicyDigest())

	fields["outcome"] = json.RawMessage(`"injected"`)
	assert.JSONEq(t, `"ok"`, string(output.Fields()["outcome"]))
	tamperedOutput := output
	tamperedOutput.fields["outcome"] = json.RawMessage(`"injected"`)
	require.ErrorIs(t, tamperedOutput.Validate(), ErrToolSanitizationEvidenceInvalid)
	_, err = common.Marshal(tamperedOutput)
	require.ErrorIs(t, err, ErrToolSanitizationEvidenceInvalid)
}

func TestToolResultSanitizationRegistryAcceptsAssessedChatJSONStringResult(t *testing.T) {
	result := `{"status":"ok","count":3,"active":true}`
	body := []byte(`{
  "model":"gpt-5",
  "messages":[
    {"role":"user","content":"lookup"},
    {"role":"assistant","tool_calls":[{"id":"call-1","type":"function","function":{"name":"lookup","arguments":"{}"}}]},
    {"role":"tool","tool_call_id":"call-1","content":"{\"status\":\"ok\",\"count\":3,\"active\":true}"},
    {"role":"assistant","content":"done"},
    {"role":"user","content":"continue"}
  ],
  "tools":[{"type":"function","function":{"name":"lookup","parameters":{"type":"object"}}}]
}`)
	envelope, err := Extract(ExtractionRequest{Protocol: "openai", Body: body})
	require.NoError(t, err)
	assessment := AssessSingleSerialToolCompaction(envelope)
	require.True(t, assessment.ReadyForSanitization)
	require.NotNil(t, assessment.Evidence)

	policy := validToolSanitizationPolicyForTest(*assessment.Evidence)
	registry, err := NewToolResultSanitizationRegistry([]ToolResultSanitizationPolicy{policy})
	require.NoError(t, err)
	output, err := registry.Sanitize(ToolResultSanitizationRequest{
		SanitizerVersion: ToolResultSanitizerVersion, PolicyVersion: policy.Version,
		Evidence: *assessment.Evidence, Result: result,
	})
	require.NoError(t, err)
	require.NoError(t, output.Validate())
	assert.JSONEq(t, `"ok"`, string(output.Fields()["outcome"]))

	_, err = registry.Sanitize(ToolResultSanitizationRequest{
		SanitizerVersion: ToolResultSanitizerVersion, PolicyVersion: policy.Version,
		Evidence: *assessment.Evidence, Result: result + " ",
	})
	require.ErrorIs(t, err, ErrToolSanitizationEvidenceInvalid)
}

func TestToolResultSanitizationRegistryDefaultsToExactDeny(t *testing.T) {
	result := map[string]any{"status": "ok", "count": float64(3), "active": true}
	evidence := toolSanitizationEvidenceForTest(result)
	emptyRegistry, err := NewToolResultSanitizationRegistry(nil)
	require.NoError(t, err)

	_, err = emptyRegistry.Sanitize(ToolResultSanitizationRequest{SanitizerVersion: ToolResultSanitizerVersion, PolicyVersion: "tool-policy-v1", Evidence: evidence, Result: result})
	require.ErrorIs(t, err, ErrToolSanitizationPolicyNotFound)

	registry, err := NewToolResultSanitizationRegistry([]ToolResultSanitizationPolicy{validToolSanitizationPolicyForTest(evidence)})
	require.NoError(t, err)
	_, err = registry.Sanitize(ToolResultSanitizationRequest{SanitizerVersion: ToolResultSanitizerVersion, PolicyVersion: "tool-policy-v2", Evidence: evidence, Result: result})
	require.ErrorIs(t, err, ErrToolSanitizationPolicyNotFound)
	_, err = registry.Sanitize(ToolResultSanitizationRequest{SanitizerVersion: ToolResultSanitizerVersion, PolicyVersion: " tool-policy-v1 ", Evidence: evidence, Result: result})
	require.ErrorIs(t, err, ErrToolSanitizationPolicyNotFound)
	_, err = registry.Sanitize(ToolResultSanitizationRequest{SanitizerVersion: ToolResultSanitizerVersion + 1, PolicyVersion: "tool-policy-v1", Evidence: evidence, Result: result})
	require.ErrorIs(t, err, ErrToolSanitizationPolicyNotFound)

	tamperedEvidence := evidence
	tamperedEvidence.ResultDigest = digestString("tampered")
	_, err = registry.Sanitize(ToolResultSanitizationRequest{SanitizerVersion: ToolResultSanitizerVersion, PolicyVersion: "tool-policy-v1", Evidence: tamperedEvidence, Result: result})
	require.ErrorIs(t, err, ErrToolSanitizationEvidenceInvalid)

	_, err = registry.Sanitize(ToolResultSanitizationRequest{
		SanitizerVersion: ToolResultSanitizerVersion, PolicyVersion: "tool-policy-v1", Evidence: evidence, Result: map[string]any{"status": "ok", "count": float64(4), "active": true},
	})
	require.ErrorIs(t, err, ErrToolSanitizationEvidenceInvalid)
}

func TestToolResultSanitizationRegistryRejectsUnsafeInput(t *testing.T) {
	tests := []struct {
		name          string
		result        any
		mutatePolicy  func(*ToolResultSanitizationPolicy)
		expectedError error
	}{
		{name: "non object", result: "not-an-object", expectedError: ErrToolSanitizationInputInvalid},
		{name: "missing pointer", result: map[string]any{"status": "ok", "count": float64(3)}, expectedError: ErrToolSanitizationInputInvalid},
		{name: "wrong scalar type", result: map[string]any{"status": float64(1), "count": float64(3), "active": true}, expectedError: ErrToolSanitizationInputInvalid},
		{name: "string outside allowlist", result: map[string]any{"status": "unknown", "count": float64(3), "active": true}, expectedError: ErrToolSanitizationSensitiveValue},
		{name: "url string", result: map[string]any{"status": "https://private.example/file", "count": float64(3), "active": true}, expectedError: ErrToolSanitizationSensitiveValue},
		{name: "unknown field", result: map[string]any{"status": "ok", "count": float64(3), "active": true, "other": false}, expectedError: ErrToolSanitizationInputInvalid},
		{name: "unknown sensitive field", result: map[string]any{"status": "ok", "count": float64(3), "active": true, "accessToken": "short"}, expectedError: ErrToolSanitizationSensitiveValue},
		{
			name: "depth exceeded",
			result: map[string]any{"status": "ok", "count": float64(3), "active": true,
				"unknown": map[string]any{"level2": map[string]any{"level3": map[string]any{"level4": true}}}},
			expectedError: ErrToolSanitizationLimitExceeded,
		},
		{
			name:          "input bytes exceeded",
			result:        map[string]any{"status": "ok", "count": float64(3), "active": true, "unknown": strings.Repeat("x", 512)},
			mutatePolicy:  func(policy *ToolResultSanitizationPolicy) { policy.MaxInputBytes = 128 },
			expectedError: ErrToolSanitizationLimitExceeded,
		},
		{
			name:          "output bytes exceeded",
			result:        map[string]any{"status": "ok", "count": float64(3), "active": true},
			mutatePolicy:  func(policy *ToolResultSanitizationPolicy) { policy.MaxOutputBytes = 128 },
			expectedError: ErrToolSanitizationLimitExceeded,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			evidence := toolSanitizationEvidenceForTest(test.result)
			policy := validToolSanitizationPolicyForTest(evidence)
			if test.mutatePolicy != nil {
				test.mutatePolicy(&policy)
			}
			registry, err := NewToolResultSanitizationRegistry([]ToolResultSanitizationPolicy{policy})
			require.NoError(t, err)
			_, err = registry.Sanitize(ToolResultSanitizationRequest{SanitizerVersion: ToolResultSanitizerVersion, PolicyVersion: policy.Version, Evidence: evidence, Result: test.result})
			require.ErrorIs(t, err, test.expectedError)
			assert.NotContains(t, err.Error(), "private.example")
		})
	}
}

func TestToolResultSanitizationRegistryRejectsDuplicateJSONKeys(t *testing.T) {
	result := `{"status":"failed","status":"ok","count":3,"active":true}`
	evidence := toolSanitizationEvidenceForTest(result)
	policy := validToolSanitizationPolicyForTest(evidence)
	registry, err := NewToolResultSanitizationRegistry([]ToolResultSanitizationPolicy{policy})
	require.NoError(t, err)

	_, err = registry.Sanitize(ToolResultSanitizationRequest{
		SanitizerVersion: ToolResultSanitizerVersion, PolicyVersion: policy.Version, Evidence: evidence, Result: result,
	})
	require.ErrorIs(t, err, ErrToolSanitizationInputInvalid)
}

func TestToolResultSanitizationRegistrySupportsEscapedPointersAndArrayIndexes(t *testing.T) {
	result := map[string]any{
		"items": []any{map[string]any{"state": "ready"}},
		"a/b":   map[string]any{"~flag": true},
	}
	evidence := toolSanitizationEvidenceForTest(result)
	policy := ToolResultSanitizationPolicy{
		SanitizerVersion: ToolResultSanitizerVersion, Version: "escaped-policy-v1", ToolIdentityDigest: evidence.ToolIdentityDigest, SchemaDigest: evidence.SchemaDigest,
		MaxInputBytes: 4096, MaxOutputBytes: 2048, MaxDepth: 4,
		Rules: []ToolResultProjectionRule{
			{JSONPointer: "/items/0/state", OutputField: "item_state", ValueType: ToolResultScalarString, MaxStringBytes: 16, AllowedStringValues: []string{"ready"}},
			{JSONPointer: "/a~1b/~0flag", OutputField: "flag", ValueType: ToolResultScalarBoolean},
		},
	}
	registry, err := NewToolResultSanitizationRegistry([]ToolResultSanitizationPolicy{policy})
	require.NoError(t, err)

	output, err := registry.Sanitize(ToolResultSanitizationRequest{SanitizerVersion: ToolResultSanitizerVersion, PolicyVersion: policy.Version, Evidence: evidence, Result: result})
	require.NoError(t, err)
	assert.JSONEq(t, `"ready"`, string(output.Fields()["item_state"]))
	assert.JSONEq(t, `true`, string(output.Fields()["flag"]))
}

func TestNewToolResultSanitizationRegistryRejectsUnsafePolicies(t *testing.T) {
	evidence := toolSanitizationEvidenceForTest(map[string]any{"status": "ok", "count": float64(3), "active": true})
	tests := []struct {
		name   string
		mutate func(*ToolResultSanitizationPolicy)
	}{
		{name: "invalid version", mutate: func(policy *ToolResultSanitizationPolicy) { policy.Version = "bad version" }},
		{name: "unsupported sanitizer version", mutate: func(policy *ToolResultSanitizationPolicy) { policy.SanitizerVersion++ }},
		{name: "invalid digest", mutate: func(policy *ToolResultSanitizationPolicy) { policy.SchemaDigest = "invalid" }},
		{name: "no rules", mutate: func(policy *ToolResultSanitizationPolicy) { policy.Rules = nil }},
		{name: "invalid pointer escape", mutate: func(policy *ToolResultSanitizationPolicy) { policy.Rules[0].JSONPointer = "/status~2" }},
		{name: "pointer whitespace", mutate: func(policy *ToolResultSanitizationPolicy) { policy.Rules[0].JSONPointer = "/status " }},
		{name: "sensitive pointer", mutate: func(policy *ToolResultSanitizationPolicy) { policy.Rules[0].JSONPointer = "/auth/token" }},
		{name: "sensitive output", mutate: func(policy *ToolResultSanitizationPolicy) { policy.Rules[0].OutputField = "access_token" }},
		{name: "camel case sensitive pointer", mutate: func(policy *ToolResultSanitizationPolicy) { policy.Rules[0].JSONPointer = "/accessToken" }},
		{name: "camel case sensitive output", mutate: func(policy *ToolResultSanitizationPolicy) { policy.Rules[0].OutputField = "apiKey" }},
		{name: "string without enum", mutate: func(policy *ToolResultSanitizationPolicy) { policy.Rules[0].AllowedStringValues = nil }},
		{name: "url enum", mutate: func(policy *ToolResultSanitizationPolicy) {
			policy.Rules[0].AllowedStringValues = []string{"https://private.example"}
		}},
		{name: "duplicate pointer", mutate: func(policy *ToolResultSanitizationPolicy) { policy.Rules[1].JSONPointer = policy.Rules[0].JSONPointer }},
		{name: "pointer prefix conflict", mutate: func(policy *ToolResultSanitizationPolicy) { policy.Rules[1].JSONPointer = "/status/detail" }},
		{name: "duplicate output", mutate: func(policy *ToolResultSanitizationPolicy) { policy.Rules[1].OutputField = policy.Rules[0].OutputField }},
		{name: "excessive depth", mutate: func(policy *ToolResultSanitizationPolicy) { policy.MaxDepth = maximumToolSanitizationDepth + 1 }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			policy := validToolSanitizationPolicyForTest(evidence)
			test.mutate(&policy)
			_, err := NewToolResultSanitizationRegistry([]ToolResultSanitizationPolicy{policy})
			require.Error(t, err)
			assert.True(t, errors.Is(err, ErrToolSanitizationPolicyInvalid) || errors.Is(err, ErrToolSanitizationLimitExceeded))
			assert.NotContains(t, err.Error(), "private.example")
		})
	}

	policy := validToolSanitizationPolicyForTest(evidence)
	_, err := NewToolResultSanitizationRegistry([]ToolResultSanitizationPolicy{policy, policy})
	require.ErrorIs(t, err, ErrToolSanitizationPolicyInvalid)
}

func toolSanitizationEvidenceForTest(result any) ToolCompactionStructuralEvidence {
	evidence := ToolCompactionStructuralEvidence{
		Protocol: "openai", CallSequence: 1, ResultSequence: 2, Status: ToolExchangeCompleted,
		CallIdentityDigest: digestString("call-1"), ToolIdentityDigest: digestString("lookup"),
		ArgumentsDigest: digestValue(map[string]any{"query": "fixed"}), ResultDigest: digestValue(result), SchemaDigest: digestString("schema-v1"),
	}
	evidence.integrityDigest = digestValue(evidence)
	return evidence
}

func validToolSanitizationPolicyForTest(evidence ToolCompactionStructuralEvidence) ToolResultSanitizationPolicy {
	return ToolResultSanitizationPolicy{
		SanitizerVersion: ToolResultSanitizerVersion, Version: "tool-policy-v1", ToolIdentityDigest: evidence.ToolIdentityDigest, SchemaDigest: evidence.SchemaDigest,
		MaxInputBytes: 4096, MaxOutputBytes: 2048, MaxDepth: 4,
		Rules: []ToolResultProjectionRule{
			{JSONPointer: "/status", OutputField: "outcome", ValueType: ToolResultScalarString, MaxStringBytes: 16, AllowedStringValues: []string{"ok", "failed"}},
			{JSONPointer: "/count", OutputField: "item_count", ValueType: ToolResultScalarNumber, MaxNumberBytes: 32},
			{JSONPointer: "/active", OutputField: "is_active", ValueType: ToolResultScalarBoolean},
		},
	}
}
