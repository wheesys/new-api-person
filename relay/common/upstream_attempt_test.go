package common

import (
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestUpstreamAttemptStreamingUsesTTFT(t *testing.T) {
	startedAt := time.Unix(100, 0)
	info := &RelayInfo{IsStream: true, isFirstResponse: true}
	attempt := info.BeginUpstreamAttempt(10, 0, startedAt)
	attempt.MarkFirstResponse(startedAt.Add(250 * time.Millisecond))
	sample := attempt.Finish(startedAt.Add(3 * time.Second))

	latency, ok := sample.ScoringLatency(info.IsStream)
	require.True(t, ok)
	assert.Equal(t, 250*time.Millisecond, latency)
	assert.Equal(t, 3*time.Second, sample.Latency)
}

func TestUpstreamAttemptStreamingCalculatesGenerationAndThroughput(t *testing.T) {
	startedAt := time.Unix(150, 0)
	info := &RelayInfo{IsStream: true}
	attempt := info.BeginUpstreamAttempt(15, 0, startedAt)
	attempt.MarkFirstResponse(startedAt.Add(250 * time.Millisecond))
	attempt.MarkOutputTokens(100, startedAt.Add(1250*time.Millisecond))
	sample := attempt.Finish(startedAt.Add(1250 * time.Millisecond))

	require.True(t, sample.HasGeneration)
	assert.Equal(t, time.Second, sample.Generation)
	assert.Equal(t, int64(100), sample.OutputTokens)
	assert.InDelta(t, 100.0, sample.ThroughputTokensPerSecond, 0.000001)
	assert.True(t, sample.HasThroughput)
}

func TestUpstreamAttemptRejectsTooShortThroughputSample(t *testing.T) {
	startedAt := time.Unix(160, 0)
	attempt := (&RelayInfo{IsStream: true}).BeginUpstreamAttempt(16, 0, startedAt)
	attempt.MarkFirstResponse(startedAt.Add(250 * time.Millisecond))
	attempt.MarkOutputTokens(100, startedAt.Add(300*time.Millisecond))
	sample := attempt.Finish(startedAt.Add(300 * time.Millisecond))

	assert.False(t, sample.HasThroughput)
	assert.Zero(t, sample.ThroughputTokensPerSecond)
}

func TestUpstreamAttemptStreamingWithoutFirstResponseHasNoScore(t *testing.T) {
	startedAt := time.Unix(200, 0)
	attempt := (&RelayInfo{IsStream: true}).BeginUpstreamAttempt(20, 0, startedAt)
	sample := attempt.Finish(startedAt.Add(time.Second))

	latency, ok := sample.ScoringLatency(true)
	assert.False(t, ok)
	assert.Zero(t, latency)
}

func TestUpstreamAttemptZeroDurationIsNotAValidScore(t *testing.T) {
	streamLatency, streamOK := (UpstreamAttemptSample{HasTTFT: true}).ScoringLatency(true)
	nonStreamLatency, nonStreamOK := (UpstreamAttemptSample{HasLatency: true}).ScoringLatency(false)

	assert.False(t, streamOK)
	assert.Zero(t, streamLatency)
	assert.False(t, nonStreamOK)
	assert.Zero(t, nonStreamLatency)
}

func TestUpstreamAttemptNonStreamingUsesAttemptLatency(t *testing.T) {
	startedAt := time.Unix(300, 0)
	attempt := (&RelayInfo{}).BeginUpstreamAttempt(30, 1, startedAt)
	sample := attempt.Finish(startedAt.Add(1750 * time.Millisecond))

	latency, ok := sample.ScoringLatency(false)
	require.True(t, ok)
	assert.Equal(t, 1750*time.Millisecond, latency)
	assert.Equal(t, 30, sample.ChannelID)
	assert.Equal(t, 1, sample.RetryIndex)
}

func TestUpstreamAttemptsAreIsolatedAcrossRetries(t *testing.T) {
	info := &RelayInfo{IsStream: true}
	firstStartedAt := time.Unix(400, 0)
	firstAttempt := info.BeginUpstreamAttempt(40, 0, firstStartedAt)
	firstAttempt.MarkFirstResponse(firstStartedAt.Add(2 * time.Second))
	firstAttempt.MarkOutputTokens(20, firstStartedAt.Add(4*time.Second))

	secondStartedAt := time.Unix(500, 0)
	secondAttempt := info.BeginUpstreamAttempt(41, 1, secondStartedAt)
	secondAttempt.MarkFirstResponse(secondStartedAt.Add(100 * time.Millisecond))
	info.RecordCurrentUpstreamAttemptOutputTokens(30, secondStartedAt.Add(time.Second))

	firstSample := firstAttempt.Finish(firstStartedAt.Add(4 * time.Second))
	secondSample := secondAttempt.Finish(secondStartedAt.Add(time.Second))
	assert.Equal(t, 2*time.Second, firstSample.TTFT)
	assert.Equal(t, 100*time.Millisecond, secondSample.TTFT)
	assert.Equal(t, int64(20), firstSample.OutputTokens)
	assert.Equal(t, int64(30), secondSample.OutputTokens)
}

func TestUpstreamAttemptFirstResponseIsFirstWriteWins(t *testing.T) {
	startedAt := time.Unix(600, 0)
	attempt := (&RelayInfo{}).BeginUpstreamAttempt(50, 0, startedAt)
	timestamps := []time.Time{
		startedAt.Add(500 * time.Millisecond),
		startedAt.Add(100 * time.Millisecond),
		startedAt.Add(300 * time.Millisecond),
	}
	var waitGroup sync.WaitGroup
	for _, timestamp := range timestamps {
		waitGroup.Add(1)
		go func(at time.Time) {
			defer waitGroup.Done()
			attempt.MarkFirstResponse(at)
		}(timestamp)
	}
	waitGroup.Wait()

	sample := attempt.Finish(startedAt.Add(time.Second))
	assert.Contains(t, []time.Duration{100 * time.Millisecond, 300 * time.Millisecond, 500 * time.Millisecond}, sample.TTFT)
	assert.True(t, sample.HasTTFT)
}

func TestRelayInfoRequestFirstResponseIsNotOverwrittenByRetry(t *testing.T) {
	info := &RelayInfo{isFirstResponse: true}
	firstAttempt := info.BeginUpstreamAttempt(60, 0, time.Now().Add(-time.Second))
	info.SetFirstResponseTimeForAttempt(firstAttempt)
	requestFirstResponse, hasFirstResponse := info.FirstResponseTimeSnapshot()
	require.True(t, hasFirstResponse)

	secondAttempt := info.BeginUpstreamAttempt(61, 1, time.Now().Add(-500*time.Millisecond))
	info.SetFirstResponseTimeForAttempt(secondAttempt)

	currentFirstResponse, hasFirstResponse := info.FirstResponseTimeSnapshot()
	require.True(t, hasFirstResponse)
	assert.Equal(t, requestFirstResponse, currentFirstResponse)
	assert.True(t, secondAttempt.Finish(time.Now()).HasTTFT)
}

func TestFailedAttemptBodyDoesNotPolluteRequestFirstResponse(t *testing.T) {
	requestStartedAt := time.Now().Add(-2 * time.Second)
	info := &RelayInfo{
		StartTime:       requestStartedAt,
		isFirstResponse: true,
	}
	failedAttempt := info.BeginUpstreamAttempt(70, 0, requestStartedAt.Add(100*time.Millisecond))
	info.MarkUpstreamAttemptFirstResponse(failedAttempt)

	_, hasFirstResponse := info.FirstResponseTimeSnapshot()
	assert.False(t, hasFirstResponse)
	assert.True(t, failedAttempt.Finish(time.Now()).HasTTFT)

	successfulAttempt := info.BeginUpstreamAttempt(71, 1, time.Now().Add(-100*time.Millisecond))
	info.SetFirstResponseTimeForAttempt(successfulAttempt)
	requestFirstResponse, hasFirstResponse := info.FirstResponseTimeSnapshot()
	require.True(t, hasFirstResponse)
	assert.True(t, requestFirstResponse.After(requestStartedAt))
	assert.True(t, successfulAttempt.Finish(time.Now()).HasTTFT)
}
