package smartrouting

import (
	"math"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRuntimeHealthTrackerTransitionsThroughCircuitStates(t *testing.T) {
	currentTime := time.Date(2026, time.July, 28, 12, 0, 0, 0, time.UTC)
	tracker := newRuntimeHealthTracker(func() time.Time { return currentTime })

	initial := tracker.Snapshot(11, "gpt-5")
	assert.Equal(t, ChannelHealthHealthy, initial.State)
	assert.Equal(t, 1.0, initial.Reliability)
	assert.Equal(t, 1.0, initial.ResetWindowScore)

	firstFailure := tracker.RecordFailure(11, "gpt-5")
	assert.Equal(t, ChannelHealthDegraded, firstFailure.State)
	assert.InDelta(t, 0.8, firstFailure.SuccessEWMA, 0.000001)
	assert.InDelta(t, 0.2, firstFailure.FailureEWMA, 0.000001)

	tracker.RecordFailure(11, "gpt-5")
	opened := tracker.RecordFailure(11, "gpt-5")
	assert.Equal(t, ChannelHealthOpen, opened.State)
	assert.Equal(t, 3, opened.ConsecutiveFailures)
	assert.Equal(t, 5*time.Second, opened.Cooldown)
	assert.Equal(t, currentTime.Add(5*time.Second), opened.OpenUntil)
	assert.Zero(t, opened.ResetWindowScore)

	currentTime = currentTime.Add(2500 * time.Millisecond)
	halfway := tracker.Snapshot(11, "gpt-5")
	assert.Equal(t, ChannelHealthOpen, halfway.State)
	assert.InDelta(t, 0.5, halfway.ResetWindowScore, 0.000001)

	currentTime = currentTime.Add(2500 * time.Millisecond)
	halfOpen := tracker.Snapshot(11, "gpt-5")
	assert.Equal(t, ChannelHealthHalfOpen, halfOpen.State)
	assert.Equal(t, 0.5, halfOpen.ResetWindowScore)
	assert.Equal(t, ChannelHealthHalfOpen, tracker.Snapshot(11, "gpt-5").State)
	assert.True(t, tracker.TryAcquireProbe(11, "gpt-5"))
	assert.False(t, tracker.TryAcquireProbe(11, "gpt-5"))

	recovered := tracker.RecordSuccess(11, "gpt-5")
	assert.Equal(t, ChannelHealthDegraded, recovered.State)
	assert.Zero(t, recovered.ConsecutiveFailures)
	assert.Zero(t, recovered.Cooldown)
	assert.True(t, recovered.OpenUntil.IsZero())

	healthy := tracker.RecordSuccess(11, "gpt-5")
	assert.Equal(t, ChannelHealthDegraded, healthy.State)
	healthy = tracker.RecordSuccess(11, "gpt-5")
	assert.Equal(t, ChannelHealthDegraded, healthy.State)
	healthy = tracker.RecordSuccess(11, "gpt-5")
	assert.Equal(t, ChannelHealthHealthy, healthy.State)
}

func TestRuntimeHealthTrackerRecordsObservedLatencyAndPrunesStaleEntries(t *testing.T) {
	currentTime := time.Date(2026, time.July, 28, 12, 0, 0, 0, time.UTC)
	tracker := newRuntimeHealthTracker(func() time.Time { return currentTime })

	tracker.RecordSuccessWithLatency(61, "latency-model", 250*time.Millisecond)
	snapshot := tracker.Snapshot(61, "latency-model")
	assert.Equal(t, 1, snapshot.LatencySampleCount)
	assert.InDelta(t, 0.8, snapshot.LatencyScore, 0.000001)
	require.Len(t, tracker.entries, 1)

	currentTime = currentTime.Add(runtimeHealthEntryTTL + time.Minute)
	snapshot = tracker.Snapshot(62, "another-model")
	assert.Equal(t, ChannelHealthHealthy, snapshot.State)
	assert.Empty(t, tracker.entries)
}

func TestRuntimeHealthTrackerRecordsThroughputWithOneSuccessObservation(t *testing.T) {
	tracker := NewRuntimeHealthTracker()

	tracker.RecordFailure(63, "throughput-model")
	snapshot := tracker.RecordSuccessWithMetrics(63, "throughput-model", 500*time.Millisecond, 20)
	assert.InDelta(t, 0.84, snapshot.SuccessEWMA, 0.000001)
	assert.Equal(t, 1, snapshot.LatencySampleCount)
	assert.Equal(t, 1, snapshot.ThroughputSampleCount)
	assert.Equal(t, 20.0, snapshot.ThroughputTokensPerSecond)

	tracker.RecordSuccessWithMetrics(63, "throughput-model", time.Second, 40)
	snapshot = tracker.Snapshot(63, "throughput-model")
	assert.Equal(t, 2, snapshot.ThroughputSampleCount)
	assert.InDelta(t, 24.0, snapshot.ThroughputTokensPerSecond, 0.000001)
}

func TestRuntimeHealthTrackerRejectsInvalidThroughput(t *testing.T) {
	tracker := NewRuntimeHealthTracker()

	for _, value := range []float64{0, -1, math.NaN(), math.Inf(1)} {
		snapshot := tracker.RecordSuccessWithMetrics(64, "invalid-throughput", time.Second, value)
		assert.Equal(t, 0, snapshot.ThroughputSampleCount)
	}
	assert.Equal(t, 4, tracker.Snapshot(64, "invalid-throughput").LatencySampleCount)
}

func TestRuntimeHealthTrackerDoesNotCreateEntryForUnobservedLatency(t *testing.T) {
	tracker := NewRuntimeHealthTracker()

	snapshot := tracker.RecordSuccess(71, "success-without-latency")

	assert.Equal(t, ChannelHealthHealthy, snapshot.State)
	assert.Empty(t, tracker.entries)
}

func TestRuntimeHealthTrackerUsesExponentialCooldownWithMaximum(t *testing.T) {
	currentTime := time.Date(2026, time.July, 28, 12, 0, 0, 0, time.UTC)
	tracker := newRuntimeHealthTracker(func() time.Time { return currentTime })

	tracker.RecordFailure(22, "claude-sonnet")
	tracker.RecordFailure(22, "claude-sonnet")
	snapshot := tracker.RecordFailure(22, "claude-sonnet")

	expectedCooldowns := []time.Duration{
		5 * time.Second,
		10 * time.Second,
		20 * time.Second,
		40 * time.Second,
		80 * time.Second,
		160 * time.Second,
		5 * time.Minute,
		5 * time.Minute,
	}
	for index, expectedCooldown := range expectedCooldowns {
		assert.Equal(t, ChannelHealthOpen, snapshot.State)
		assert.Equal(t, expectedCooldown, snapshot.Cooldown)
		if index == len(expectedCooldowns)-1 {
			break
		}
		currentTime = snapshot.OpenUntil
		require.Equal(t, ChannelHealthHalfOpen, tracker.Snapshot(22, "claude-sonnet").State)
		snapshot = tracker.RecordFailure(22, "claude-sonnet")
	}
}

func TestRuntimeHealthTrackerKeepsChannelAndModelStatesIndependent(t *testing.T) {
	tracker := NewRuntimeHealthTracker()

	tracker.RecordFailure(31, "model-a")
	tracker.RecordFailure(31, "model-a")
	tracker.RecordFailure(31, "model-a")

	assert.Equal(t, ChannelHealthOpen, tracker.Snapshot(31, "model-a").State)
	assert.Equal(t, ChannelHealthHealthy, tracker.Snapshot(31, "model-b").State)
	assert.Equal(t, ChannelHealthHealthy, tracker.Snapshot(32, "model-a").State)
}

func TestRuntimeHealthSnapshotDoesNotExposeMutableState(t *testing.T) {
	tracker := NewRuntimeHealthTracker()
	snapshot := tracker.RecordFailure(41, " model-a ")

	snapshot.State = ChannelHealthHealthy
	snapshot.Reliability = 1
	snapshot.ConsecutiveFailures = 0

	actual := tracker.Snapshot(41, "model-a")
	assert.Equal(t, ChannelHealthDegraded, actual.State)
	assert.InDelta(t, 0.8, actual.Reliability, 0.000001)
	assert.Equal(t, 1, actual.ConsecutiveFailures)
}

func TestRuntimeHealthTrackerIsConcurrentSafe(t *testing.T) {
	fixedTime := time.Date(2026, time.July, 28, 12, 0, 0, 0, time.UTC)
	tracker := newRuntimeHealthTracker(func() time.Time { return fixedTime })

	var waitGroup sync.WaitGroup
	for index := 0; index < 100; index++ {
		waitGroup.Add(1)
		go func(succeeded bool) {
			defer waitGroup.Done()
			if succeeded {
				tracker.RecordSuccess(51, "concurrent-model")
				return
			}
			tracker.RecordFailure(51, "concurrent-model")
		}(index%2 == 0)
	}
	waitGroup.Wait()

	snapshot := tracker.Snapshot(51, "concurrent-model")
	assert.GreaterOrEqual(t, snapshot.Reliability, 0.0)
	assert.LessOrEqual(t, snapshot.Reliability, 1.0)
	assert.InDelta(t, 1.0, snapshot.SuccessEWMA+snapshot.FailureEWMA, 0.000001)
}

func TestRuntimeHealthTrackerRecordsThroughputConcurrently(t *testing.T) {
	tracker := NewRuntimeHealthTracker()
	var waitGroup sync.WaitGroup
	for index := 0; index < 100; index++ {
		waitGroup.Add(1)
		go func(tokensPerSecond float64) {
			defer waitGroup.Done()
			tracker.RecordSuccessWithMetrics(52, "concurrent-throughput", 250*time.Millisecond, tokensPerSecond)
		}(float64(index + 1))
	}
	waitGroup.Wait()

	snapshot := tracker.Snapshot(52, "concurrent-throughput")
	assert.Equal(t, 100, snapshot.ThroughputSampleCount)
	assert.True(t, validThroughput(snapshot.ThroughputTokensPerSecond))
}
