package service

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"sort"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service/smartrouting"
)

const (
	SmartRoutingMetricsSchemaVersion = 1
	SmartRoutingMetricsDefaultWindow = 24 * time.Hour
	SmartRoutingMetricsMaximumWindow = 7 * 24 * time.Hour
	SmartRoutingMetricsMaximumLogs   = 250000
)

var ErrSmartRoutingMetricsTooManyLogs = errors.New("smart routing metrics query matched too many logs")

type SmartRoutingMetricsDataQuality struct {
	MatchedLogs      int64 `json:"matched_logs"`
	ValidDecisions   int64 `json:"valid_decisions"`
	InvalidDecisions int64 `json:"invalid_decisions"`
	LegacyDecisions  int64 `json:"legacy_decisions"`
}

type SmartRoutingMetricsSummary struct {
	SuccessfulDecisions   int64   `json:"successful_decisions"`
	FallbackDecisions     int64   `json:"fallback_decisions"`
	FallbackRate          float64 `json:"fallback_rate"`
	FallbackHopSum        int64   `json:"fallback_hop_sum"`
	AverageCandidateCount float64 `json:"average_candidate_count"`
}

type SmartRoutingAverageScoreFactors struct {
	Cost        float64 `json:"cost"`
	Reliability float64 `json:"reliability"`
	Latency     float64 `json:"latency"`
	Throughput  float64 `json:"throughput"`
	Quality     float64 `json:"quality"`
	TaskMatch   float64 `json:"task_match"`
	Context     float64 `json:"context"`
	Cache       float64 `json:"cache"`
	ResetWindow float64 `json:"reset_window"`
	Affinity    float64 `json:"affinity"`
}

type SmartRoutingNamedCount struct {
	Name  string `json:"name"`
	Count int64  `json:"count"`
}

type SmartRoutingChannelCount struct {
	ChannelID int   `json:"channel_id"`
	Count     int64 `json:"count"`
}

type SmartRoutingTimelinePoint struct {
	BucketTimestamp     int64 `json:"bucket_timestamp"`
	SuccessfulDecisions int64 `json:"successful_decisions"`
	FallbackDecisions   int64 `json:"fallback_decisions"`
}

type SmartRoutingMetricsResult struct {
	SchemaVersion       int                             `json:"schema_version"`
	StartTimestamp      int64                           `json:"start_timestamp"`
	EndTimestamp        int64                           `json:"end_timestamp"`
	DataQuality         SmartRoutingMetricsDataQuality  `json:"data_quality"`
	Summary             SmartRoutingMetricsSummary      `json:"summary"`
	AverageScoreFactors SmartRoutingAverageScoreFactors `json:"average_score_factors"`
	ByPolicy            []SmartRoutingNamedCount        `json:"by_policy"`
	ByComplexity        []SmartRoutingNamedCount        `json:"by_complexity"`
	ByTaskType          []SmartRoutingNamedCount        `json:"by_task_type"`
	ByOriginalModel     []SmartRoutingNamedCount        `json:"by_original_model"`
	BySelectedModel     []SmartRoutingNamedCount        `json:"by_selected_model"`
	BySelectedChannel   []SmartRoutingChannelCount      `json:"by_selected_channel"`
	BySelectedHealth    []SmartRoutingNamedCount        `json:"by_selected_health"`
	Timeline            []SmartRoutingTimelinePoint     `json:"timeline"`
}

type smartRoutingMetricsLogEnvelope struct {
	SmartRouting *smartRoutingMetricsDecisionLog `json:"smart_routing"`
}

type smartRoutingMetricsDecisionLog struct {
	SchemaVersion     json.RawMessage                  `json:"schema_version"`
	Enabled           *bool                            `json:"enabled"`
	Policy            string                           `json:"policy"`
	Complexity        string                           `json:"complexity"`
	TaskType          string                           `json:"task_type"`
	OriginalModel     string                           `json:"original_model"`
	SelectedModel     string                           `json:"selected_model"`
	SelectedChannelID *int                             `json:"selected_channel_id"`
	SelectedHealth    string                           `json:"selected_health"`
	CandidateCount    *int                             `json:"candidate_count"`
	FallbackIndex     *int                             `json:"fallback_index"`
	ScoreFactors      *smartRoutingMetricsScoreFactors `json:"score_factors"`
}

