package smartrouting

import (
	"math"
	"sort"
	"strings"

	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/QuantumNous/new-api/service/contextconsensus"
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

type TaskType string

const (
	TaskTypeGeneral     TaskType = "general"
	TaskTypeTranslation TaskType = "translation"
	TaskTypeCoding      TaskType = "coding"
	TaskTypeReasoning   TaskType = "reasoning"
	TaskTypeAnalysis    TaskType = "analysis"
	TaskTypeCreative    TaskType = "creative"
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
	ContextConstraint     contextconsensus.ContextRoutingConstraint
}

type VirtualModelProfile struct {
	Name           string
	Policy         RoutePolicy
	PreferredTiers []ModelQualityTier
}

type SmartRouteAnalysis struct {
	TaskComplexity     TaskComplexity
	TaskType           TaskType
	RecommendedTier    ModelQualityTier
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
	CodingScore        float64
	ReasoningScore     float64
	SpeedScore         float64
	ContextScore       float64
	PreferenceScore    float64
	AffinityScore      float64
	CacheAffinityScore float64
	ResetWindowScore   float64
	HealthState        ChannelHealthState
	HardUnavailable    bool
	FinalScore         float64
	ScoreFactors       ScoreFactors
}

type ScoreFactors struct {
	Cost        float64
	Reliability float64
	Latency     float64
	Throughput  float64
	Quality     float64
	TaskMatch   float64
	Context     float64
	Cache       float64
	ResetWindow float64
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
	TaskType           TaskType
	RecommendedTier    ModelQualityTier
	ContextRequirement ContextRequirement
	OriginalModel      string
	SelectedModel      string
	SelectedChannelID  int
	SelectedHealth     ChannelHealthState
	CandidateCount     int
	FallbackIndex      int
	ScoreFactors       ScoreFactors
	DecisionReasons    []string
	ContextConsensus   ContextConsensusLog
}

type ContextConsensusLog struct {
	Mode                    string
	Version                 int
	ValidationMode          string
	ValidationResult        string
	Protocol                string
	Compacted               bool
	PreservedRecentMessages int
	PreservedSegmentCount   int
	ToolExchangeCount       int
	InputTokensBefore       int
	BindingLevel            string
	BindingReasonCodes      []string
	SwitchAllowed           bool
	WouldBlock              bool
}

type policyWeights struct {
	cost        float64
	reliability float64
	latency     float64
	throughput  float64
	quality     float64
	taskMatch   float64
	context     float64
	cache       float64
	resetWindow float64
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
		cost:        0.40,
		reliability: 0.15,
		latency:     0.10,
		throughput:  0.04,
		quality:     0.05,
		taskMatch:   0.08,
		context:     0.05,
		cache:       0.04,
		resetWindow: 0.04,
		affinity:    0.05,
	},
	PolicyBalanced: {
		cost:        0.18,
		reliability: 0.20,
		latency:     0.15,
		throughput:  0.05,
		quality:     0.12,
		taskMatch:   0.12,
		context:     0.06,
		cache:       0.04,
		resetWindow: 0.03,
		affinity:    0.05,
	},
	PolicyQualityFirst: {
		cost:        0.07,
		reliability: 0.18,
		latency:     0.07,
		throughput:  0.04,
		quality:     0.25,
		taskMatch:   0.20,
		context:     0.07,
		cache:       0.03,
		resetWindow: 0.03,
		affinity:    0.06,
	},
	PolicyLatencyFirst: {
		cost:        0.10,
		reliability: 0.17,
		latency:     0.30,
		throughput:  0.08,
		quality:     0.05,
		taskMatch:   0.09,
		context:     0.05,
		cache:       0.06,
		resetWindow: 0.04,
		affinity:    0.06,
	},
	PolicyReliabilityFirst: {
		cost:        0.06,
		reliability: 0.32,
		latency:     0.10,
		throughput:  0.05,
		quality:     0.08,
		taskMatch:   0.10,
		context:     0.05,
		cache:       0.06,
		resetWindow: 0.08,
		affinity:    0.10,
	},
}

func ResolveVirtualModel(modelName string) (VirtualModelProfile, bool) {
	normalized := strings.ToLower(strings.TrimSpace(modelName))
	if normalized == "auto" || normalized == "smart" {
		normalized = "auto:balanced"
	}
	if strings.HasPrefix(normalized, "smart:") {
		normalized = "auto:" + strings.TrimPrefix(normalized, "smart:")
	}
	profile, ok := virtualModelProfiles[normalized]
	return profile, ok
}

