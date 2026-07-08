package smartrouting

import (
	"math"
	"sort"
	"strings"

	"github.com/QuantumNous/new-api/relaykit/types"
)

type EndpointType string

const (
	EndpointChatCompletions EndpointType = "chat_completions"
	EndpointResponses       EndpointType = "responses"
	EndpointClaudeMessages  EndpointType = "claude_messages"
	EndpointGemini          EndpointType = "gemini"
	EndpointEmbedding       EndpointType = "embedding"
	EndpointRerank          EndpointType = "rerank"
	EndpointImage           EndpointType = "image"
	EndpointAudio           EndpointType = "audio"
)

type TaskComplexity string

const (
	TaskSimple   TaskComplexity = "simple"
	TaskStandard TaskComplexity = "standard"
	TaskComplex  TaskComplexity = "complex"
	TaskCritical TaskComplexity = "critical"
)

type ContextRequirement string

const (
	ContextShort  ContextRequirement = "short"
	ContextMedium ContextRequirement = "medium"
	ContextLong   ContextRequirement = "long"
	ContextHuge   ContextRequirement = "huge"
)

type RoutePolicy string

const (
	PolicyCostFirst        RoutePolicy = "cost_first"
	PolicyBalanced         RoutePolicy = "balanced"
	PolicyQualityFirst     RoutePolicy = "quality_first"
	PolicyLatencyFirst     RoutePolicy = "latency_first"
	PolicyReliabilityFirst RoutePolicy = "reliability_first"
)

type ModelQualityTier string

const (
	QualityEconomy   ModelQualityTier = "economy"
	QualityStandard  ModelQualityTier = "standard"
	QualityPremium   ModelQualityTier = "premium"
	QualityReasoning ModelQualityTier = "reasoning"
)

type SmartRouteRequest struct {
	OriginalModel         string
	EndpointType          EndpointType
	UsingGroup            string
	TokenID               int
	UserID                int
	Stream                bool
	EstimatedPromptTokens int
	MaxOutputTokens       int
	ContextTokensRequired int
	HasTools              bool
	ToolCount             int
	RequiresJSONSchema    bool
	HasImages             bool
	HasAudio              bool
	HasFiles              bool
	ReasoningRequested    bool
	RequiresReliability   bool
	TokenMeta             *types.TokenCountMeta
}

type VirtualModelProfile struct {
	Name           string
	Policy         RoutePolicy
	PreferredTiers []ModelQualityTier
}

type SmartRouteAnalysis struct {
	TaskComplexity     TaskComplexity
	ContextRequirement ContextRequirement
	TaskScore          int
	ContextScore       int
	TaskReasons        []string
	ContextReasons     []string
}

type SmartRouteCandidate struct {
	ModelName          string
	ChannelID          int
	Group              string
	QualityTier        ModelQualityTier
	MaxContextTokens   int
	SupportsTools      bool
	SupportsJSONSchema bool
	SupportsVision     bool
	SupportsAudio      bool
	SupportsFiles      bool
	SupportsReasoning  bool
	SupportsStream     bool
	EstimatedQuota     int
	Reliability        float64
	LatencyScore       float64
	ThroughputScore    float64
	QualityScore       float64
	AffinityScore      float64
	FinalScore         float64
	ScoreFactors       ScoreFactors
}

type ScoreFactors struct {
	Cost        float64
	Reliability float64
	Latency     float64
	Throughput  float64
	Quality     float64
	Affinity    float64
}

type CandidateRejection struct {
	Candidate SmartRouteCandidate
	Reasons   []string
}

type Decision struct {
	Enabled            bool
	Policy             RoutePolicy
	TaskComplexity     TaskComplexity
	ContextRequirement ContextRequirement
	OriginalModel      string
	SelectedModel      string
	SelectedChannelID  int
	CandidateCount     int
	FallbackIndex      int
	ScoreFactors       ScoreFactors
	DecisionReasons    []string
	ContextConsensus   ContextConsensusLog
}

type ContextConsensusLog struct {
	Mode                    string
	Compacted               bool
	PreservedRecentMessages int
}

type policyWeights struct {
	cost        float64
	reliability float64
	latency     float64
	throughput  float64
	quality     float64
	affinity    float64
}

