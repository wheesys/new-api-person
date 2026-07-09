package smartrouting

import (
	"math"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
)

type ChannelSnapshot struct {
	ID            int
	Group         string
	Models        string
	Status        int
	ResponseTime  int
	Weight        int
	PriceRatio    *float64
	EndpointTypes []constant.EndpointType
}

func NewChannelSnapshot(channel *model.Channel) ChannelSnapshot {
	if channel == nil {
		return ChannelSnapshot{}
	}
	models := channel.Models
	return ChannelSnapshot{
		ID:            channel.Id,
		Group:         channel.Group,
		Models:        models,
		Status:        channel.Status,
		ResponseTime:  channel.ResponseTime,
		Weight:        channel.GetWeight(),
		PriceRatio:    channel.PriceRatio,
		EndpointTypes: common.GetEndpointTypesByChannelType(channel.Type, firstCommaListValue(models)),
	}
}

type ModelCapabilities struct {
	QualityTier        ModelQualityTier
	MaxContextTokens   int
	SupportsTools      bool
	SupportsJSONSchema bool
	SupportsVision     bool
	SupportsAudio      bool
	SupportsFiles      bool
	SupportsReasoning  bool
	SupportsStream     bool
}

func BuildCandidatesFromSnapshots(request SmartRouteRequest, pricing []model.Pricing, channels []ChannelSnapshot) []SmartRouteCandidate {
	if len(pricing) == 0 || len(channels) == 0 {
		return nil
	}

	candidates := make([]SmartRouteCandidate, 0)
	for _, item := range pricing {
		if !pricingMatchesRequest(item, request) {
			continue
		}
		capabilities := InferModelCapabilities(item.ModelName, item.SupportedEndpointTypes)
		profile, hasProfile := GetModelRoutingProfile(item.ModelName)
		if hasProfile {
			capabilities = mergeModelCapabilitiesWithProfile(capabilities, profile)
		}
		for _, channel := range channels {
			if !channelMatchesRequest(channel, item.ModelName, request) {
				continue
			}
			estimatedQuota := estimateQuota(request, item, channel)
			latencyScore := latencyScoreFromResponseTime(channel.ResponseTime)
			candidates = append(candidates, SmartRouteCandidate{
				ModelName:          item.ModelName,
				ChannelID:          channel.ID,
				Group:              request.UsingGroup,
				QualityTier:        capabilities.QualityTier,
				MaxContextTokens:   capabilities.MaxContextTokens,
				SupportsTools:      capabilities.SupportsTools,
				SupportsJSONSchema: capabilities.SupportsJSONSchema,
				SupportsVision:     capabilities.SupportsVision,
				SupportsAudio:      capabilities.SupportsAudio,
				SupportsFiles:      capabilities.SupportsFiles,
				SupportsReasoning:  capabilities.SupportsReasoning,
				SupportsStream:     capabilities.SupportsStream,
				EstimatedQuota:     estimatedQuota,
				Reliability:        1,
				LatencyScore:       latencyScore,
				ThroughputScore:    channelWeightScore(channel.Weight),
				QualityScore:       candidateQualityScore(capabilities.QualityTier, profile, hasProfile),
				CodingScore:        profile.CodingScore,
				ReasoningScore:     profile.ReasoningScore,
				SpeedScore:         profile.SpeedScore,
				ContextScore:       profile.ContextScore,
				PreferenceScore:    profile.PreferenceScore,
				AffinityScore:      0,
			})
		}
	}
	return candidates
}

func mergeModelCapabilitiesWithProfile(capabilities ModelCapabilities, profile ModelRoutingProfile) ModelCapabilities {
	if profile.MaxContextTokens > 0 {
		capabilities.MaxContextTokens = profile.MaxContextTokens
	}
	capabilities.SupportsTools = capabilities.SupportsTools || profile.SupportsTools
	capabilities.SupportsJSONSchema = capabilities.SupportsJSONSchema || profile.SupportsJSONSchema
	capabilities.SupportsVision = capabilities.SupportsVision || profile.SupportsVision
	capabilities.SupportsAudio = capabilities.SupportsAudio || profile.SupportsAudio
	capabilities.SupportsReasoning = capabilities.SupportsReasoning || profile.SupportsReasoning
	capabilities.SupportsStream = capabilities.SupportsStream || profile.SupportsStream
	return capabilities
}

