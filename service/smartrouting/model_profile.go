package smartrouting

import (
	"bytes"
	"encoding/csv"
	"errors"
	"math"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/QuantumNous/new-api/common"
	"gopkg.in/yaml.v3"
)

const (
	BenchmarkSourceAider              = "aider"
	BenchmarkSourceSWEBench           = "swe_bench"
	BenchmarkSourceArtificialAnalysis = "artificial_analysis"
	BenchmarkSourceArena              = "arena"
)

type ModelRoutingProfile struct {
	ModelName          string             `json:"model_name"`
	ModelPattern       string             `json:"model_pattern,omitempty"`
	QualityScore       float64            `json:"quality_score"`
	GeneralScore       float64            `json:"general_score"`
	CodingScore        float64            `json:"coding_score"`
	ReasoningScore     float64            `json:"reasoning_score"`
	SpeedScore         float64            `json:"speed_score"`
	CostScore          float64            `json:"cost_score"`
	ContextScore       float64            `json:"context_score"`
	ReliabilityScore   float64            `json:"reliability_score"`
	PreferenceScore    float64            `json:"preference_score"`
	MaxContextTokens   int                `json:"max_context_tokens"`
	SupportsTools      bool               `json:"supports_tools"`
	SupportsJSONSchema bool               `json:"supports_json_schema"`
	SupportsVision     bool               `json:"supports_vision"`
	SupportsAudio      bool               `json:"supports_audio"`
	SupportsReasoning  bool               `json:"supports_reasoning"`
	SupportsStream     bool               `json:"supports_stream"`
	SourceScores       map[string]float64 `json:"source_scores,omitempty"`
	Sources            []string           `json:"sources,omitempty"`
	UpdatedAt          int64              `json:"updated_at"`
}

type ExternalBenchmarkRecord struct {
	Source    string  `json:"source"`
	ModelName string  `json:"model_name"`
	PassRate  float64 `json:"pass_rate"`
	Cost      float64 `json:"cost"`
	Seconds   float64 `json:"seconds"`
}

type benchmarkColumnKeys struct {
	Model   []string
	Score   []string
	Cost    []string
	Seconds []string
}

var modelRoutingProfileCache = struct {
	sync.RWMutex
	profiles map[string]ModelRoutingProfile
}{profiles: map[string]ModelRoutingProfile{}}

func ExternalBenchmarkRefreshInterval() time.Duration {
	return 10 * 24 * time.Hour
}

func ModelProfileRefreshInterval() time.Duration {
	return 24 * time.Hour
}

func ClearModelRoutingProfileCache() {
	modelRoutingProfileCache.Lock()
	defer modelRoutingProfileCache.Unlock()
	modelRoutingProfileCache.profiles = map[string]ModelRoutingProfile{}
}

func SetModelRoutingProfiles(profiles []ModelRoutingProfile) {
	next := make(map[string]ModelRoutingProfile, len(profiles))
	for _, profile := range profiles {
		key := normalizeProfileModelName(profile.ModelName)
		if key == "" {
			key = normalizeProfileModelName(profile.ModelPattern)
		}
		if key == "" {
			continue
		}
		next[key] = profile
	}
	modelRoutingProfileCache.Lock()
	modelRoutingProfileCache.profiles = next
	modelRoutingProfileCache.Unlock()
}

func GetModelRoutingProfile(modelName string) (ModelRoutingProfile, bool) {
	key := normalizeProfileModelName(modelName)
	if key == "" {
		return ModelRoutingProfile{}, false
	}
	modelRoutingProfileCache.RLock()
	defer modelRoutingProfileCache.RUnlock()
	profile, ok := modelRoutingProfileCache.profiles[key]
	return profile, ok
}

func ParseAiderLeaderboard(data []byte) ([]ExternalBenchmarkRecord, error) {
	return parseExternalBenchmarkLeaderboard(data, BenchmarkSourceAider, "aider", benchmarkColumnKeys{
		Model:   []string{"model", "model_name", "name"},
		Score:   []string{"pass_rate", "pass_rate_2", "pass_rate_1", "percent_correct", "score"},
		Cost:    []string{"cost", "total_cost", "cost_per_case", "price"},
		Seconds: []string{"seconds", "seconds_per_case", "time"},
	})
}

func ParseSWEBenchLeaderboard(data []byte) ([]ExternalBenchmarkRecord, error) {
	return parseExternalBenchmarkLeaderboard(data, BenchmarkSourceSWEBench, "swe-bench", benchmarkColumnKeys{
		Model:   []string{"model", "model_name", "name"},
		Score:   []string{"resolved", "resolve_rate", "resolved_rate", "percent_resolved", "pass_rate", "score"},
		Cost:    []string{"cost", "total_cost", "cost_per_case", "price"},
		Seconds: []string{"seconds", "seconds_per_case", "time", "runtime"},
	})
}

