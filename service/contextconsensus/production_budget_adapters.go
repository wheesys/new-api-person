package contextconsensus

import (
	"context"
	"fmt"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/setting/model_setting"
	"github.com/QuantumNous/new-api/types"
	"github.com/tiktoken-go/tokenizer"
)

const (
	productionTokenCounterSource  = "tiktoken_openai_chat_protocol"
	productionTokenCounterVersion = "tiktoken-go/v0.6.2:openai-chat-pure-text-v1"
	configuredContextLimitSource  = "smart_routing.authoritative_context_limits"
)

// ProductionTokenCounter counts supported pure-text OpenAI Chat requests with
// the model's strict tokenizer and the protocol's message framing overhead.
// Unsupported protocol states fail closed instead of falling back to an
// estimator or counting serialized JSON syntax as prompt content.
type ProductionTokenCounter struct{}

func NewProductionTokenCounter() TokenCounter {
	return ProductionTokenCounter{}
}

func (ProductionTokenCounter) CountPromptTokens(ctx context.Context, request TokenCountRequest) (TokenCountResult, error) {
	if err := ctx.Err(); err != nil {
		return TokenCountResult{}, err
	}
	if strings.TrimSpace(request.Model) == "" {
		return TokenCountResult{}, fmt.Errorf("authoritative tokenizer model is required")
	}
	if request.ChannelID <= 0 {
		return TokenCountResult{}, fmt.Errorf("authoritative tokenizer channel ID must be positive")
	}
	if request.Protocol != types.RelayFormatOpenAI {
		return TokenCountResult{}, fmt.Errorf("authoritative tokenizer does not support protocol %q", request.Protocol)
	}
	if len(request.RequestBody) == 0 {
		return TokenCountResult{}, fmt.Errorf("authoritative tokenizer request body is required")
	}

	var payload dto.GeneralOpenAIRequest
	if err := common.Unmarshal(request.RequestBody, &payload); err != nil {
		return TokenCountResult{}, fmt.Errorf("decode authoritative tokenizer request body: %w", err)
	}
	if payload.Model != request.Model {
		return TokenCountResult{}, fmt.Errorf("authoritative tokenizer request body model does not match final model")
	}

	modelTokenizer, err := tokenizer.ForModel(tokenizer.Model(request.Model))
	if err != nil {
		return TokenCountResult{}, fmt.Errorf("authoritative tokenizer is unavailable for model %q: %w", request.Model, err)
	}
	meta := payload.GetTokenCountMeta()
	if meta == nil || len(meta.Files) > 0 || len(payload.Tools) > 0 || payload.ToolChoice != nil || len(payload.Functions) > 0 || len(payload.FunctionCall) > 0 || payload.ResponseFormat != nil || payload.Prompt != nil || payload.Input != nil {
		return TokenCountResult{}, fmt.Errorf("authoritative tokenizer only supports pure-text OpenAI Chat requests without tools, schema, media, prompt, or input fields")
	}
	for index, message := range payload.Messages {
		if len(message.ToolCalls) > 0 || message.ToolCallId != "" || message.Reasoning != nil || message.ReasoningContent != nil {
			return TokenCountResult{}, fmt.Errorf("authoritative tokenizer does not support stateful message %d", index)
		}
	}
	count, err := modelTokenizer.Count(meta.CombineText)
	if err != nil {
		return TokenCountResult{}, fmt.Errorf("count final prompt with authoritative tokenizer: %w", err)
	}
	protocolOverhead := meta.MessagesCount*3 + meta.NameCount + 3
	if err := ctx.Err(); err != nil {
		return TokenCountResult{}, err
	}

	return TokenCountResult{
		Breakdown: TokenBreakdown{
			TextTokens:             count,
			ProtocolOverheadTokens: protocolOverhead,
		},
		Authoritative: true,
		Model:         request.Model,
		ChannelID:     request.ChannelID,
		Protocol:      request.Protocol,
		BodyDigest:    digestBytes(request.RequestBody),
		Source:        productionTokenCounterSource,
		Version:       productionTokenCounterVersion + ":" + modelTokenizer.GetName(),
	}, nil
}

type FrozenContextLimitResolver struct {
	limits map[string]model_setting.AuthoritativeContextLimitConfig
}

// NewProductionContextLimitResolver freezes the current smart-routing settings
// snapshot so a request cannot observe a context-limit hot update midway.
func NewProductionContextLimitResolver() ContextLimitResolver {
	return NewFrozenContextLimitResolver(model_setting.GetSmartRoutingSettings())
}

func NewFrozenContextLimitResolver(settings *model_setting.SmartRoutingSettings) ContextLimitResolver {
	resolver := &FrozenContextLimitResolver{
		limits: map[string]model_setting.AuthoritativeContextLimitConfig{},
	}
	if settings == nil {
		return resolver
	}
	for model, limit := range settings.AuthoritativeContextLimits {
		limit.ChannelIDs = append([]int(nil), limit.ChannelIDs...)
		limit.RelayFormats = append([]string(nil), limit.RelayFormats...)
		resolver.limits[model] = limit
	}
	return resolver
}

func (resolver *FrozenContextLimitResolver) ResolveContextLimit(ctx context.Context, request ContextLimitRequest) (ContextLimitResult, error) {
	if err := ctx.Err(); err != nil {
		return ContextLimitResult{}, err
	}
	if resolver == nil {
		return ContextLimitResult{}, fmt.Errorf("frozen context limit resolver is required")
	}
	if strings.TrimSpace(request.Model) == "" {
		return ContextLimitResult{}, fmt.Errorf("authoritative context limit model is required")
	}
	if request.ChannelID <= 0 {
		return ContextLimitResult{}, fmt.Errorf("authoritative context limit channel ID must be positive")
	}
	if request.Protocol == "" {
		return ContextLimitResult{}, fmt.Errorf("authoritative context limit protocol is required")
	}

	limit, found := resolver.limits[request.Model]
	if !found {
		return ContextLimitResult{}, fmt.Errorf("authoritative context limit is unavailable for model %q", request.Model)
	}
	if limit.MaxContextTokens <= 0 {
		return ContextLimitResult{}, fmt.Errorf("authoritative context limit for model %q is invalid", request.Model)
	}
	if strings.TrimSpace(limit.Version) == "" {
		return ContextLimitResult{}, fmt.Errorf("authoritative context limit version for model %q is required", request.Model)
	}
	channelAllowed := false
	for _, channelID := range limit.ChannelIDs {
		if channelID == request.ChannelID {
			channelAllowed = true
			break
		}
	}
	if !channelAllowed {
		return ContextLimitResult{}, fmt.Errorf("authoritative context limit for model %q does not allow channel %d", request.Model, request.ChannelID)
	}
	protocolAllowed := false
	for _, relayFormat := range limit.RelayFormats {
		if relayFormat == string(request.Protocol) {
			protocolAllowed = true
			break
		}
	}
	if !protocolAllowed {
		return ContextLimitResult{}, fmt.Errorf("authoritative context limit for model %q does not allow protocol %q", request.Model, request.Protocol)
	}

	return ContextLimitResult{
		ContextLimitTokens: limit.MaxContextTokens,
		Authoritative:      true,
		Model:              request.Model,
		ChannelID:          request.ChannelID,
		Protocol:           request.Protocol,
		Source:             configuredContextLimitSource,
		Version:            limit.Version,
	}, nil
}