func candidateQualityScore(tier ModelQualityTier, profile ModelRoutingProfile, hasProfile bool) float64 {
	if hasProfile && profile.QualityScore > 0 {
		return normalizeScore(profile.QualityScore)
	}
	return qualityScore(tier)
}

func InferModelCapabilities(modelName string, endpoints []constant.EndpointType) ModelCapabilities {
	normalized := strings.ToLower(strings.TrimSpace(modelName))
	capabilities := ModelCapabilities{
		QualityTier:        inferQualityTier(normalized),
		MaxContextTokens:   inferContextTokens(normalized),
		SupportsTools:      true,
		SupportsJSONSchema: true,
		SupportsStream:     true,
	}

	if endpointTypesContain(endpoints, constant.EndpointTypeImageGeneration) || common.IsImageGenerationModel(normalized) {
		capabilities.QualityTier = QualityPremium
		capabilities.SupportsTools = false
		capabilities.SupportsJSONSchema = false
		capabilities.SupportsVision = true
	}
	if endpointTypesContain(endpoints, constant.EndpointTypeEmbeddings) ||
		endpointTypesContain(endpoints, constant.EndpointTypeJinaRerank) {
		capabilities.QualityTier = QualityStandard
		capabilities.SupportsTools = false
		capabilities.SupportsJSONSchema = false
		capabilities.SupportsStream = false
	}
	if endpointTypesContain(endpoints, constant.EndpointTypeAnthropic) ||
		endpointTypesContain(endpoints, constant.EndpointTypeGemini) {
		capabilities.SupportsTools = true
		capabilities.SupportsJSONSchema = true
		capabilities.SupportsStream = true
	}
	if strings.Contains(normalized, "vision") || strings.Contains(normalized, "4o") || strings.Contains(normalized, "gpt-5") ||
		strings.Contains(normalized, "claude") || strings.Contains(normalized, "gemini") {
		capabilities.SupportsVision = true
	}
	if strings.Contains(normalized, "audio") || strings.Contains(normalized, "realtime") || strings.Contains(normalized, "tts") || strings.Contains(normalized, "whisper") {
		capabilities.SupportsAudio = true
	}
	if strings.Contains(normalized, "reasoning") || strings.Contains(normalized, "thinking") || strings.HasPrefix(normalized, "o") {
		capabilities.SupportsReasoning = true
	}
	return capabilities
}

func pricingMatchesRequest(item model.Pricing, request SmartRouteRequest) bool {
	if !stringSliceContains(item.EnableGroup, request.UsingGroup) {
		return false
	}
	return endpointMatchesRequest(item.SupportedEndpointTypes, request.EndpointType)
}

func channelMatchesRequest(channel ChannelSnapshot, modelName string, request SmartRouteRequest) bool {
	if channel.Status != common.ChannelStatusEnabled {
		return false
	}
	if !commaListContains(channel.Group, request.UsingGroup) {
		return false
	}
	if !commaListContains(channel.Models, modelName) {
		return false
	}
	if len(channel.EndpointTypes) == 0 {
		return true
	}
	return endpointMatchesRequest(channel.EndpointTypes, request.EndpointType)
}

func endpointMatchesRequest(endpoints []constant.EndpointType, endpoint EndpointType) bool {
	expected := endpointTypeForRequest(endpoint)
	if expected == "" {
		return true
	}
	return endpointTypesContain(endpoints, expected)
}

func endpointTypeForRequest(endpoint EndpointType) constant.EndpointType {
	switch endpoint {
	case EndpointChatCompletions:
		return constant.EndpointTypeOpenAI
	case EndpointResponses:
		return constant.EndpointTypeOpenAIResponse
	case EndpointClaudeMessages:
		return constant.EndpointTypeAnthropic
	case EndpointGemini:
		return constant.EndpointTypeGemini
	case EndpointEmbedding:
		return constant.EndpointTypeEmbeddings
	case EndpointRerank:
		return constant.EndpointTypeJinaRerank
	case EndpointImage:
		return constant.EndpointTypeImageGeneration
	default:
		return ""
	}
}