type smartRoutingMetricsScoreFactors struct {
	Cost        *float64 `json:"cost"`
	Reliability *float64 `json:"reliability"`
	Latency     *float64 `json:"latency"`
	Throughput  *float64 `json:"throughput"`
	Quality     *float64 `json:"quality"`
	TaskMatch   *float64 `json:"task_match"`
	Context     *float64 `json:"context"`
	Cache       *float64 `json:"cache"`
	ResetWindow *float64 `json:"reset_window"`
	Affinity    *float64 `json:"affinity"`
}

type smartRoutingMetricsAccumulator struct {
	result            SmartRoutingMetricsResult
	candidateCountSum int64
	scoreFactorSums   SmartRoutingAverageScoreFactors
	policy            map[string]int64
	complexity        map[string]int64
	taskType          map[string]int64
	originalModel     map[string]int64
	selectedModel     map[string]int64
	selectedChannel   map[int]int64
	selectedHealth    map[string]int64
	timeline          map[int64]*SmartRoutingTimelinePoint
}

func QuerySmartRoutingMetrics(ctx context.Context, startTimestamp int64, endTimestamp int64) (SmartRoutingMetricsResult, error) {
	accumulator := newSmartRoutingMetricsAccumulator(startTimestamp, endTimestamp)
	matched, err := model.IterateSmartRoutingConsumeLogs(
		ctx,
		startTimestamp,
		endTimestamp,
		SmartRoutingMetricsMaximumLogs,
		accumulator.add,
	)
	if errors.Is(err, model.ErrSmartRoutingLogQueryLimitExceeded) {
		return SmartRoutingMetricsResult{}, ErrSmartRoutingMetricsTooManyLogs
	}
	if err != nil {
		return SmartRoutingMetricsResult{}, err
	}
	accumulator.result.DataQuality.MatchedLogs = int64(matched)
	return accumulator.finish(), nil
}

func newSmartRoutingMetricsAccumulator(startTimestamp int64, endTimestamp int64) *smartRoutingMetricsAccumulator {
	accumulator := &smartRoutingMetricsAccumulator{
		result: SmartRoutingMetricsResult{
			SchemaVersion:  SmartRoutingMetricsSchemaVersion,
			StartTimestamp: startTimestamp,
			EndTimestamp:   endTimestamp,
		},
		policy:          map[string]int64{},
		complexity:      map[string]int64{},
		taskType:        map[string]int64{},
		originalModel:   map[string]int64{},
		selectedModel:   map[string]int64{},
		selectedChannel: map[int]int64{},
		selectedHealth:  map[string]int64{},
		timeline:        map[int64]*SmartRoutingTimelinePoint{},
	}
	startBucket := startTimestamp - startTimestamp%int64(time.Hour/time.Second)
	endBucket := endTimestamp - endTimestamp%int64(time.Hour/time.Second)
	for bucket := startBucket; bucket <= endBucket; bucket += int64(time.Hour / time.Second) {
		accumulator.timeline[bucket] = &SmartRoutingTimelinePoint{BucketTimestamp: bucket}
	}
	return accumulator
}

func (accumulator *smartRoutingMetricsAccumulator) add(projection model.SmartRoutingLogProjection) error {
	var envelope smartRoutingMetricsLogEnvelope
	if err := common.UnmarshalJsonStr(projection.Other, &envelope); err != nil || envelope.SmartRouting == nil {
		accumulator.result.DataQuality.InvalidDecisions++
		return nil
	}
	decision := envelope.SmartRouting
	legacy, valid := validateSmartRoutingMetricsDecision(decision, projection)
	if !valid {
		accumulator.result.DataQuality.InvalidDecisions++
		return nil
	}
	accumulator.result.DataQuality.ValidDecisions++
	if legacy {
		accumulator.result.DataQuality.LegacyDecisions++
	}
	accumulator.result.Summary.SuccessfulDecisions++
	accumulator.candidateCountSum += int64(*decision.CandidateCount)
	if *decision.FallbackIndex > 0 {
		accumulator.result.Summary.FallbackDecisions++
		accumulator.result.Summary.FallbackHopSum += int64(*decision.FallbackIndex)
	}
	accumulator.policy[decision.Policy]++
	accumulator.complexity[decision.Complexity]++
	accumulator.taskType[decision.TaskType]++
	accumulator.originalModel[decision.OriginalModel]++
	accumulator.selectedModel[decision.SelectedModel]++
	accumulator.selectedChannel[*decision.SelectedChannelID]++
	accumulator.selectedHealth[decision.SelectedHealth]++
	accumulator.addScoreFactors(decision.ScoreFactors)

	bucket := projection.CreatedAt - projection.CreatedAt%int64(time.Hour/time.Second)
	point, ok := accumulator.timeline[bucket]
	if ok {
		point.SuccessfulDecisions++
		if *decision.FallbackIndex > 0 {
			point.FallbackDecisions++
		}
	}
	return nil
}

