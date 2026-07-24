package contextconsensus

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestEvaluateCompactionAuthorizationRequiresAllThreeGrants(t *testing.T) {
	policy := CompactionPolicy{
		SystemEnabled:        true,
		PolicyVersion:        "context-compaction-v1",
		PreservedRecentTurns: 2,
		TargetInputTokens:    4000,
		MaxSummaryTokens:     500,
	}

	tests := []struct {
		name              string
		systemEnabled     bool
		apiKeyAllowed     bool
		requestAuthorized bool
		wantAllowed       bool
		wantReasonCodes   []string
	}{
		{name: "all authorized", systemEnabled: true, apiKeyAllowed: true, requestAuthorized: true, wantAllowed: true, wantReasonCodes: []string{}},
		{name: "request denied", systemEnabled: true, apiKeyAllowed: true, wantReasonCodes: []string{"request_compaction_not_authorized"}},
		{name: "API key denied", systemEnabled: true, requestAuthorized: true, wantReasonCodes: []string{"api_key_compaction_denied"}},
		{name: "API key and request denied", systemEnabled: true, wantReasonCodes: []string{"api_key_compaction_denied", "request_compaction_not_authorized"}},
		{name: "system disabled", apiKeyAllowed: true, requestAuthorized: true, wantReasonCodes: []string{"system_compaction_disabled"}},
		{name: "system and request denied", apiKeyAllowed: true, wantReasonCodes: []string{"system_compaction_disabled", "request_compaction_not_authorized"}},
		{name: "system and API key denied", requestAuthorized: true, wantReasonCodes: []string{"system_compaction_disabled", "api_key_compaction_denied"}},
		{name: "all denied", wantReasonCodes: []string{"system_compaction_disabled", "api_key_compaction_denied", "request_compaction_not_authorized"}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			snapshot := policy.Snapshot(test.apiKeyAllowed, test.requestAuthorized)
			snapshot.SystemEnabled = test.systemEnabled
			decision := EvaluateCompactionAuthorization(snapshot)
			assert.Equal(t, test.wantAllowed, decision.Allowed)
			assert.Equal(t, test.wantReasonCodes, decision.ReasonCodes)
		})
	}
}