func estimateQuota(request SmartRouteRequest, item model.Pricing, channel ChannelSnapshot) int {
	promptTokens := request.EstimatedPromptTokens
	outputTokens := request.MaxOutputTokens
	if request.TokenMeta != nil {
		if promptTokens == 0 && request.TokenMeta.MessagesCount > 0 {
			promptTokens = request.TokenMeta.MessagesCount * 128
		}
		if outputTokens == 0 {
			outputTokens = request.TokenMeta.MaxTokens
		}
	}

	var estimated float64
	if item.QuotaType == 1 || item.ModelPrice > 0 {
		estimated = item.ModelPrice * float64(common.QuotaPerUnit)
	} else {
		completionRatio := item.CompletionRatio
		if completionRatio <= 0 {
			completionRatio = 1
		}
		estimated = (float64(promptTokens) + float64(outputTokens)*completionRatio) * item.ModelRatio
	}
	if channel.PriceRatio != nil {
		estimated *= channelRatioValue(channel.PriceRatio)
	}
	if estimated < 0 {
		return 0
	}
	return int(math.Round(estimated))
}

func channelRatioValue(ratio *float64) float64 {
	if ratio == nil || *ratio < 0 {
		return 1
	}
	return *ratio
}

func latencyScoreFromResponseTime(responseTime int) float64 {
	if responseTime <= 0 {
		return 0.5
	}
	score := 1 - float64(responseTime)/1000
	return normalizeScore(score)
}

func channelWeightScore(weight int) float64 {
	if weight <= 0 {
		return 0
	}
	return normalizeScore(float64(weight) / 100)
}

func qualityScore(tier ModelQualityTier) float64 {
	switch tier {
	case QualityReasoning:
		return 1
	case QualityPremium:
		return 0.88
	case QualityStandard:
		return 0.65
	case QualityEconomy:
		return 0.42
	default:
		return 0.5
	}
}

func inferQualityTier(modelName string) ModelQualityTier {
	switch {
	case strings.Contains(modelName, "thinking"), strings.Contains(modelName, "reasoning"), strings.HasPrefix(modelName, "o1"), strings.HasPrefix(modelName, "o3"), strings.HasPrefix(modelName, "o4"):
		return QualityReasoning
	case strings.Contains(modelName, "gpt-5"), strings.Contains(modelName, "opus"), strings.Contains(modelName, "sonnet"), strings.Contains(modelName, "pro"):
		return QualityPremium
	case strings.Contains(modelName, "mini"), strings.Contains(modelName, "flash"), strings.Contains(modelName, "haiku"), strings.Contains(modelName, "lite"):
		return QualityEconomy
	default:
		return QualityStandard
	}
}

func inferContextTokens(modelName string) int {
	switch {
	case strings.Contains(modelName, "1m"):
		return 1000000
	case strings.Contains(modelName, "256k"):
		return 256000
	case strings.Contains(modelName, "200k"), strings.Contains(modelName, "claude"):
		return 200000
	case strings.Contains(modelName, "128k"), strings.Contains(modelName, "gpt-5"), strings.Contains(modelName, "gemini"):
		return 128000
	default:
		return 32000
	}
}

func endpointTypesContain(values []constant.EndpointType, target constant.EndpointType) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

func stringSliceContains(values []string, target string) bool {
	for _, value := range values {
		if strings.TrimSpace(value) == target {
			return true
		}
	}
	return false
}

func commaListContains(values string, target string) bool {
	for _, value := range strings.Split(values, ",") {
		if strings.TrimSpace(value) == target {
			return true
		}
	}
	return false
}

func firstCommaListValue(values string) string {
	for _, value := range strings.Split(values, ",") {
		value = strings.TrimSpace(value)
		if value != "" {
			return value
		}
	}
	return ""
}