func ParseArtificialAnalysisLeaderboard(data []byte) ([]ExternalBenchmarkRecord, error) {
	return parseExternalBenchmarkLeaderboard(data, BenchmarkSourceArtificialAnalysis, "artificial analysis", benchmarkColumnKeys{
		Model:   []string{"model", "model_name", "name"},
		Score:   []string{"intelligence_index", "quality_index", "quality_score", "intelligence", "score"},
		Cost:    []string{"blended_price", "price", "cost", "cost_per_1m_tokens", "median_price"},
		Seconds: []string{"output_speed", "speed", "tokens_per_second", "median_output_speed"},
	})
}

func ParseArenaLeaderboard(data []byte) ([]ExternalBenchmarkRecord, error) {
	return parseExternalBenchmarkLeaderboard(data, BenchmarkSourceArena, "arena", benchmarkColumnKeys{
		Model:   []string{"model", "model_name", "name"},
		Score:   []string{"arena_score", "elo", "rating", "score", "arena_elo"},
		Cost:    []string{"cost", "price"},
		Seconds: []string{"seconds", "time", "latency"},
	})
}

func parseExternalBenchmarkLeaderboard(data []byte, source string, displayName string, keys benchmarkColumnKeys) ([]ExternalBenchmarkRecord, error) {
	trimmed := bytes.TrimSpace(data)
	if len(trimmed) == 0 {
		return nil, errors.New(displayName + " leaderboard data is empty")
	}

	var jsonRows []map[string]any
	if err := common.Unmarshal(trimmed, &jsonRows); err == nil {
		return parseBenchmarkRows(jsonRows, source, keys), nil
	}
	var yamlRows []map[string]any
	if err := yaml.Unmarshal(trimmed, &yamlRows); err == nil && len(yamlRows) > 0 {
		return parseBenchmarkRows(yamlRows, source, keys), nil
	}

	reader := csv.NewReader(bytes.NewReader(trimmed))
	reader.FieldsPerRecord = -1
	rows, err := reader.ReadAll()
	if err != nil || len(rows) < 2 {
		rows = parseMarkdownTableRows(string(trimmed))
	}
	if len(rows) < 2 {
		return nil, errors.New(displayName + " leaderboard data has no rows")
	}
	records := parseBenchmarkTableRows(rows, source, keys)
	if len(records) == 0 {
		records = parseBenchmarkTableRows(parseMarkdownTableRows(string(trimmed)), source, keys)
	}
	return records, nil
}

func BuildProfilesFromExternalBenchmarks(records []ExternalBenchmarkRecord, updatedAt int64) []ModelRoutingProfile {
	if len(records) == 0 {
		return nil
	}
	sourceStats := buildBenchmarkSourceStats(records)

	builders := make(map[string]*modelRoutingProfileBuilder)
	modelOrder := make([]string, 0, len(records))
	for _, record := range records {
		modelName := strings.TrimSpace(record.ModelName)
		if modelName == "" {
			continue
		}
		key := normalizeProfileModelName(modelName)
		builder, ok := builders[key]
		if !ok {
			builder = &modelRoutingProfileBuilder{
				profile: ModelRoutingProfile{
					ModelName:        modelName,
					ModelPattern:     modelName,
					ReliabilityScore: 0.8,
					SourceScores:     map[string]float64{},
					UpdatedAt:        updatedAt,
				},
			}
			builders[key] = builder
			modelOrder = append(modelOrder, key)
		}
		stats := sourceStats[record.Source]
		codingScore := normalizedRatio(record.PassRate, stats.MaxPassRate)
		costScore := inverseNormalizedRatio(record.Cost, stats.MaxCost)
		speedScore := inverseNormalizedRatio(record.Seconds, stats.MaxSeconds)
		builder.addSource(record.Source, codingScore)
		builder.addCostScore(costScore)
		builder.addSpeedScore(speedScore)
		builder.addBenchmarkScore(record.Source, codingScore)
	}
	profiles := make([]ModelRoutingProfile, 0, len(builders))
	for _, key := range modelOrder {
		profiles = append(profiles, builders[key].build())
	}
	return profiles
}

type benchmarkSourceStats struct {
	MaxPassRate float64
	MaxCost     float64
	MaxSeconds  float64
}

