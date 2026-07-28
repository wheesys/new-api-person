package smartrouting

import (
	"math"
	"strings"
	"sync"
	"time"
)

const (
	runtimeHealthEWMAAlpha          = 0.2
	runtimeHealthFailureThreshold   = 3
	runtimeHealthHealthyReliability = 0.8
	runtimeHealthInitialCooldown    = 5 * time.Second
	runtimeHealthMaximumCooldown    = 5 * time.Minute
	runtimeHealthHalfOpenProbeLease = 5 * time.Second
	runtimeHealthEntryTTL           = 15 * time.Minute
	runtimeHealthCleanupInterval    = time.Minute
)

type ChannelHealthState string

const (
	ChannelHealthHealthy  ChannelHealthState = "healthy"
	ChannelHealthDegraded ChannelHealthState = "degraded"
	ChannelHealthOpen     ChannelHealthState = "open"
	ChannelHealthHalfOpen ChannelHealthState = "half_open"
)

// RuntimeHealthSnapshot is an immutable view of one channel and model pair.
type RuntimeHealthSnapshot struct {
	ChannelID                 int
	ModelName                 string
	State                     ChannelHealthState
	Reliability               float64
	SuccessEWMA               float64
	FailureEWMA               float64
	ResetWindowScore          float64
	LatencyScore              float64
	LatencySampleCount        int
	ThroughputTokensPerSecond float64
	ThroughputSampleCount     int
	ConsecutiveFailures       int
	Cooldown                  time.Duration
	OpenUntil                 time.Time
}

type runtimeHealthKey struct {
	channelID int
	modelName string
}

type runtimeHealthEntry struct {
	state                 ChannelHealthState
	successEWMA           float64
	failureEWMA           float64
	consecutiveFailures   int
	cooldown              time.Duration
	openedAt              time.Time
	openUntil             time.Time
	probeInFlight         bool
	probeUntil            time.Time
	latencyEWMASeconds    float64
	latencySampleCount    int
	throughputEWMA        float64
	throughputSampleCount int
	lastObservedAt        time.Time
}

// RuntimeHealthTracker maintains concurrent in-process health observations.
type RuntimeHealthTracker struct {
	mutex       sync.Mutex
	entries     map[runtimeHealthKey]*runtimeHealthEntry
	now         func() time.Time
	lastCleanup time.Time
}

// NewRuntimeHealthTracker creates an empty tracker whose unseen entries are healthy.
func NewRuntimeHealthTracker() *RuntimeHealthTracker {
	return newRuntimeHealthTracker(time.Now)
}

func newRuntimeHealthTracker(now func() time.Time) *RuntimeHealthTracker {
	return &RuntimeHealthTracker{
		entries: make(map[runtimeHealthKey]*runtimeHealthEntry),
		now:     now,
	}
}

func (tracker *RuntimeHealthTracker) Clear() {
	tracker.mutex.Lock()
	defer tracker.mutex.Unlock()
	tracker.entries = make(map[runtimeHealthKey]*runtimeHealthEntry)
	tracker.lastCleanup = time.Time{}
}

// Snapshot returns a health view and advances expired open entries to half-open without acquiring a probe lease.
func (tracker *RuntimeHealthTracker) Snapshot(channelID int, modelName string) RuntimeHealthSnapshot {
	key := runtimeHealthKey{channelID: channelID, modelName: strings.TrimSpace(modelName)}
	tracker.mutex.Lock()
	defer tracker.mutex.Unlock()

	now := tracker.now()
	tracker.prune(now)
	entry, ok := tracker.entries[key]
	if !ok {
		return RuntimeHealthSnapshot{
			ChannelID:        channelID,
			ModelName:        key.modelName,
			State:            ChannelHealthHealthy,
			Reliability:      1,
			SuccessEWMA:      1,
			ResetWindowScore: 1,
			LatencyScore:     0.5,
		}
	}
	entry.advanceCooldown(now)
	entry.lastObservedAt = now
	return entry.snapshot(key, now)
}

func (tracker *RuntimeHealthTracker) TryAcquireProbe(channelID int, modelName string) bool {
	key := runtimeHealthKey{channelID: channelID, modelName: strings.TrimSpace(modelName)}
	tracker.mutex.Lock()
	defer tracker.mutex.Unlock()

	now := tracker.now()
	tracker.prune(now)
	entry, ok := tracker.entries[key]
	if !ok {
		return true
	}
	entry.advanceCooldown(now)
	entry.lastObservedAt = now
	if entry.state == ChannelHealthOpen {
		return false
	}
	if entry.state != ChannelHealthHalfOpen {
		return true
	}
	if entry.probeInFlight && now.Before(entry.probeUntil) {
		return false
	}
	entry.probeInFlight = true
	entry.probeUntil = now.Add(runtimeHealthHalfOpenProbeLease)
	return true
}

