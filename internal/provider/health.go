package provider

import (
	"sort"
	"sync"
	"time"
)

const (
	HealthAvailable = "available"
	HealthDegraded  = "degraded"
	HealthProbing   = "probing"

	DefaultHealthFailureThreshold = 3
	DefaultHealthProbeInterval    = time.Minute
)

type HealthDecision struct {
	Allowed      bool
	Reason       string
	Probe        bool
	NextProbeAt  time.Time
	RetryAfter   time.Time
	FailureCount int
}

type HealthTracker struct {
	mu            sync.Mutex
	now           func() time.Time
	probeInterval time.Duration
	outbounds     map[string]*healthState
}

type healthState struct {
	name                string
	lastSuccessAt       time.Time
	lastFailureAt       time.Time
	lastErrorKind       ErrorKind
	consecutiveFailures int
	nextProbeAt         time.Time
	probeInFlight       bool
}

type HealthSnapshotItem struct {
	Outbound            string `json:"outbound"`
	State               string `json:"state"`
	LastSuccessAt       string `json:"last_success_at"`
	LastFailureAt       string `json:"last_failure_at"`
	LastErrorKind       string `json:"last_error_kind,omitempty"`
	ConsecutiveFailures int    `json:"consecutive_failures"`
	NextProbeAt         string `json:"next_probe_at,omitempty"`
}

func NewHealthTracker(outboundNames []string) *HealthTracker {
	return NewHealthTrackerWithProbeInterval(outboundNames, DefaultHealthProbeInterval)
}

func NewHealthTrackerWithProbeInterval(outboundNames []string, probeInterval time.Duration) *HealthTracker {
	return newHealthTracker(outboundNames, time.Now, probeInterval)
}

func NewTestHealthTracker(outboundNames []string, now func() time.Time) *HealthTracker {
	return NewTestHealthTrackerWithProbeInterval(outboundNames, now, DefaultHealthProbeInterval)
}

func NewTestHealthTrackerWithProbeInterval(outboundNames []string, now func() time.Time, probeInterval time.Duration) *HealthTracker {
	return newHealthTracker(outboundNames, now, probeInterval)
}

func newHealthTracker(outboundNames []string, now func() time.Time, probeInterval time.Duration) *HealthTracker {
	if len(outboundNames) == 0 {
		return nil
	}
	if now == nil {
		now = time.Now
	}
	if probeInterval <= 0 {
		probeInterval = DefaultHealthProbeInterval
	}
	tracker := &HealthTracker{now: now, probeInterval: probeInterval, outbounds: make(map[string]*healthState, len(outboundNames))}
	for _, name := range outboundNames {
		tracker.outbounds[name] = &healthState{name: name}
	}
	return tracker
}

func (t *HealthTracker) BeforeAttempt(outbound string) HealthDecision {
	if t == nil {
		return HealthDecision{Allowed: true}
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	state := t.outbounds[outbound]
	if state == nil {
		return HealthDecision{Allowed: true}
	}
	if state.consecutiveFailures < DefaultHealthFailureThreshold {
		return HealthDecision{Allowed: true}
	}
	if state.probeInFlight {
		return state.blockDecision()
	}
	if now := t.now().UTC(); state.nextProbeAt.IsZero() || !now.Before(state.nextProbeAt) {
		state.probeInFlight = true
		return HealthDecision{Allowed: true, Reason: HealthProbing, Probe: true, NextProbeAt: state.nextProbeAt, FailureCount: state.consecutiveFailures}
	}
	return state.blockDecision()
}

func (t *HealthTracker) RecordSuccess(outbound string) {
	if t == nil {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	state := t.outbounds[outbound]
	if state == nil {
		return
	}
	state.lastSuccessAt = t.now().UTC()
	state.lastErrorKind = ""
	state.consecutiveFailures = 0
	state.nextProbeAt = time.Time{}
	state.probeInFlight = false
}

func (t *HealthTracker) RecordFailure(outbound string, kind ErrorKind) {
	if t == nil || !isRecoverable(kind) {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	state := t.outbounds[outbound]
	if state == nil {
		return
	}
	state.lastFailureAt = t.now().UTC()
	state.lastErrorKind = kind
	state.consecutiveFailures++
	state.probeInFlight = false
	if state.consecutiveFailures >= DefaultHealthFailureThreshold {
		state.nextProbeAt = state.lastFailureAt.Add(t.probeInterval)
	}
}

func (t *HealthTracker) Snapshot() []HealthSnapshotItem {
	if t == nil {
		return nil
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	items := make([]HealthSnapshotItem, 0, len(t.outbounds))
	for _, state := range t.outbounds {
		items = append(items, state.snapshot(t.now().UTC()))
	}
	sort.Slice(items, func(i, j int) bool { return items[i].Outbound < items[j].Outbound })
	return items
}

func (s *healthState) blockDecision() HealthDecision {
	return HealthDecision{Allowed: false, Reason: HealthDegraded, NextProbeAt: s.nextProbeAt, RetryAfter: s.nextProbeAt, FailureCount: s.consecutiveFailures}
}

func (s *healthState) snapshot(now time.Time) HealthSnapshotItem {
	state := HealthAvailable
	if s.consecutiveFailures >= DefaultHealthFailureThreshold {
		state = HealthDegraded
		if s.probeInFlight || s.nextProbeAt.IsZero() || !now.Before(s.nextProbeAt) {
			state = HealthProbing
		}
	} else if s.consecutiveFailures > 0 {
		state = HealthDegraded
	}
	return HealthSnapshotItem{
		Outbound:            s.name,
		State:               state,
		LastSuccessAt:       formatHealthTime(s.lastSuccessAt),
		LastFailureAt:       formatHealthTime(s.lastFailureAt),
		LastErrorKind:       string(s.lastErrorKind),
		ConsecutiveFailures: s.consecutiveFailures,
		NextProbeAt:         formatHealthTime(s.nextProbeAt),
	}
}

func formatHealthTime(value time.Time) string {
	if value.IsZero() {
		return ""
	}
	return value.UTC().Format(time.RFC3339Nano)
}