func buildBenchmarkSourceStats(records []ExternalBenchmarkRecord) map[string]benchmarkSourceStats {
	statsBySource := map[string]benchmarkSourceStats{}
	for _, record := range records {
		stats := statsBySource[record.Source]
		if record.PassRate > stats.MaxPassRate {
			stats.MaxPassRate = record.PassRate
		}
		if record.Cost > stats.MaxCost {
			stats.MaxCost = record.Cost
		}
		if record.Seconds > stats.MaxSeconds {
			stats.MaxSeconds = record.Seconds
		}
		statsBySource[record.Source] = stats
	}
	return statsBySource
}

func normalizeProfileModelName(modelName string) string {
	return strings.ToLower(strings.TrimSpace(modelName))
}

type modelRoutingProfileBuilder struct {
	profile                ModelRoutingProfile
	qualityScoreTotal      float64
	qualityScoreCount      int
	generalScoreTotal      float64
	generalScoreCount      int
	codingScoreTotal       float64
	codingScoreCount       int
	reasoningScoreTotal    float64
	reasoningScoreCount    int
	speedScoreTotal        float64
	speedScoreCount        int
	costScoreTotal         float64
	costScoreCount         int
	preferenceScoreTotal   float64
	preferenceScoreCount   int
	seenBenchmarkSourceSet map[string]bool
}

func (builder *modelRoutingProfileBuilder) addSource(source string, score float64) {
	source = strings.TrimSpace(source)
	if source == "" {
		return
	}
	if builder.seenBenchmarkSourceSet == nil {
		builder.seenBenchmarkSourceSet = map[string]bool{}
	}
	if !builder.seenBenchmarkSourceSet[source] {
		builder.profile.Sources = append(builder.profile.Sources, source)
		builder.seenBenchmarkSourceSet[source] = true
	}
	builder.profile.SourceScores[source] = score
}

func (builder *modelRoutingProfileBuilder) addBenchmarkScore(source string, score float64) {
	switch source {
	case BenchmarkSourceAider:
		builder.addCodingScore(score)
		builder.addReasoningScore(score)
	case BenchmarkSourceSWEBench:
		builder.addCodingScore(score)
		builder.addReasoningScore(score)
		builder.addGeneralScore(score)
	case BenchmarkSourceArtificialAnalysis:
		builder.addGeneralScore(score)
		builder.addQualityScore(score)
	case BenchmarkSourceArena:
		builder.addGeneralScore(score)
		builder.addPreferenceScore(score)
		builder.addQualityScore(score)
	default:
		builder.addGeneralScore(score)
		builder.addQualityScore(score)
	}
}

func (builder *modelRoutingProfileBuilder) addQualityScore(score float64) {
	if score <= 0 {
		return
	}
	builder.qualityScoreTotal += score
	builder.qualityScoreCount++
}

func (builder *modelRoutingProfileBuilder) addGeneralScore(score float64) {
	if score <= 0 {
		return
	}
	builder.generalScoreTotal += score
	builder.generalScoreCount++
}

func (builder *modelRoutingProfileBuilder) addCodingScore(score float64) {
	if score <= 0 {
		return
	}
	builder.codingScoreTotal += score
	builder.codingScoreCount++
}

func (builder *modelRoutingProfileBuilder) addReasoningScore(score float64) {
	if score <= 0 {
		return
	}
	builder.reasoningScoreTotal += score
	builder.reasoningScoreCount++
}

func (builder *modelRoutingProfileBuilder) addSpeedScore(score float64) {
	if score <= 0 {
		return
	}
	builder.speedScoreTotal += score
	builder.speedScoreCount++
}

func (builder *modelRoutingProfileBuilder) addCostScore(score float64) {
	if score <= 0 {
		return
	}
	builder.costScoreTotal += score
	builder.costScoreCount++
}

func (builder *modelRoutingProfileBuilder) addPreferenceScore(score float64) {
	if score <= 0 {
		return
	}
	builder.preferenceScoreTotal += score
	builder.preferenceScoreCount++
}

func (builder *modelRoutingProfileBuilder) build() ModelRoutingProfile {
	builder.profile.QualityScore = averagedScore(builder.qualityScoreTotal, builder.qualityScoreCount)
	builder.profile.GeneralScore = averagedScore(builder.generalScoreTotal, builder.generalScoreCount)
	builder.profile.CodingScore = averagedScore(builder.codingScoreTotal, builder.codingScoreCount)
	builder.profile.ReasoningScore = averagedScore(builder.reasoningScoreTotal, builder.reasoningScoreCount)
	builder.profile.SpeedScore = averagedScore(builder.speedScoreTotal, builder.speedScoreCount)
	builder.profile.CostScore = averagedScore(builder.costScoreTotal, builder.costScoreCount)
	builder.profile.PreferenceScore = averagedScore(builder.preferenceScoreTotal, builder.preferenceScoreCount)
	if builder.profile.QualityScore == 0 {
		builder.profile.QualityScore = firstPositiveScore(builder.profile.GeneralScore, builder.profile.CodingScore, builder.profile.PreferenceScore)
	}
	if builder.profile.GeneralScore == 0 {
		builder.profile.GeneralScore = firstPositiveScore(builder.profile.QualityScore, builder.profile.CodingScore, builder.profile.PreferenceScore)
	}
	if builder.profile.PreferenceScore == 0 {
		builder.profile.PreferenceScore = firstPositiveScore(builder.profile.GeneralScore, builder.profile.QualityScore)
	}
	return builder.profile
}