// RecordSuccess updates EWMA reliability and closes a half-open circuit.
func (tracker *RuntimeHealthTracker) RecordSuccess(channelID int, modelName string) RuntimeHealthSnapshot {
	return tracker.recordSuccessWithMetrics(channelID, modelName, 0, 0)
}

func (tracker *RuntimeHealthTracker) RecordSuccessWithLatency(channelID int, modelName string, latency time.Duration) RuntimeHealthSnapshot {
	return tracker.recordSuccessWithMetrics(channelID, modelName, latency, 0)
}

func (tracker *RuntimeHealthTracker) RecordSuccessWithMetrics(channelID int, modelName string, latency time.Duration, tokensPerSecond float64) RuntimeHealthSnapshot {
	return tracker.recordSuccessWithMetrics(channelID, modelName, latency, tokensPerSecond)
}

func (tracker *RuntimeHealthTracker) recordSuccessWithMetrics(channelID int, modelName string, latency time.Duration, tokensPerSecond float64) RuntimeHealthSnapshot {
	key := runtimeHealthKey{channelID: channelID, modelName: strings.TrimSpace(modelName)}
	tracker.mutex.Lock()
	defer tracker.mutex.Unlock()

	now := tracker.now()
	tracker.prune(now)
	entry, ok := tracker.entries[key]
	if !ok {
		if latency <= 0 && !validThroughput(tokensPerSecond) {
			return RuntimeHealthSnapshot{
				ChannelID:        channelID,
				ModelName:        key.modelName,
				State:            ChannelHealthHealthy,
				Reliability:      1,
				SuccessEWMA:      1,
				ResetWindowScore: 1,
				LatencyScore:     0.5,
			}
		}
		entry = tracker.entry(key)
	}
	entry.advanceCooldown(now)
	entry.lastObservedAt = now
	entry.successEWMA = updateRuntimeHealthEWMA(entry.successEWMA, 1)
	entry.failureEWMA = updateRuntimeHealthEWMA(entry.failureEWMA, 0)
	if latency > 0 {
		latencySeconds := latency.Seconds()
		if entry.latencySampleCount == 0 {
			entry.latencyEWMASeconds = latencySeconds
		} else {
			entry.latencyEWMASeconds = updateRuntimeHealthEWMA(entry.latencyEWMASeconds, latencySeconds)
		}
		entry.latencySampleCount++
	}
	if validThroughput(tokensPerSecond) {
		if entry.throughputSampleCount == 0 {
			entry.throughputEWMA = tokensPerSecond
		} else {
			entry.throughputEWMA = updateRuntimeHealthEWMA(entry.throughputEWMA, tokensPerSecond)
		}
		entry.throughputSampleCount++
	}

	if entry.state != ChannelHealthOpen {
		entry.consecutiveFailures = 0
		entry.openedAt = time.Time{}
		entry.openUntil = time.Time{}
		entry.probeInFlight = false
		entry.probeUntil = time.Time{}
		if entry.state == ChannelHealthHalfOpen {
			entry.cooldown = 0
		}
		entry.state = stateForReliability(entry.successEWMA)
	}

	return entry.snapshot(key, now)
}

// RecordFailure updates EWMA reliability and opens a circuit after three consecutive failures.
func (tracker *RuntimeHealthTracker) RecordFailure(channelID int, modelName string) RuntimeHealthSnapshot {
	key := runtimeHealthKey{channelID: channelID, modelName: strings.TrimSpace(modelName)}
	tracker.mutex.Lock()
	defer tracker.mutex.Unlock()

	now := tracker.now()
	tracker.prune(now)
	entry := tracker.entry(key)
	entry.advanceCooldown(now)
	entry.lastObservedAt = now
	entry.successEWMA = updateRuntimeHealthEWMA(entry.successEWMA, 0)
	entry.failureEWMA = updateRuntimeHealthEWMA(entry.failureEWMA, 1)
	entry.consecutiveFailures++

	switch {
	case entry.state == ChannelHealthHalfOpen:
		entry.open(now)
	case entry.state == ChannelHealthOpen:
		// In-flight failures recorded after isolation must not extend the reset window.
	case entry.consecutiveFailures >= runtimeHealthFailureThreshold:
		entry.open(now)
	default:
		entry.state = ChannelHealthDegraded
	}

	return entry.snapshot(key, now)
}

func (tracker *RuntimeHealthTracker) entry(key runtimeHealthKey) *runtimeHealthEntry {
	entry, ok := tracker.entries[key]
	if ok {
		return entry
	}
	entry = &runtimeHealthEntry{
		state:          ChannelHealthHealthy,
		successEWMA:    1,
		lastObservedAt: tracker.now(),
	}
	tracker.entries[key] = entry
	return entry
}

func (tracker *RuntimeHealthTracker) prune(now time.Time) {
	if !tracker.lastCleanup.IsZero() && now.Sub(tracker.lastCleanup) < runtimeHealthCleanupInterval {
		return
	}
	for key, entry := range tracker.entries {
		if !entry.lastObservedAt.IsZero() && now.Sub(entry.lastObservedAt) >= runtimeHealthEntryTTL {
			delete(tracker.entries, key)
		}
	}
	tracker.lastCleanup = now
}