func (accumulator *smartRoutingMetricsAccumulator) addScoreFactors(factors *smartRoutingMetricsScoreFactors) {
	accumulator.scoreFactorSums.Cost += *factors.Cost
	accumulator.scoreFactorSums.Reliability += *factors.Reliability
	accumulator.scoreFactorSums.Latency += *factors.Latency
	accumulator.scoreFactorSums.Throughput += *factors.Throughput
	accumulator.scoreFactorSums.Quality += *factors.Quality
	accumulator.scoreFactorSums.TaskMatch += *factors.TaskMatch
	accumulator.scoreFactorSums.Context += *factors.Context
	accumulator.scoreFactorSums.Cache += *factors.Cache
	accumulator.scoreFactorSums.ResetWindow += *factors.ResetWindow
	accumulator.scoreFactorSums.Affinity += *factors.Affinity
}

func (accumulator *smartRoutingMetricsAccumulator) finish() SmartRoutingMetricsResult {
	count := accumulator.result.Summary.SuccessfulDecisions
	if count > 0 {
		accumulator.result.Summary.FallbackRate = roundSmartRoutingMetric(float64(accumulator.result.Summary.FallbackDecisions) / float64(count))
		accumulator.result.Summary.AverageCandidateCount = roundSmartRoutingMetric(float64(accumulator.candidateCountSum) / float64(count))
		accumulator.result.AverageScoreFactors = divideSmartRoutingScoreFactors(accumulator.scoreFactorSums, float64(count))
	}
	accumulator.result.ByPolicy = sortedSmartRoutingNamedCounts(accumulator.policy)
	accumulator.result.ByComplexity = sortedSmartRoutingNamedCounts(accumulator.complexity)
	accumulator.result.ByTaskType = sortedSmartRoutingNamedCounts(accumulator.taskType)
	accumulator.result.ByOriginalModel = sortedSmartRoutingNamedCounts(accumulator.originalModel)
	accumulator.result.BySelectedModel = sortedSmartRoutingNamedCounts(accumulator.selectedModel)
	accumulator.result.BySelectedChannel = sortedSmartRoutingChannelCounts(accumulator.selectedChannel)
	accumulator.result.BySelectedHealth = sortedSmartRoutingNamedCounts(accumulator.selectedHealth)

	buckets := make([]int64, 0, len(accumulator.timeline))
	for bucket := range accumulator.timeline {
		buckets = append(buckets, bucket)
	}
	sort.Slice(buckets, func(left int, right int) bool { return buckets[left] < buckets[right] })
	accumulator.result.Timeline = make([]SmartRoutingTimelinePoint, 0, len(buckets))
	for _, bucket := range buckets {
		accumulator.result.Timeline = append(accumulator.result.Timeline, *accumulator.timeline[bucket])
	}
	return accumulator.result
}

func validateSmartRoutingMetricsDecision(decision *smartRoutingMetricsDecisionLog, projection model.SmartRoutingLogProjection) (bool, bool) {
	if decision.Enabled == nil || !*decision.Enabled || decision.SelectedChannelID == nil ||
		decision.CandidateCount == nil || decision.FallbackIndex == nil || decision.ScoreFactors == nil {
		return false, false
	}
	legacy := len(decision.SchemaVersion) == 0
	if !legacy {
		var schemaVersion int
		if err := common.Unmarshal(decision.SchemaVersion, &schemaVersion); err != nil || schemaVersion != SmartRoutingMetricsSchemaVersion {
			return false, false
		}
	}
	if !validSmartRoutingPolicy(decision.Policy) || !validSmartRoutingComplexity(decision.Complexity) ||
		!validSmartRoutingTaskType(decision.TaskType) || !validSmartRoutingHealth(decision.SelectedHealth) {
		return false, false
	}
	if decision.OriginalModel == "" || decision.SelectedModel == "" || *decision.SelectedChannelID <= 0 ||
		*decision.SelectedChannelID != projection.ChannelID || decision.SelectedModel != projection.ModelName {
		return false, false
	}
	if *decision.CandidateCount <= 0 || *decision.FallbackIndex < 0 || *decision.FallbackIndex >= *decision.CandidateCount {
		return false, false
	}
	if !validSmartRoutingScoreFactors(decision.ScoreFactors) {
		return false, false
	}
	return legacy, true
}