func averagedScore(total float64, count int) float64 {
	if count == 0 {
		return 0
	}
	return total / float64(count)
}

func firstPositiveScore(scores ...float64) float64 {
	for _, score := range scores {
		if score > 0 {
			return score
		}
	}
	return 0
}

func parseBenchmarkRows(rows []map[string]any, source string, keys benchmarkColumnKeys) []ExternalBenchmarkRecord {
	records := make([]ExternalBenchmarkRecord, 0, len(rows))
	for _, row := range rows {
		modelName := firstString(row, keys.Model...)
		if strings.TrimSpace(modelName) == "" {
			continue
		}
		records = append(records, ExternalBenchmarkRecord{
			Source:    source,
			ModelName: modelName,
			PassRate:  firstFloat(row, keys.Score...),
			Cost:      firstFloat(row, keys.Cost...),
			Seconds:   firstFloat(row, keys.Seconds...),
		})
	}
	return records
}

func parseBenchmarkTableRows(rows [][]string, source string, keys benchmarkColumnKeys) []ExternalBenchmarkRecord {
	if len(rows) < 2 {
		return nil
	}
	headers := make([]string, len(rows[0]))
	for i, header := range rows[0] {
		headers[i] = normalizeTableHeader(header)
	}
	records := make([]ExternalBenchmarkRecord, 0, len(rows)-1)
	for _, row := range rows[1:] {
		values := map[string]any{}
		for i, value := range row {
			if i >= len(headers) {
				break
			}
			values[headers[i]] = strings.TrimSpace(value)
		}
		records = append(records, parseBenchmarkRows([]map[string]any{values}, source, keys)...)
	}
	return records
}

func parseMarkdownTableRows(text string) [][]string {
	lines := strings.Split(text, "\n")
	rows := make([][]string, 0)
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "|") || !strings.HasSuffix(line, "|") {
			continue
		}
		trimmed := strings.Trim(line, "|")
		parts := strings.Split(trimmed, "|")
		row := make([]string, 0, len(parts))
		separator := true
		for _, part := range parts {
			cell := strings.TrimSpace(part)
			row = append(row, cell)
			if strings.Trim(cell, "-: ") != "" {
				separator = false
			}
		}
		if separator {
			continue
		}
		rows = append(rows, row)
	}
	return rows
}

func normalizeTableHeader(header string) string {
	normalized := strings.ToLower(strings.TrimSpace(header))
	normalized = strings.ReplaceAll(normalized, "%", "")
	normalized = strings.ReplaceAll(normalized, " ", "_")
	normalized = strings.ReplaceAll(normalized, "-", "_")
	return normalized
}

func firstString(row map[string]any, keys ...string) string {
	for _, key := range keys {
		if value, ok := row[key]; ok {
			return strings.TrimSpace(common.Interface2String(value))
		}
	}
	return ""
}

func firstFloat(row map[string]any, keys ...string) float64 {
	for _, key := range keys {
		value, ok := row[key]
		if !ok {
			continue
		}
		switch typed := value.(type) {
		case float64:
			return typed
		case float32:
			return float64(typed)
		case int:
			return float64(typed)
		case int64:
			return float64(typed)
		case string:
			parsed, err := strconv.ParseFloat(cleanNumericText(typed), 64)
			if err == nil {
				return parsed
			}
		}
	}
	return 0
}

func cleanNumericText(value string) string {
	value = strings.TrimSpace(value)
	value = strings.TrimSuffix(value, "%")
	value = strings.TrimPrefix(value, "$ ")
	value = strings.TrimPrefix(value, "$")
	return strings.ReplaceAll(value, ",", "")
}

func normalizedRatio(value float64, maxValue float64) float64 {
	if value <= 0 || maxValue <= 0 {
		return 0
	}
	return math.Min(value/maxValue, 1)
}

func inverseNormalizedRatio(value float64, maxValue float64) float64 {
	if value <= 0 || maxValue <= 0 {
		return 0
	}
	return math.Max(0, math.Min(1, 1-value/maxValue))
}