var virtualModelProfiles = map[string]VirtualModelProfile{
	"auto:cheap": {
		Name:           "auto:cheap",
		Policy:         PolicyCostFirst,
		PreferredTiers: []ModelQualityTier{QualityEconomy, QualityStandard},
	},
	"auto:balanced": {
		Name:           "auto:balanced",
		Policy:         PolicyBalanced,
		PreferredTiers: []ModelQualityTier{QualityStandard, QualityEconomy, QualityPremium},
	},
	"auto:quality": {
		Name:           "auto:quality",
		Policy:         PolicyQualityFirst,
		PreferredTiers: []ModelQualityTier{QualityPremium, QualityReasoning, QualityStandard},
	},
	"auto:fast": {
		Name:           "auto:fast",
		Policy:         PolicyLatencyFirst,
		PreferredTiers: []ModelQualityTier{QualityEconomy, QualityStandard},
	},
	"auto:reasoning": {
		Name:           "auto:reasoning",
		Policy:         PolicyQualityFirst,
		PreferredTiers: []ModelQualityTier{QualityReasoning, QualityPremium},
	},
}

var routePolicyWeights = map[RoutePolicy]policyWeights{
	PolicyCostFirst: {
		cost:        0.55,
		reliability: 0.18,
		latency:     0.12,
		throughput:  0.05,
		quality:     0.08,
		affinity:    0.02,
	},
	PolicyBalanced: {
		cost:        0.25,
		reliability: 0.25,
		latency:     0.20,
		throughput:  0.08,
		quality:     0.17,
		affinity:    0.05,
	},
	PolicyQualityFirst: {
		cost:        0.10,
		reliability: 0.25,
		latency:     0.08,
		throughput:  0.07,
		quality:     0.45,
		affinity:    0.05,
	},
	PolicyLatencyFirst: {
		cost:        0.18,
		reliability: 0.20,
		latency:     0.45,
		throughput:  0.08,
		quality:     0.06,
		affinity:    0.03,
	},
	PolicyReliabilityFirst: {
		cost:        0.08,
		reliability: 0.50,
		latency:     0.12,
		throughput:  0.08,
		quality:     0.17,
		affinity:    0.05,
	},
}

func ResolveVirtualModel(modelName string) (VirtualModelProfile, bool) {
	normalized := strings.ToLower(strings.TrimSpace(modelName))
	if strings.HasPrefix(normalized, "smart:") {
		normalized = "auto:" + strings.TrimPrefix(normalized, "smart:")
	}
	profile, ok := virtualModelProfiles[normalized]
	return profile, ok
}

func AnalyzeRequest(request SmartRouteRequest) SmartRouteAnalysis {
	taskScore, taskReasons := scoreTaskComplexity(request)
	contextScore, contextReasons := scoreContextRequirement(request)
	return SmartRouteAnalysis{
		TaskComplexity:     classifyTaskComplexity(taskScore, request.RequiresReliability),
		ContextRequirement: classifyContextRequirement(contextScore),
		TaskScore:          taskScore,
		ContextScore:       contextScore,
		TaskReasons:        taskReasons,
		ContextReasons:     contextReasons,
	}
}

func RankCandidates(request SmartRouteRequest, candidates []SmartRouteCandidate, policy RoutePolicy) ([]SmartRouteCandidate, []CandidateRejection) {
	if len(candidates) == 0 {
		return nil, nil
	}

	accepted := make([]SmartRouteCandidate, 0, len(candidates))
	rejections := make([]CandidateRejection, 0)
	maxQuota := maxEstimatedQuota(candidates)
	for _, candidate := range candidates {
		reasons := rejectCandidate(request, candidate)
		if len(reasons) > 0 {
			rejections = append(rejections, CandidateRejection{
				Candidate: candidate,
				Reasons:   reasons,
			})
			continue
		}
		scored := candidate
		scored.ScoreFactors = buildScoreFactors(candidate, maxQuota)
		scored.FinalScore = weightedScore(scored.ScoreFactors, policy)
		accepted = append(accepted, scored)
	}

	sort.SliceStable(accepted, func(left int, right int) bool {
		if accepted[left].FinalScore == accepted[right].FinalScore {
			if accepted[left].EstimatedQuota == accepted[right].EstimatedQuota {
				return accepted[left].ChannelID < accepted[right].ChannelID
			}
			return accepted[left].EstimatedQuota < accepted[right].EstimatedQuota
		}
		return accepted[left].FinalScore > accepted[right].FinalScore
	})

	return accepted, rejections
}

