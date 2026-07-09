package smartrouting

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"

	"github.com/QuantumNous/new-api/common"
)

const defaultAiderLeaderboardURL = "https://raw.githubusercontent.com/Aider-AI/aider/main/aider/website/_data/polyglot_leaderboard.yml"

type ExternalBenchmarkRefreshResult struct {
	SourceCount int `json:"source_count"`
	RecordCount int `json:"record_count"`
}

type ModelProfileRefreshResult struct {
	ProfileCount int `json:"profile_count"`
}

var externalBenchmarkCache = struct {
	sync.RWMutex
	Records   []ExternalBenchmarkRecord
	UpdatedAt int64
}{}

type externalBenchmarkAdapter struct {
	Source     string
	EnvURL     string
	DefaultURL string
	Parse      func([]byte) ([]ExternalBenchmarkRecord, error)
}

func RefreshExternalBenchmarks(ctx context.Context) (ExternalBenchmarkRefreshResult, error) {
	adapters := []externalBenchmarkAdapter{
		{
			Source:     BenchmarkSourceAider,
			EnvURL:     "SMART_ROUTING_AIDER_LEADERBOARD_URL",
			DefaultURL: defaultAiderLeaderboardURL,
			Parse:      ParseAiderLeaderboard,
		},
		{
			Source: BenchmarkSourceSWEBench,
			EnvURL: "SMART_ROUTING_SWE_BENCH_LEADERBOARD_URL",
			Parse:  ParseSWEBenchLeaderboard,
		},
		{
			Source: BenchmarkSourceArtificialAnalysis,
			EnvURL: "SMART_ROUTING_ARTIFICIAL_ANALYSIS_LEADERBOARD_URL",
			Parse:  ParseArtificialAnalysisLeaderboard,
		},
		{
			Source: BenchmarkSourceArena,
			EnvURL: "SMART_ROUTING_ARENA_LEADERBOARD_URL",
			Parse:  ParseArenaLeaderboard,
		},
	}
	records := make([]ExternalBenchmarkRecord, 0)
	sourceCount := 0
	for _, adapter := range adapters {
		url := strings.TrimSpace(common.GetEnvOrDefaultString(adapter.EnvURL, adapter.DefaultURL))
		if url == "" {
			continue
		}
		sourceRecords, err := fetchExternalBenchmarkRecords(ctx, adapter, url)
		if err != nil {
			return ExternalBenchmarkRefreshResult{}, err
		}
		records = append(records, sourceRecords...)
		sourceCount++
	}
	SetExternalBenchmarkRecords(records, common.GetTimestamp())
	return ExternalBenchmarkRefreshResult{SourceCount: sourceCount, RecordCount: len(records)}, nil
}

func fetchExternalBenchmarkRecords(ctx context.Context, adapter externalBenchmarkAdapter, url string) ([]ExternalBenchmarkRecord, error) {
	if url == "" {
		return nil, fmt.Errorf("%s is empty", adapter.EnvURL)
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return nil, fmt.Errorf("%s leaderboard returned status %d", adapter.Source, response.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, 4<<20))
	if err != nil {
		return nil, err
	}
	records, err := adapter.Parse(body)
	if err != nil {
		return nil, err
	}
	return records, nil
}

func RefreshModelRoutingProfiles(_ context.Context) (ModelProfileRefreshResult, error) {
	records, updatedAt := GetExternalBenchmarkRecords()
	profiles := BuildProfilesFromExternalBenchmarks(records, updatedAt)
	SetModelRoutingProfiles(profiles)
	return ModelProfileRefreshResult{ProfileCount: len(profiles)}, nil
}

func SetExternalBenchmarkRecords(records []ExternalBenchmarkRecord, updatedAt int64) {
	externalBenchmarkCache.Lock()
	defer externalBenchmarkCache.Unlock()
	externalBenchmarkCache.Records = append([]ExternalBenchmarkRecord(nil), records...)
	externalBenchmarkCache.UpdatedAt = updatedAt
}

func GetExternalBenchmarkRecords() ([]ExternalBenchmarkRecord, int64) {
	externalBenchmarkCache.RLock()
	defer externalBenchmarkCache.RUnlock()
	return append([]ExternalBenchmarkRecord(nil), externalBenchmarkCache.Records...), externalBenchmarkCache.UpdatedAt
}
