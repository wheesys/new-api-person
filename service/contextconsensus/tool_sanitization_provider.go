package contextconsensus

import "fmt"

type ToolSanitizationPolicyProvider interface {
	Sanitize(evidence ToolCompactionStructuralEvidence, result any) (ToolResultSanitizationOutput, error)
}

type staticToolSanitizationPolicySelection struct {
	sanitizerVersion int
	policyVersion    string
}

type StaticToolSanitizationPolicyProvider struct {
	registry   *ToolResultSanitizationRegistry
	selections map[string]staticToolSanitizationPolicySelection
}

func NewStaticToolSanitizationPolicyProvider(policies []ToolResultSanitizationPolicy) (*StaticToolSanitizationPolicyProvider, error) {
	registry, err := NewToolResultSanitizationRegistry(policies)
	if err != nil {
		return nil, err
	}
	provider := &StaticToolSanitizationPolicyProvider{
		registry: registry, selections: make(map[string]staticToolSanitizationPolicySelection, len(policies)),
	}
	for _, policy := range policies {
		key := toolResultSanitizationProviderKey(policy.ToolIdentityDigest, policy.SchemaDigest)
		if _, exists := provider.selections[key]; exists {
			return nil, fmt.Errorf("%w: multiple active policies match one tool schema", ErrToolSanitizationPolicyInvalid)
		}
		provider.selections[key] = staticToolSanitizationPolicySelection{
			sanitizerVersion: policy.SanitizerVersion,
			policyVersion:    policy.Version,
		}
	}
	return provider, nil
}

func (provider *StaticToolSanitizationPolicyProvider) Sanitize(evidence ToolCompactionStructuralEvidence, result any) (ToolResultSanitizationOutput, error) {
	if provider == nil || provider.registry == nil {
		return ToolResultSanitizationOutput{}, ErrToolSanitizationPolicyNotFound
	}
	selection, found := provider.selections[toolResultSanitizationProviderKey(evidence.ToolIdentityDigest, evidence.SchemaDigest)]
	if !found {
		return ToolResultSanitizationOutput{}, ErrToolSanitizationPolicyNotFound
	}
	return provider.registry.Sanitize(ToolResultSanitizationRequest{
		SanitizerVersion: selection.sanitizerVersion,
		PolicyVersion:    selection.policyVersion,
		Evidence:         evidence,
		Result:           result,
	})
}

func toolResultSanitizationProviderKey(toolIdentityDigest, schemaDigest string) string {
	return toolIdentityDigest + "\x00" + schemaDigest
}