func validSmartRoutingScoreFactors(factors *smartRoutingMetricsScoreFactors) bool {
	values := []*float64{
		factors.Cost,
		factors.Reliability,
		factors.Latency,
		factors.Throughput,
		factors.Quality,
		factors.TaskMatch,
		factors.Context,
		factors.Cache,
		factors.ResetWindow,
		factors.Affinity,
	}
	for _, value := range values {
		if value == nil || math.IsNaN(*value) || math.IsInf(*value, 0) || *value < 0 || *value > 1 {
			return false
		}
	}
	return true
}

func validSmartRoutingPolicy(value string) bool {
	switch smartrouting.RoutePolicy(value) {
	case smartrouting.PolicyCostFirst, smartrouting.PolicyBalanced, smartrouting.PolicyQualityFirst,
		smartrouting.PolicyLatencyFirst, smartrouting.PolicyReliabilityFirst:
		return true
	default:
		return false
	}
}

func validSmartRoutingComplexity(value string) bool {
	switch smartrouting.TaskComplexity(value) {
	case smartrouting.TaskSimple, smartrouting.TaskStandard, smartrouting.TaskComplex, smartrouting.TaskCritical:
		return true
	default:
		return false
	}
}

func validSmartRoutingTaskType(value string) bool {
	switch smartrouting.TaskType(value) {
	case smartrouting.TaskTypeGeneral, smartrouting.TaskTypeTranslation, smartrouting.TaskTypeCoding,
		smartrouting.TaskTypeReasoning, smartrouting.TaskTypeAnalysis, smartrouting.TaskTypeCreative:
		return true
	default:
		return false
	}
}

func validSmartRoutingHealth(value string) bool {
	switch smartrouting.ChannelHealthState(value) {
	case smartrouting.ChannelHealthHealthy, smartrouting.ChannelHealthDegraded,
		smartrouting.ChannelHealthOpen, smartrouting.ChannelHealthHalfOpen:
		return true
	default:
		return false
	}
}

func divideSmartRoutingScoreFactors(sums SmartRoutingAverageScoreFactors, count float64) SmartRoutingAverageScoreFactors {
	return SmartRoutingAverageScoreFactors{
		Cost:        roundSmartRoutingMetric(sums.Cost / count),
		Reliability: roundSmartRoutingMetric(sums.Reliability / count),
		Latency:     roundSmartRoutingMetric(sums.Latency / count),
		Throughput:  roundSmartRoutingMetric(sums.Throughput / count),
		Quality:     roundSmartRoutingMetric(sums.Quality / count),
		TaskMatch:   roundSmartRoutingMetric(sums.TaskMatch / count),
		Context:     roundSmartRoutingMetric(sums.Context / count),
		Cache:       roundSmartRoutingMetric(sums.Cache / count),
		ResetWindow: roundSmartRoutingMetric(sums.ResetWindow / count),
		Affinity:    roundSmartRoutingMetric(sums.Affinity / count),
	}
}

func sortedSmartRoutingNamedCounts(counts map[string]int64) []SmartRoutingNamedCount {
	result := make([]SmartRoutingNamedCount, 0, len(counts))
	for name, count := range counts {
		result = append(result, SmartRoutingNamedCount{Name: name, Count: count})
	}
	sort.Slice(result, func(left int, right int) bool {
		if result[left].Count == result[right].Count {
			return result[left].Name < result[right].Name
		}
		return result[left].Count > result[right].Count
	})
	return result
}

func sortedSmartRoutingChannelCounts(counts map[int]int64) []SmartRoutingChannelCount {
	result := make([]SmartRoutingChannelCount, 0, len(counts))
	for channelID, count := range counts {
		result = append(result, SmartRoutingChannelCount{ChannelID: channelID, Count: count})
	}
	sort.Slice(result, func(left int, right int) bool {
		if result[left].Count == result[right].Count {
			return result[left].ChannelID < result[right].ChannelID
		}
		return result[left].Count > result[right].Count
	})
	return result
}

func roundSmartRoutingMetric(value float64) float64 {
	return math.Round(value*1000000) / 1000000
}
