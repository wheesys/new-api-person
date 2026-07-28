package common

import (
	"sync"
	"time"
)

type UpstreamAttemptTiming struct {
	mutex           sync.Mutex
	channelID       int
	retryIndex      int
	startedAt       time.Time
	firstResponseAt time.Time
	finishedAt      time.Time
	outputTokens    int64
	outputTokensAt  time.Time
}

type relayTimingState struct {
	mutex           sync.RWMutex
	upstreamAttempt *UpstreamAttemptTiming
}

type UpstreamAttemptSample struct {
	ChannelID                 int
	RetryIndex                int
	Latency                   time.Duration
	HasLatency                bool
	TTFT                      time.Duration
	HasTTFT                   bool
	Generation                time.Duration
	HasGeneration             bool
	OutputTokens              int64
	HasOutputTokens           bool
	ThroughputTokensPerSecond float64
	HasThroughput             bool
}

func (info *RelayInfo) BeginUpstreamAttempt(channelID int, retryIndex int, startedAt time.Time) *UpstreamAttemptTiming {
	attempt := &UpstreamAttemptTiming{
		channelID:  channelID,
		retryIndex: retryIndex,
		startedAt:  startedAt,
	}
	timingState := info.ensureTimingState()
	timingState.mutex.Lock()
	timingState.upstreamAttempt = attempt
	timingState.mutex.Unlock()
	return attempt
}

func (info *RelayInfo) CurrentUpstreamAttempt() *UpstreamAttemptTiming {
	if info == nil {
		return nil
	}
	timingState := info.ensureTimingState()
	timingState.mutex.RLock()
	defer timingState.mutex.RUnlock()
	return timingState.upstreamAttempt
}

func (info *RelayInfo) SetFirstResponseTimeForAttempt(attempt *UpstreamAttemptTiming) {
	if info == nil {
		return
	}
	now := time.Now()
	timingState := info.ensureTimingState()
	timingState.mutex.Lock()
	if info.isFirstResponse {
		info.FirstResponseTime = now
		info.isFirstResponse = false
	}
	if attempt == nil {
		attempt = timingState.upstreamAttempt
	}
	timingState.mutex.Unlock()
	if attempt != nil {
		attempt.MarkFirstResponse(now)
	}
}

func (info *RelayInfo) MarkUpstreamAttemptFirstResponse(attempt *UpstreamAttemptTiming) {
	if attempt != nil {
		attempt.MarkFirstResponse(time.Now())
	}
}

func (attempt *UpstreamAttemptTiming) MarkOutputTokens(outputTokens int64, observedAt time.Time) {
	if attempt == nil || outputTokens <= 0 || observedAt.IsZero() {
		return
	}
	attempt.mutex.Lock()
	defer attempt.mutex.Unlock()
	if observedAt.Before(attempt.startedAt) {
		return
	}
	attempt.outputTokens = outputTokens
	attempt.outputTokensAt = observedAt
}

func (info *RelayInfo) RecordCurrentUpstreamAttemptOutputTokens(outputTokens int64, observedAt time.Time) {
	if info == nil {
		return
	}
	info.CurrentUpstreamAttempt().MarkOutputTokens(outputTokens, observedAt)
}

func (info *RelayInfo) FirstResponseTimeSnapshot() (time.Time, bool) {
	if info == nil {
		return time.Time{}, false
	}
	timingState := info.ensureTimingState()
	timingState.mutex.RLock()
	defer timingState.mutex.RUnlock()
	return info.FirstResponseTime, info.FirstResponseTime.After(info.StartTime)
}

func (info *RelayInfo) ensureTimingState() *relayTimingState {
	if info.timingState == nil {
		info.timingState = &relayTimingState{}
	}
	return info.timingState
}

func (attempt *UpstreamAttemptTiming) MarkFirstResponse(at time.Time) {
	if attempt == nil || at.IsZero() {
		return
	}
	attempt.mutex.Lock()
	defer attempt.mutex.Unlock()
	if attempt.firstResponseAt.IsZero() && !at.Before(attempt.startedAt) {
		attempt.firstResponseAt = at
	}
}

func (attempt *UpstreamAttemptTiming) Finish(at time.Time) UpstreamAttemptSample {
	if attempt == nil {
		return UpstreamAttemptSample{}
	}
	attempt.mutex.Lock()
	defer attempt.mutex.Unlock()
	if attempt.finishedAt.IsZero() && !at.IsZero() && !at.Before(attempt.startedAt) {
		attempt.finishedAt = at
	}
	return attempt.snapshotLocked(at)
}

func (attempt *UpstreamAttemptTiming) Snapshot(at time.Time) UpstreamAttemptSample {
	if attempt == nil {
		return UpstreamAttemptSample{}
	}
	attempt.mutex.Lock()
	defer attempt.mutex.Unlock()
	return attempt.snapshotLocked(at)
}

func (attempt *UpstreamAttemptTiming) snapshotLocked(at time.Time) UpstreamAttemptSample {
	sample := UpstreamAttemptSample{
		ChannelID:  attempt.channelID,
		RetryIndex: attempt.retryIndex,
	}
	completedAt := attempt.finishedAt
	if completedAt.IsZero() && !at.IsZero() && !at.Before(attempt.startedAt) {
		completedAt = at
	}
	if !completedAt.IsZero() {
		sample.Latency = completedAt.Sub(attempt.startedAt)
		sample.HasLatency = true
	}
	if !attempt.firstResponseAt.IsZero() {
		sample.TTFT = attempt.firstResponseAt.Sub(attempt.startedAt)
		sample.HasTTFT = true
		generationEnd := attempt.outputTokensAt
		if generationEnd.IsZero() {
			generationEnd = completedAt
		}
		if !generationEnd.IsZero() && !generationEnd.Before(attempt.firstResponseAt) {
			sample.Generation = generationEnd.Sub(attempt.firstResponseAt)
			sample.HasGeneration = true
		}
	}
	if attempt.outputTokens > 0 {
		sample.OutputTokens = attempt.outputTokens
		sample.HasOutputTokens = true
	}
	if sample.HasOutputTokens && !attempt.outputTokensAt.IsZero() && sample.OutputTokens >= 8 && sample.HasGeneration && sample.Generation >= 100*time.Millisecond {
		sample.ThroughputTokensPerSecond = float64(sample.OutputTokens) / sample.Generation.Seconds()
		sample.HasThroughput = sample.ThroughputTokensPerSecond > 0
	}
	return sample
}

func (sample UpstreamAttemptSample) ScoringLatency(isStream bool) (time.Duration, bool) {
	if isStream {
		return sample.TTFT, sample.HasTTFT && sample.TTFT > 0
	}
	return sample.Latency, sample.HasLatency && sample.Latency > 0
}