func (decision Decision) LogFields() map[string]interface{} {
	fields := map[string]interface{}{
		"enabled":             decision.Enabled,
		"policy":              string(decision.Policy),
		"complexity":          string(decision.TaskComplexity),
		"context_requirement": string(decision.ContextRequirement),
		"original_model":      decision.OriginalModel,
		"selected_model":      decision.SelectedModel,
		"selected_channel_id": decision.SelectedChannelID,
		"candidate_count":     decision.CandidateCount,
		"fallback_index":      decision.FallbackIndex,
		"score_factors": map[string]interface{}{
			"cost":        decision.ScoreFactors.Cost,
			"reliability": decision.ScoreFactors.Reliability,
			"latency":     decision.ScoreFactors.Latency,
			"throughput":  decision.ScoreFactors.Throughput,
			"quality":     decision.ScoreFactors.Quality,
			"affinity":    decision.ScoreFactors.Affinity,
		},
	}
	if len(decision.DecisionReasons) > 0 {
		fields["decision_reasons"] = decision.DecisionReasons
	}
	if decision.ContextConsensus.Mode != "" {
		fields["context_consensus"] = map[string]interface{}{
			"mode":                      decision.ContextConsensus.Mode,
			"compacted":                 decision.ContextConsensus.Compacted,
			"preserved_recent_messages": decision.ContextConsensus.PreservedRecentMessages,
		}
	}
	return fields
}

func scoreTaskComplexity(request SmartRouteRequest) (int, []string) {
	score := 0
	reasons := make([]string, 0)
	text := normalizedCombinedText(request.TokenMeta)

	if isSimpleRewriteOrTranslation(text) {
		score -= 2
		reasons = append(reasons, "simple_rewrite_or_translation")
	}
	if request.HasTools || request.ToolCount > 0 {
		score += 3 + request.ToolCount
		reasons = append(reasons, "tools_required")
	}
	if request.RequiresJSONSchema {
		score += 3
		reasons = append(reasons, "json_schema_required")
	}
	if request.HasImages || hasFileType(request.TokenMeta, types.FileTypeImage) {
		score += 3
		reasons = append(reasons, "vision_required")
	}
	if request.HasAudio || hasFileType(request.TokenMeta, types.FileTypeAudio) {
		score += 2
		reasons = append(reasons, "audio_required")
	}
	if request.HasFiles || hasFileType(request.TokenMeta, types.FileTypeFile) {
		score += 2
		reasons = append(reasons, "files_required")
	}
	if request.ReasoningRequested || strings.Contains(text, "reasoning") || strings.Contains(text, "推理") {
		score += 3
		reasons = append(reasons, "reasoning_requested")
	}
	if containsAny(text, []string{"代码", "debug", "调试", "迁移", "架构", "方案", "多步骤", "multi-step", "schema"}) {
		score += 2
		reasons = append(reasons, "complex_task_terms")
	}
	if request.MaxOutputTokens >= 4096 || (request.TokenMeta != nil && request.TokenMeta.MaxTokens >= 4096) {
		score += 1
		reasons = append(reasons, "large_output_budget")
	}
	if request.RequiresReliability {
		score += 4
		reasons = append(reasons, "high_reliability_required")
	}

	if score < 0 {
		score = 0
	}
	return score, reasons
}

func scoreContextRequirement(request SmartRouteRequest) (int, []string) {
	score := 0
	reasons := make([]string, 0)
	messageCount := 0
	if request.TokenMeta != nil {
		messageCount = request.TokenMeta.MessagesCount
	}
	contextTokens := request.ContextTokensRequired
	if contextTokens == 0 {
		contextTokens = request.EstimatedPromptTokens + request.MaxOutputTokens
	}
	if request.TokenMeta != nil && request.MaxOutputTokens == 0 {
		contextTokens += request.TokenMeta.MaxTokens
	}

	switch {
	case contextTokens >= 100000:
		score += 9
		reasons = append(reasons, "huge_prompt_tokens")
	case contextTokens >= 32000:
		score += 6
		reasons = append(reasons, "large_prompt_tokens")
	case contextTokens >= 8000:
		score += 3
		reasons = append(reasons, "medium_prompt_tokens")
	}

	switch {
	case messageCount >= 80:
		score += 4
		reasons = append(reasons, "many_messages")
	case messageCount >= 24:
		score += 3
		reasons = append(reasons, "long_conversation")
	case messageCount >= 8:
		score += 1
		reasons = append(reasons, "multi_turn_context")
	}

	text := normalizedCombinedText(request.TokenMeta)
	if containsAny(text, []string{"全部历史", "整段对话", "所有内容", "完整方案", "entire conversation", "all previous"}) {
		score += 4
		reasons = append(reasons, "explicit_full_context_reference")
	}
	if containsAny(text, []string{"上面那句话", "上一条", "recent", "previous message"}) {
		score += 1
		reasons = append(reasons, "recent_context_reference")
	}
	if request.HasFiles || hasFileType(request.TokenMeta, types.FileTypeFile) {
		score += 2
		reasons = append(reasons, "file_context")
	}
	if request.TokenMeta != nil && request.TokenMeta.MaxTokens >= 4096 {
		score += 2
		reasons = append(reasons, "large_output_context")
	}

	return score, reasons
}

