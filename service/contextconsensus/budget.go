package contextconsensus

import (
	"context"
	"fmt"
	"math"
	"strings"
)

type ResolvedTokenBudgetRequest struct {
	RequestedMaxOutput     *uint
	DefaultMaxOutputTokens int
	SafetyMarginTokens     int
}

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

	countResult, err := counter.CountPromptTokens(ctx, countRequest)
	if err != nil {
		return TokenBudget{}, fmt.Errorf("count prompt tokens: %w", err)
	}
	if err := validateTokenCountResult(countRequest, countResult); err != nil {
		return TokenBudget{}, err
	}
	return evaluateTokenBudget(countResult, nil, budgetRequest)
}

func EvaluateResolvedTokenBudget(
	ctx context.Context,
	counter TokenCounter,
	resolver ContextLimitResolver,
	countRequest TokenCountRequest,
	budgetRequest ResolvedTokenBudgetRequest,
) (TokenBudget, error) {
	if counter == nil {
		return TokenBudget{}, fmt.Errorf("token counter is required")
	}
	if resolver == nil {
		return TokenBudget{}, fmt.Errorf("context limit resolver is required")
	}
	limitRequest := ContextLimitRequest{Model: countRequest.Model, ChannelID: countRequest.ChannelID, Protocol: countRequest.Protocol}
	limitResult, err := resolver.ResolveContextLimit(ctx, limitRequest)
	if err != nil {
		return TokenBudget{}, fmt.Errorf("resolve context limit: %w", err)
	}
	if err := validateContextLimitResult(limitRequest, limitResult); err != nil {
		return TokenBudget{}, err
	}
	countResult, err := counter.CountPromptTokens(ctx, countRequest)
	if err != nil {
		return TokenBudget{}, fmt.Errorf("count prompt tokens: %w", err)
	}
	if err := validateTokenCountResult(countRequest, countResult); err != nil {
		return TokenBudget{}, err
	}
	return evaluateTokenBudget(countResult, &limitResult, TokenBudgetRequest{
		ContextLimitTokens:     limitResult.ContextLimitTokens,
		RequestedMaxOutput:     budgetRequest.RequestedMaxOutput,
		DefaultMaxOutputTokens: budgetRequest.DefaultMaxOutputTokens,
		SafetyMarginTokens:     budgetRequest.SafetyMarginTokens,
	})
}

func validateTokenCountResult(request TokenCountRequest, result TokenCountResult) error {
	if strings.TrimSpace(request.Model) == "" {
		return fmt.Errorf("token count model is required")
	}
	if request.Protocol == "" {
		return fmt.Errorf("token count protocol is required")
	}
	if request.ChannelID <= 0 {
		return fmt.Errorf("token count channel ID must be positive")
	}
	if len(request.RequestBody) == 0 {
		return fmt.Errorf("token count request body is required")
	}
	if !result.Authoritative {
		return fmt.Errorf("token count result is not authoritative")
	}
	if result.Model != request.Model {
		return fmt.Errorf("token count result model does not match request")
	}
	if result.ChannelID != request.ChannelID {
		return fmt.Errorf("token count result channel ID does not match request")
	}
	if result.Protocol != request.Protocol {
		return fmt.Errorf("token count result protocol does not match request")
	}
	expectedDigest := digestBytes(request.RequestBody)
	if result.BodyDigest == "" || result.BodyDigest != expectedDigest {
		return fmt.Errorf("token count result body digest does not match request")
	}
	if request.Envelope != nil && request.Envelope.SourceDigest != expectedDigest {
		return fmt.Errorf("token count request envelope does not match request body")
	}
	if strings.TrimSpace(result.Source) == "" {
		return fmt.Errorf("token count result source is required")
	}
	if strings.TrimSpace(result.Version) == "" {
		return fmt.Errorf("token count result version is required")
	}
	return validateTokenBreakdown(result.Breakdown)
}

func validateContextLimitResult(request ContextLimitRequest, result ContextLimitResult) error {
	if strings.TrimSpace(request.Model) == "" {
		return fmt.Errorf("context limit model is required")
	}
	if request.Protocol == "" {
		return fmt.Errorf("context limit protocol is required")
	}
	if request.ChannelID <= 0 {
		return fmt.Errorf("context limit channel ID must be positive")
	}
	if !result.Authoritative {
		return fmt.Errorf("context limit result is not authoritative")
	}
	if result.Model != request.Model {
		return fmt.Errorf("context limit result model does not match request")
	}
	if result.ChannelID != request.ChannelID {
		return fmt.Errorf("context limit result channel ID does not match request")
	}
	if result.Protocol != request.Protocol {
		return fmt.Errorf("context limit result protocol does not match request")
	}
	if result.ContextLimitTokens <= 0 {
		return fmt.Errorf("context limit tokens must be greater than zero")
	}
	if strings.TrimSpace(result.Source) == "" {
		return fmt.Errorf("context limit result source is required")
	}
	if strings.TrimSpace(result.Version) == "" {
		return fmt.Errorf("context limit result version is required")
	}
	return nil
}

func validateTokenBreakdown(breakdown TokenBreakdown) error {
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
			return err
		}
	}
	return nil
}

func evaluateTokenBudget(countResult TokenCountResult, limitResult *ContextLimitResult, budgetRequest TokenBudgetRequest) (TokenBudget, error) {
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

	outputTokens := budgetRequest.DefaultMaxOutputTokens
	explicitZeroOutput := false
	if budgetRequest.RequestedMaxOutput != nil {
		if uint64(*budgetRequest.RequestedMaxOutput) > uint64(math.MaxInt) {
			return TokenBudget{}, fmt.Errorf("requested max output tokens exceed supported range")
		}
		outputTokens = int(*budgetRequest.RequestedMaxOutput)
		explicitZeroOutput = *budgetRequest.RequestedMaxOutput == 0
	}
	promptTokens, err := sumTokenCounts(countResult.Breakdown.TextTokens, countResult.Breakdown.ToolTokens, countResult.Breakdown.SchemaTokens, countResult.Breakdown.MediaTokens, countResult.Breakdown.ProtocolOverheadTokens)
	if err != nil {
		return TokenBudget{}, err
	}
	requiredTokens, err := sumTokenCounts(promptTokens, outputTokens, budgetRequest.SafetyMarginTokens)
	if err != nil {
		return TokenBudget{}, err
	}
	remainingTokens := budgetRequest.ContextLimitTokens - requiredTokens
	return TokenBudget{
		Breakdown:          countResult.Breakdown,
		TokenCount:         countResult,
		ContextLimit:       limitResult,
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

func sumTokenCounts(values ...int) (int, error) {
	total := 0
	for _, value := range values {
		if value < 0 {
			return 0, fmt.Errorf("token count must not be negative")
		}
		if value > math.MaxInt-total {
			return 0, fmt.Errorf("token count exceeds supported range")
		}
		total += value
	}
	return total, nil
}