func (entry *runtimeHealthEntry) advanceCooldown(now time.Time) {
	if entry.state == ChannelHealthOpen && !now.Before(entry.openUntil) {
		entry.state = ChannelHealthHalfOpen
		entry.probeInFlight = false
		entry.probeUntil = time.Time{}
	}
}

func (entry *runtimeHealthEntry) open(now time.Time) {
	if entry.cooldown == 0 {
		entry.cooldown = runtimeHealthInitialCooldown
	} else {
		entry.cooldown *= 2
		if entry.cooldown > runtimeHealthMaximumCooldown {
			entry.cooldown = runtimeHealthMaximumCooldown
		}
	}
	entry.state = ChannelHealthOpen
	entry.openedAt = now
	entry.openUntil = now.Add(entry.cooldown)
	entry.probeInFlight = false
	entry.probeUntil = time.Time{}
}

func (entry *runtimeHealthEntry) snapshot(key runtimeHealthKey, now time.Time) RuntimeHealthSnapshot {
	return RuntimeHealthSnapshot{
		ChannelID:                 key.channelID,
		ModelName:                 key.modelName,
		State:                     entry.state,
		Reliability:               entry.successEWMA,
		SuccessEWMA:               entry.successEWMA,
		FailureEWMA:               entry.failureEWMA,
		ResetWindowScore:          entry.resetWindowScore(now),
		LatencyScore:              latencyFactor(time.Duration(entry.latencyEWMASeconds * float64(time.Second))),
		LatencySampleCount:        entry.latencySampleCount,
		ThroughputTokensPerSecond: entry.throughputEWMA,
		ThroughputSampleCount:     entry.throughputSampleCount,
		ConsecutiveFailures:       entry.consecutiveFailures,
		Cooldown:                  entry.cooldown,
		OpenUntil:                 entry.openUntil,
	}
}

func latencyFactor(latency time.Duration) float64 {
	if latency <= 0 {
		return 0.5
	}
	return normalizeScore(1 / (1 + latency.Seconds()))
}

func validThroughput(tokensPerSecond float64) bool {
	return tokensPerSecond > 0 && !math.IsNaN(tokensPerSecond) && !math.IsInf(tokensPerSecond, 0)
}

func (entry *runtimeHealthEntry) resetWindowScore(now time.Time) float64 {
	if entry.state == ChannelHealthHalfOpen {
		return 0.5
	}
	if entry.state != ChannelHealthOpen {
		return 1
	}
	if entry.cooldown <= 0 || !now.Before(entry.openUntil) {
		return 1
	}
	elapsed := now.Sub(entry.openedAt)
	if elapsed <= 0 {
		return 0
	}
	return normalizeScore(float64(elapsed) / float64(entry.cooldown))
}

func updateRuntimeHealthEWMA(current float64, observation float64) float64 {
	return current*(1-runtimeHealthEWMAAlpha) + observation*runtimeHealthEWMAAlpha
}

func stateForReliability(reliability float64) ChannelHealthState {
	if reliability < runtimeHealthHealthyReliability {
		return ChannelHealthDegraded
	}
	return ChannelHealthHealthy
}

var defaultRuntimeHealthTracker = NewRuntimeHealthTracker()

// GetRuntimeHealthSnapshot returns the current process-wide health view.
func GetRuntimeHealthSnapshot(channelID int, modelName string) RuntimeHealthSnapshot {
	return defaultRuntimeHealthTracker.Snapshot(channelID, modelName)
}

// RecordRuntimeHealthSuccess records a successful process-wide observation.
func RecordRuntimeHealthSuccess(channelID int, modelName string) RuntimeHealthSnapshot {
	return defaultRuntimeHealthTracker.RecordSuccess(channelID, modelName)
}

func RecordRuntimeHealthSuccessWithLatency(channelID int, modelName string, latency time.Duration) RuntimeHealthSnapshot {
	return defaultRuntimeHealthTracker.RecordSuccessWithLatency(channelID, modelName, latency)
}

func RecordRuntimeHealthSuccessWithMetrics(channelID int, modelName string, latency time.Duration, tokensPerSecond float64) RuntimeHealthSnapshot {
	return defaultRuntimeHealthTracker.RecordSuccessWithMetrics(channelID, modelName, latency, tokensPerSecond)
}

// RecordRuntimeHealthFailure records a failed process-wide observation.
func RecordRuntimeHealthFailure(channelID int, modelName string) RuntimeHealthSnapshot {
	return defaultRuntimeHealthTracker.RecordFailure(channelID, modelName)
}

func TryAcquireRuntimeHealthProbe(channelID int, modelName string) bool {
	return defaultRuntimeHealthTracker.TryAcquireProbe(channelID, modelName)
}

func ClearRuntimeHealth() {
	defaultRuntimeHealthTracker.Clear()
}
