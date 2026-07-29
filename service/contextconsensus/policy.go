package contextconsensus

import "github.com/QuantumNous/new-api/relaykit/types"

type CompactionPolicy struct {
	SystemEnabled        bool
	PolicyVersion        string
	PreservedRecentTurns int
	TargetInputTokens    int
	MaxSummaryTokens     int
}

// ManagedContextRequest is the gateway-only managed session contract captured
// from request headers. ExternalContextID must remain request-local and must not
// be persisted or written to logs.
type ManagedContextRequest struct {
	ExternalContextID       string
	ExpectedRevision        uint64
	Protocol                types.RelayFormat
	IncrementalSourceDigest string
	CurrentUserText         string
}

type CompactionPolicySnapshot struct {
	SystemEnabled        bool   `json:"system_enabled"`
	APIKeyAllowed        bool   `json:"api_key_allowed"`
	RequestAuthorized    bool   `json:"request_authorized"`
	PolicyVersion        string `json:"policy_version"`
	PreservedRecentTurns int    `json:"preserved_recent_turns"`
	TargetInputTokens    int    `json:"target_input_tokens"`
	MaxSummaryTokens     int    `json:"max_summary_tokens"`
}

type CompactionAuthorizationDecision struct {
	Allowed     bool     `json:"allowed"`
	ReasonCodes []string `json:"reason_codes,omitempty"`
}

func (policy CompactionPolicy) Snapshot(apiKeyAllowed, requestAuthorized bool) CompactionPolicySnapshot {
	return CompactionPolicySnapshot{
		SystemEnabled:        policy.SystemEnabled,
		APIKeyAllowed:        apiKeyAllowed,
		RequestAuthorized:    requestAuthorized,
		PolicyVersion:        policy.PolicyVersion,
		PreservedRecentTurns: policy.PreservedRecentTurns,
		TargetInputTokens:    policy.TargetInputTokens,
		MaxSummaryTokens:     policy.MaxSummaryTokens,
	}
}

func EvaluateCompactionAuthorization(snapshot CompactionPolicySnapshot) CompactionAuthorizationDecision {
	reasonCodes := make([]string, 0, 3)
	if !snapshot.SystemEnabled {
		reasonCodes = append(reasonCodes, "system_compaction_disabled")
	}
	if !snapshot.APIKeyAllowed {
		reasonCodes = append(reasonCodes, "api_key_compaction_denied")
	}
	if !snapshot.RequestAuthorized {
		reasonCodes = append(reasonCodes, "request_compaction_not_authorized")
	}
	return CompactionAuthorizationDecision{
		Allowed:     len(reasonCodes) == 0,
		ReasonCodes: reasonCodes,
	}
}
