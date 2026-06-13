package provider

import (
	"sort"
	"sync"
	"time"
)

const (
	HealthAvailable = "available"
	HealthDegraded  = "degraded"

	DefaultHealthFailureThreshold = 3
)

type HealthDecision struct {
	Allowed bool
	Reason  string
}

type HealthTracker struct {
	mu        sync.Mutex
	now       func() time.Time
	outbounds map[string]*healthState
}

type healthState struct {
	name                string
	lastSuccessAt       time.Time
	lastFailureAt       time.Time
	lastErrorKind       ErrorKind
	consecutiveFailures int
}

type HealthSnapshotItem struct {
	Outbound            string `json:"outbound"`
	State               string `json:"state"`
	LastSuccessAt       string `json:"last_success_at"`
	LastFailureAt       string `json:"last_failure_at"`
	LastErrorKind       string `json:"last_error_kind,omitempty"`
	ConsecutiveFailures int    `json:"consecutive_failures"`
}

func NewHealthTracker(outboundNames []string) *HealthTracker {
	return NewTestHealthTracker(outboundNames, time.Now)
}

func NewTestHealthTracker(outboundNames []string, now func() time.Time) *HealthTracker {
	if len(outboundNames) == 0 {
		return nil
	}
	if now == nil {
		now = time.Now
	}
	tracker := &HealthTracker{now: now, outbounds: make(map[string]*healthState, len(outboundNames))}
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
	if state.consecutiveFailures >= DefaultHealthFailureThreshold {
		return HealthDecision{Allowed: false, Reason: HealthDegraded}
	}
	return HealthDecision{Allowed: true}
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
}

func (t *HealthTracker) RecordFailure(outbound string, kind ErrorKind) {
	if t == nil {
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
}

func (t *HealthTracker) Snapshot() []HealthSnapshotItem {
	if t == nil {
		return nil
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	items := make([]HealthSnapshotItem, 0, len(t.outbounds))
	for _, state := range t.outbounds {
		items = append(items, state.snapshot())
	}
	sort.Slice(items, func(i, j int) bool { return items[i].Outbound < items[j].Outbound })
	return items
}

func (s *healthState) snapshot() HealthSnapshotItem {
	state := HealthAvailable
	if s.consecutiveFailures > 0 {
		state = HealthDegraded
	}
	return HealthSnapshotItem{
		Outbound:            s.name,
		State:               state,
		LastSuccessAt:       formatHealthTime(s.lastSuccessAt),
		LastFailureAt:       formatHealthTime(s.lastFailureAt),
		LastErrorKind:       string(s.lastErrorKind),
		ConsecutiveFailures: s.consecutiveFailures,
	}
}

func formatHealthTime(value time.Time) string {
	if value.IsZero() {
		return ""
	}
	return value.UTC().Format(time.RFC3339Nano)
}
