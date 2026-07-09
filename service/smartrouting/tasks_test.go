package smartrouting

import (
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRefreshExternalBenchmarksCombinesConfiguredSources(t *testing.T) {
	t.Cleanup(func() { SetExternalBenchmarkRecords(nil, 0) })
	originalTransport := http.DefaultClient.Transport
	http.DefaultClient.Transport = benchmarkRoundTripper{
		"https://benchmarks.local/aider":               `[{"model":"aider-model","pass_rate":70}]`,
		"https://benchmarks.local/swe-bench":           `[{"model":"swe-model","resolved":60}]`,
		"https://benchmarks.local/artificial-analysis": "Model,Intelligence Index,Output Speed,Blended Price\naa-model,80,100,$1.00\n",
		"https://benchmarks.local/arena":               "| Model | Arena Score |\n| --- | ---: |\n| arena-model | 1200 |\n",
	}
	t.Cleanup(func() { http.DefaultClient.Transport = originalTransport })
	t.Setenv("SMART_ROUTING_AIDER_LEADERBOARD_URL", "https://benchmarks.local/aider")
	t.Setenv("SMART_ROUTING_SWE_BENCH_LEADERBOARD_URL", "https://benchmarks.local/swe-bench")
	t.Setenv("SMART_ROUTING_ARTIFICIAL_ANALYSIS_LEADERBOARD_URL", "https://benchmarks.local/artificial-analysis")
	t.Setenv("SMART_ROUTING_ARENA_LEADERBOARD_URL", "https://benchmarks.local/arena")

	result, err := RefreshExternalBenchmarks(t.Context())

	require.NoError(t, err)
	assert.Equal(t, 4, result.SourceCount)
	assert.Equal(t, 4, result.RecordCount)
	records, _ := GetExternalBenchmarkRecords()
	require.Len(t, records, 4)
	assert.ElementsMatch(t, []string{
		BenchmarkSourceAider,
		BenchmarkSourceSWEBench,
		BenchmarkSourceArtificialAnalysis,
		BenchmarkSourceArena,
	}, []string{records[0].Source, records[1].Source, records[2].Source, records[3].Source})
}

type benchmarkRoundTripper map[string]string

func (transport benchmarkRoundTripper) RoundTrip(request *http.Request) (*http.Response, error) {
	body, ok := transport[request.URL.String()]
	if !ok {
		return &http.Response{
			StatusCode: http.StatusNotFound,
			Body:       io.NopCloser(strings.NewReader("not found")),
			Header:     http.Header{},
			Request:    request,
		}, nil
	}
	return &http.Response{
		StatusCode: http.StatusOK,
		Body:       io.NopCloser(strings.NewReader(body)),
		Header:     http.Header{},
		Request:    request,
	}, nil
}
