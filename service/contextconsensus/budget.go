package contextconsensus

import (
	"context"
	"fmt"
)

func EvaluateTokenBudget(
	ctx context.Context,
	counter TokenCounter,
	countRequest TokenCountRequest,
	budgetRequest TokenBudgetRequest,
) (TokenBudget, error) {
	if counter == nil {
		return TokenBudget{}, fmt.Errorf("token counter is required")
	}
	if err := validateNonNegative("context limit tokens", budgetRequest.ContextLimitTokens); err != nil {
		return TokenBudget{}, err
	}
	if budgetRequest.ContextLimitTokens == 0 {
		return TokenBudget{}, fmt.Errorf("context limit tokens must be greater than zero")
	}
	if err := validateNonNegative("default max output tokens", budgetRequest.DefaultMaxOutputTokens); err != nil {
		return TokenBudget{}, err
	}
	if err := validateNonNegative("safety margin tokens", budgetRequest.SafetyMarginTokens); err != nil {
		return TokenBudget{}, err
	}

	breakdown, err := counter.CountPromptTokens(ctx, countRequest)
	if err != nil {
		return TokenBudget{}, fmt.Errorf("count prompt tokens: %w", err)
	}
	components := []struct {
		name  string
		value int
	}{
		{name: "text tokens", value: breakdown.TextTokens},
		{name: "tool tokens", value: breakdown.ToolTokens},
		{name: "schema tokens", value: breakdown.SchemaTokens},
		{name: "media tokens", value: breakdown.MediaTokens},
		{name: "protocol overhead tokens", value: breakdown.ProtocolOverheadTokens},
	}
	for _, component := range components {
		if err := validateNonNegative(component.name, component.value); err != nil {
			return TokenBudget{}, err
		}
	}

	outputTokens := budgetRequest.DefaultMaxOutputTokens
	explicitZeroOutput := false
	if budgetRequest.RequestedMaxOutput != nil {
		outputTokens = int(*budgetRequest.RequestedMaxOutput)
		explicitZeroOutput = *budgetRequest.RequestedMaxOutput == 0
	}
	promptTokens := breakdown.PromptTokens()
	requiredTokens := promptTokens + outputTokens + budgetRequest.SafetyMarginTokens
	remainingTokens := budgetRequest.ContextLimitTokens - requiredTokens
	return TokenBudget{
		Breakdown:          breakdown,
		PromptTokens:       promptTokens,
		OutputTokens:       outputTokens,
		SafetyMarginTokens: budgetRequest.SafetyMarginTokens,
		RequiredTokens:     requiredTokens,
		ContextLimitTokens: budgetRequest.ContextLimitTokens,
		RemainingTokens:    remainingTokens,
		Fits:               remainingTokens >= 0,
		ExplicitZeroOutput: explicitZeroOutput,
	}, nil
}