func classifyTaskComplexity(score int, requiresReliability bool) TaskComplexity {
	if requiresReliability || score >= 9 {
		return TaskCritical
	}
	if score >= 6 {
		return TaskComplex
	}
	if score >= 2 {
		return TaskStandard
	}
	return TaskSimple
}

func classifyContextRequirement(score int) ContextRequirement {
	switch {
	case score >= 11:
		return ContextHuge
	case score >= 6:
		return ContextLong
	case score >= 2:
		return ContextMedium
	default:
		return ContextShort
	}
}

func rejectCandidate(request SmartRouteRequest, candidate SmartRouteCandidate) []string {
	reasons := make([]string, 0)
	if request.ContextTokensRequired > 0 && candidate.MaxContextTokens > 0 && request.ContextTokensRequired > candidate.MaxContextTokens {
		reasons = append(reasons, "context_too_small")
	}
	if request.Stream && !candidate.SupportsStream {
		reasons = append(reasons, "stream_not_supported")
	}
	if (request.HasTools || request.ToolCount > 0) && !candidate.SupportsTools {
		reasons = append(reasons, "tools_not_supported")
	}
	if request.RequiresJSONSchema && !candidate.SupportsJSONSchema {
		reasons = append(reasons, "json_schema_not_supported")
	}
	if request.HasImages && !candidate.SupportsVision {
		reasons = append(reasons, "vision_not_supported")
	}
	if request.HasAudio && !candidate.SupportsAudio {
		reasons = append(reasons, "audio_not_supported")
	}
	if request.HasFiles && !candidate.SupportsFiles {
		reasons = append(reasons, "files_not_supported")
	}
	if request.ReasoningRequested && !candidate.SupportsReasoning {
		reasons = append(reasons, "reasoning_not_supported")
	}
	return reasons
}

func maxEstimatedQuota(candidates []SmartRouteCandidate) int {
	maxQuota := 0
	for _, candidate := range candidates {
		if candidate.EstimatedQuota > maxQuota {
			maxQuota = candidate.EstimatedQuota
		}
	}
	return maxQuota
}

func buildScoreFactors(candidate SmartRouteCandidate, maxQuota int) ScoreFactors {
	return ScoreFactors{
		Cost:        costScore(candidate.EstimatedQuota, maxQuota),
		Reliability: normalizeScore(candidate.Reliability),
		Latency:     normalizeScore(candidate.LatencyScore),
		Throughput:  normalizeScore(candidate.ThroughputScore),
		Quality:     normalizeScore(candidate.QualityScore),
		Affinity:    normalizeScore(candidate.AffinityScore),
	}
}

func weightedScore(factors ScoreFactors, policy RoutePolicy) float64 {
	weights, ok := routePolicyWeights[policy]
	if !ok {
		weights = routePolicyWeights[PolicyBalanced]
	}
	return factors.Cost*weights.cost +
		factors.Reliability*weights.reliability +
		factors.Latency*weights.latency +
		factors.Throughput*weights.throughput +
		factors.Quality*weights.quality +
		factors.Affinity*weights.affinity
}

func costScore(estimatedQuota int, maxQuota int) float64 {
	if estimatedQuota <= 0 {
		return 1
	}
	if maxQuota <= 0 {
		return 1
	}
	score := 1 - (float64(estimatedQuota) / float64(maxQuota))
	return normalizeScore(score)
}

func normalizeScore(value float64) float64 {
	if math.IsNaN(value) || math.IsInf(value, 0) {
		return 0
	}
	if value < 0 {
		return 0
	}
	if value > 1 {
		return 1
	}
	return value
}

func normalizedCombinedText(meta *types.TokenCountMeta) string {
	if meta == nil {
		return ""
	}
	return strings.ToLower(strings.TrimSpace(meta.CombineText))
}

func hasFileType(meta *types.TokenCountMeta, fileType types.FileType) bool {
	if meta == nil {
		return false
	}
	for _, file := range meta.Files {
		if file != nil && file.FileType == fileType {
			return true
		}
	}
	return false
}

func isSimpleRewriteOrTranslation(text string) bool {
	if text == "" {
		return false
	}
	return containsAny(text, []string{
		"翻译",
		"translate",
		"改写",
		"rewrite",
		"润色",
		"polish",
		"格式调整",
		"format",
	})
}

func containsAny(text string, terms []string) bool {
	for _, term := range terms {
		if strings.Contains(text, strings.ToLower(term)) {
			return true
		}
	}
	return false
}