func AnalyzeRequest(request SmartRouteRequest) SmartRouteAnalysis {
	taskScore, taskReasons := scoreTaskComplexity(request)
	contextScore, contextReasons := scoreContextRequirement(request)
	taskComplexity := classifyTaskComplexity(taskScore, request.RequiresReliability)
	taskType := classifyTaskType(request)
	return SmartRouteAnalysis{
		TaskComplexity:     taskComplexity,
		TaskType:           taskType,
		RecommendedTier:    recommendedTier(taskComplexity, taskType, request),
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
	analysis := AnalyzeRequest(request)
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
		scored.ScoreFactors = buildScoreFactors(request, analysis, candidate, maxQuota)
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
		"task_type":           string(decision.TaskType),
		"recommended_tier":    string(decision.RecommendedTier),
		"context_requirement": string(decision.ContextRequirement),
		"original_model":      decision.OriginalModel,
		"selected_model":      decision.SelectedModel,
		"selected_channel_id": decision.SelectedChannelID,
		"selected_health":     string(decision.SelectedHealth),
		"candidate_count":     decision.CandidateCount,
		"fallback_index":      decision.FallbackIndex,
		"score_factors": map[string]interface{}{
			"cost":         decision.ScoreFactors.Cost,
			"reliability":  decision.ScoreFactors.Reliability,
			"latency":      decision.ScoreFactors.Latency,
			"throughput":   decision.ScoreFactors.Throughput,
			"quality":      decision.ScoreFactors.Quality,
			"task_match":   decision.ScoreFactors.TaskMatch,
			"context":      decision.ScoreFactors.Context,
			"cache":        decision.ScoreFactors.Cache,
			"reset_window": decision.ScoreFactors.ResetWindow,
			"affinity":     decision.ScoreFactors.Affinity,
		},
	}
	if len(decision.DecisionReasons) > 0 {
		fields["decision_reasons"] = decision.DecisionReasons
	}
	if decision.ContextConsensus.Mode != "" {
		contextConsensus := map[string]interface{}{
			"mode":                      decision.ContextConsensus.Mode,
			"compacted":                 decision.ContextConsensus.Compacted,
			"preserved_recent_messages": decision.ContextConsensus.PreservedRecentMessages,
		}
		if decision.ContextConsensus.ValidationMode != "" {
			contextConsensus["version"] = decision.ContextConsensus.Version
			contextConsensus["validation_mode"] = decision.ContextConsensus.ValidationMode
			contextConsensus["validation_result"] = decision.ContextConsensus.ValidationResult
			contextConsensus["protocol"] = decision.ContextConsensus.Protocol
			contextConsensus["preserved_segment_count"] = decision.ContextConsensus.PreservedSegmentCount
			contextConsensus["tool_exchange_count"] = decision.ContextConsensus.ToolExchangeCount
			contextConsensus["input_tokens_before"] = decision.ContextConsensus.InputTokensBefore
			contextConsensus["binding_level"] = decision.ContextConsensus.BindingLevel
			contextConsensus["binding_reason_codes"] = append([]string(nil), decision.ContextConsensus.BindingReasonCodes...)
			contextConsensus["switch_allowed"] = decision.ContextConsensus.SwitchAllowed
			contextConsensus["would_block"] = decision.ContextConsensus.WouldBlock
		}
		fields["context_consensus"] = contextConsensus
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
		toolComplexity := request.ToolCount
		if toolComplexity > 3 {
			toolComplexity = 3
		}
		score += 3 + toolComplexity
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
	if containsAny(text, []string{"代码", "code", "debug", "调试", "迁移", "migration", "架构", "方案", "多步骤", "multi-step", "schema"}) {
		score += 2
		reasons = append(reasons, "complex_task_terms")
	}
	if containsAny(text, []string{"证明", "prove", "数学", "math", "定理", "theorem", "根因", "root cause"}) {
		score += 2
		reasons = append(reasons, "deep_analysis_terms")
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

func classifyTaskType(request SmartRouteRequest) TaskType {
	text := normalizedCombinedText(request.TokenMeta)
	if isSimpleRewriteOrTranslation(text) && containsAny(text, []string{"翻译", "translate"}) {
		return TaskTypeTranslation
	}
	if request.ReasoningRequested || containsAny(text, []string{"reasoning", "推理", "证明", "prove", "定理", "theorem"}) {
		return TaskTypeReasoning
	}
	if isCodingTaskText(text) {
		return TaskTypeCoding
	}
	if containsAny(text, []string{"分析", "analysis", "比较", "compare", "评估", "evaluate", "根因", "root cause"}) {
		return TaskTypeAnalysis
	}
	if containsAny(text, []string{"创作", "creative", "故事", "story", "文案", "copywriting", "诗", "poem"}) {
		return TaskTypeCreative
	}
	return TaskTypeGeneral
}

func recommendedTier(complexity TaskComplexity, taskType TaskType, request SmartRouteRequest) ModelQualityTier {
	if taskType == TaskTypeReasoning || request.ReasoningRequested {
		return QualityReasoning
	}

	tier := QualityEconomy
	switch complexity {
	case TaskCritical, TaskComplex:
		tier = QualityPremium
	case TaskStandard:
		tier = QualityStandard
	}
	if (request.HasTools || request.ToolCount > 0 || request.RequiresJSONSchema) && tier == QualityEconomy {
		return QualityStandard
	}
	return tier
}

func rejectCandidate(request SmartRouteRequest, candidate SmartRouteCandidate) []string {
	reasons := make([]string, 0)
	if candidate.HardUnavailable {
		reasons = append(reasons, "channel_hard_unavailable")
	} else if candidate.HealthState == ChannelHealthOpen {
		reasons = append(reasons, "channel_health_open")
	}
	if request.ContextConstraint.ValidationMode != "" && !request.ContextConstraint.SwitchAllowed &&
		candidate.ModelName != request.OriginalModel {
		reasons = append(reasons, "context_state_switch_not_allowed")
	}
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

func buildScoreFactors(request SmartRouteRequest, analysis SmartRouteAnalysis, candidate SmartRouteCandidate, maxQuota int) ScoreFactors {
	return ScoreFactors{
		Cost:        costScore(candidate.EstimatedQuota, maxQuota),
		Reliability: normalizeScore(candidate.Reliability),
		Latency:     candidateLatencyFactor(candidate),
		Throughput:  normalizeScore(candidate.ThroughputScore),
		Quality:     candidateQualityFactor(request, candidate),
		TaskMatch:   candidateTaskMatchFactor(analysis, candidate),
		Context:     candidateContextFactor(request, candidate),
		Cache:       normalizeScore(candidate.CacheAffinityScore),
		ResetWindow: normalizeScore(candidate.ResetWindowScore),
		Affinity:    normalizeScore(candidate.AffinityScore),
	}
}

func candidateLatencyFactor(candidate SmartRouteCandidate) float64 {
	if candidate.SpeedScore > 0 {
		return normalizeScore(candidate.SpeedScore)
	}
	return normalizeScore(candidate.LatencyScore)
}

func candidateQualityFactor(request SmartRouteRequest, candidate SmartRouteCandidate) float64 {
	quality := candidate.QualityScore
	text := normalizedCombinedText(request.TokenMeta)
	if isCodingTaskText(text) && candidate.CodingScore > 0 {
		quality = candidate.CodingScore
	}
	if (request.ReasoningRequested || strings.Contains(text, "reasoning") || strings.Contains(text, "推理")) && candidate.ReasoningScore > 0 {
		quality = candidate.ReasoningScore
	}
	if candidate.ContextScore > 0 && (request.ContextTokensRequired >= 32000 || request.ContextTokensRequired > candidate.MaxContextTokens/2) {
		quality = (quality*0.75 + candidate.ContextScore*0.25)
	}
	if candidate.PreferenceScore > 0 && quality == 0 {
		quality = candidate.PreferenceScore
	}
	return normalizeScore(quality)
}

func candidateTaskMatchFactor(analysis SmartRouteAnalysis, candidate SmartRouteCandidate) float64 {
	specialtyScore := 0.5
	switch analysis.TaskType {
	case TaskTypeCoding:
		if candidate.CodingScore > 0 {
			specialtyScore = candidate.CodingScore
		}
	case TaskTypeReasoning:
		if candidate.ReasoningScore > 0 {
			specialtyScore = candidate.ReasoningScore
		} else if candidate.SupportsReasoning {
			specialtyScore = 0.8
		}
	case TaskTypeAnalysis:
		if candidate.ReasoningScore > 0 {
			specialtyScore = candidate.ReasoningScore
		}
	}
	tierScore := qualityTierMatchScore(analysis.RecommendedTier, candidate.QualityTier)
	return normalizeScore(specialtyScore*0.5 + tierScore*0.5)
}

func qualityTierMatchScore(recommended ModelQualityTier, actual ModelQualityTier) float64 {
	if recommended == actual {
		return 1
	}
	tierOrder := map[ModelQualityTier]int{
		QualityEconomy:   0,
		QualityStandard:  1,
		QualityPremium:   2,
		QualityReasoning: 3,
	}
	recommendedOrder, recommendedKnown := tierOrder[recommended]
	actualOrder, actualKnown := tierOrder[actual]
	if !recommendedKnown || !actualKnown {
		return 0.5
	}
	difference := recommendedOrder - actualOrder
	if difference < 0 {
		difference = -difference
	}
	if difference == 1 {
		return 0.7
	}
	return 0.35
}

func candidateContextFactor(request SmartRouteRequest, candidate SmartRouteCandidate) float64 {
	requiredTokens := request.ContextTokensRequired
	if requiredTokens <= 0 {
		if candidate.ContextScore > 0 {
			return normalizeScore(candidate.ContextScore)
		}
		return 0.5
	}
	if candidate.MaxContextTokens <= 0 {
		return 0.5
	}
	if requiredTokens >= candidate.MaxContextTokens {
		return 0
	}
	headroom := 1 - float64(requiredTokens)/float64(candidate.MaxContextTokens)
	if candidate.ContextScore > 0 {
		headroom = headroom*0.75 + normalizeScore(candidate.ContextScore)*0.25
	}
	return normalizeScore(headroom)
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
		factors.TaskMatch*weights.taskMatch +
		factors.Context*weights.context +
		factors.Cache*weights.cache +
		factors.ResetWindow*weights.resetWindow +
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

func isCodingTaskText(text string) bool {
	return containsAny(text, []string{"代码", "code", "debug", "调试", "迁移", "migration", "架构", "schema"})
}

func containsAny(text string, terms []string) bool {
	for _, term := range terms {
		if strings.Contains(text, strings.ToLower(term)) {
			return true
		}
	}
	return false
}
