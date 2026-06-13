package quota

import (
	"sort"
	"sync"
	"time"

	"github.com/ryanycheng/Syrogo/internal/config"
)

const (
	StateAvailable = "available"
	StateLimited   = "limited"
	StateCooldown  = "cooldown"
)

type WindowConfig struct {
	Name        string
	Duration    time.Duration
	MaxRequests int
}

type OutboundConfig struct {
	Name          string
	Windows       []WindowConfig
	Cooldown      time.Duration
	ProbeInterval time.Duration
}

type ClientConfig struct {
	Name    string
	Inbound string
	Windows []WindowConfig
}

type Decision struct {
	Allowed    bool
	Reason     string
	Probe      bool
	RetryAfter time.Time
}

type Tracker struct {
	mu       sync.Mutex
	now      func() time.Time
	subjects map[string]*subjectState
}

type subjectState struct {
	name                string
	inbound             string
	windows             []windowState
	cooldown            time.Duration
	probeInterval       time.Duration
	cooldownUntil       time.Time
	nextProbeAt         time.Time
	lastQuotaExceededAt time.Time
	lastSuccessAt       time.Time
	probeInFlight       bool
}

type windowState struct {
	name        string
	duration    time.Duration
	maxRequests int
	events      []time.Time
}

type SnapshotItem struct {
	Outbound            string           `json:"outbound,omitempty"`
	Client              string           `json:"client,omitempty"`
	Inbound             string           `json:"inbound,omitempty"`
	Enabled             bool             `json:"enabled"`
	State               string           `json:"state"`
	CooldownUntil       string           `json:"cooldown_until,omitempty"`
	NextProbeAt         string           `json:"next_probe_at"`
	LastQuotaExceededAt string           `json:"last_quota_exceeded_at,omitempty"`
	LastSuccessAt       string           `json:"last_success_at"`
	Windows             []SnapshotWindow `json:"windows"`
}

type SnapshotWindow struct {
	Name      string `json:"name"`
	Duration  string `json:"duration"`
	Limit     int    `json:"limit"`
	Used      int    `json:"used"`
	Remaining int    `json:"remaining"`
	ResetAt   string `json:"reset_at"`
}

type PersistedState struct {
	CapturedAt string                      `json:"captured_at"`
	Subjects   map[string]PersistedSubject `json:"subjects"`
}

type PersistedSubject struct {
	Name                string                          `json:"name"`
	Inbound             string                          `json:"inbound,omitempty"`
	CooldownUntil       string                          `json:"cooldown_until,omitempty"`
	NextProbeAt         string                          `json:"next_probe_at,omitempty"`
	LastQuotaExceededAt string                          `json:"last_quota_exceeded_at,omitempty"`
	LastSuccessAt       string                          `json:"last_success_at,omitempty"`
	Windows             map[string]PersistedWindowState `json:"windows"`
}

type PersistedWindowState struct {
	Events []string `json:"events"`
}

func NewTracker(cfgs []OutboundConfig) *Tracker {
	return newTrackerFromOutbounds(cfgs, time.Now)
}

func NewTrackerFromOutbounds(outbounds []config.OutboundSpec) *Tracker {
	cfgs := make([]OutboundConfig, 0)
	for _, outbound := range outbounds {
		if !outbound.Quota.Enabled {
			continue
		}
		cfgs = append(cfgs, OutboundConfig{
			Name:          outbound.Name,
			Windows:       windowsFromConfig(outbound.Quota.Windows),
			Cooldown:      outbound.Quota.Cooldown.Duration(),
			ProbeInterval: outbound.Quota.ProbeInterval.Duration(),
		})
	}
	if len(cfgs) == 0 {
		return nil
	}
	return NewTracker(cfgs)
}

func NewClientTrackerFromInbounds(inbounds []config.InboundSpec) *Tracker {
	cfgs := make([]ClientConfig, 0)
	for _, inbound := range inbounds {
		for _, client := range inbound.Clients {
			if !client.Quota.Enabled {
				continue
			}
			cfgs = append(cfgs, ClientConfig{Name: client.Name, Inbound: inbound.Name, Windows: windowsFromConfig(client.Quota.Windows)})
		}
	}
	if len(cfgs) == 0 {
		return nil
	}
	return NewClientTracker(cfgs)
}

func NewClientTracker(cfgs []ClientConfig) *Tracker {
	return newTrackerFromClients(cfgs, time.Now)
}

func NewTestTracker(cfgs []OutboundConfig, now func() time.Time) *Tracker {
	return newTrackerFromOutbounds(cfgs, now)
}

func NewTestClientTracker(cfgs []ClientConfig, now func() time.Time) *Tracker {
	return newTrackerFromClients(cfgs, now)
}

func windowsFromConfig(windows []config.QuotaWindowConfig) []WindowConfig {
	result := make([]WindowConfig, 0, len(windows))
	for _, window := range windows {
		result = append(result, WindowConfig{Name: window.Name, Duration: window.Duration.Duration(), MaxRequests: window.MaxRequests})
	}
	return result
}

func newTrackerFromOutbounds(cfgs []OutboundConfig, now func() time.Time) *Tracker {
	if now == nil {
		now = time.Now
	}
	tracker := &Tracker{now: now, subjects: make(map[string]*subjectState, len(cfgs))}
	for _, cfg := range cfgs {
		tracker.subjects[cfg.Name] = newSubjectState(cfg.Name, "", cfg.Windows, cfg.Cooldown, cfg.ProbeInterval)
	}
	return tracker
}

func newTrackerFromClients(cfgs []ClientConfig, now func() time.Time) *Tracker {
	if now == nil {
		now = time.Now
	}
	tracker := &Tracker{now: now, subjects: make(map[string]*subjectState, len(cfgs))}
	for _, cfg := range cfgs {
		tracker.subjects[cfg.Name] = newSubjectState(cfg.Name, cfg.Inbound, cfg.Windows, 0, 0)
	}
	return tracker
}

func newSubjectState(name string, inbound string, windows []WindowConfig, cooldown time.Duration, probeInterval time.Duration) *subjectState {
	state := &subjectState{name: name, inbound: inbound, cooldown: cooldown, probeInterval: probeInterval, windows: make([]windowState, 0, len(windows))}
	for _, window := range windows {
		state.windows = append(state.windows, windowState{name: window.Name, duration: window.Duration, maxRequests: window.MaxRequests})
	}
	return state
}

func (t *Tracker) BeforeAttempt(outbound string) Decision {
	return t.beforeAttempt(outbound, true)
}

func (t *Tracker) BeforeClientRequest(client string) Decision {
	return t.beforeAttempt(client, false)
}

func (t *Tracker) beforeAttempt(name string, allowProbe bool) Decision {
	if t == nil {
		return Decision{Allowed: true}
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	state := t.subjects[name]
	if state == nil {
		return Decision{Allowed: true}
	}
	now := t.now().UTC()
	state.prune(now)
	if allowProbe && state.inCooldown(now) {
		if !state.nextProbeAt.IsZero() && !now.Before(state.nextProbeAt) && !state.probeInFlight {
			state.probeInFlight = true
			return Decision{Allowed: true, Probe: true}
		}
		return Decision{Allowed: false, Reason: StateCooldown, RetryAfter: state.nextProbeAt}
	}
	if retryAfter, limited := state.limitReached(now); limited {
		return Decision{Allowed: false, Reason: StateLimited, RetryAfter: retryAfter}
	}
	return Decision{Allowed: true}
}

func (t *Tracker) RecordSuccess(outbound string) {
	t.recordSuccess(outbound, true)
}

func (t *Tracker) RecordClientRequest(client string) {
	t.recordSuccess(client, false)
}

func (t *Tracker) recordSuccess(name string, clearCooldown bool) {
	if t == nil {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	state := t.subjects[name]
	if state == nil {
		return
	}
	now := t.now().UTC()
	state.prune(now)
	for i := range state.windows {
		state.windows[i].events = append(state.windows[i].events, now)
	}
	if clearCooldown {
		state.cooldownUntil = time.Time{}
		state.nextProbeAt = time.Time{}
		state.probeInFlight = false
	}
	state.lastSuccessAt = now
}

func (t *Tracker) RecordQuotaExceeded(outbound string) {
	if t == nil {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	state := t.subjects[outbound]
	if state == nil {
		return
	}
	now := t.now().UTC()
	state.lastQuotaExceededAt = now
	state.cooldownUntil = now.Add(state.cooldown)
	state.nextProbeAt = now.Add(state.probeInterval)
	state.probeInFlight = false
}

func (t *Tracker) Snapshot() []SnapshotItem {
	return t.snapshot(false)
}

func (t *Tracker) ClientSnapshot() []SnapshotItem {
	return t.snapshot(true)
}

func (t *Tracker) ExportState() PersistedState {
	state := PersistedState{CapturedAt: formatTime(time.Now().UTC()), Subjects: map[string]PersistedSubject{}}
	if t == nil {
		return state
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	now := t.now().UTC()
	state.CapturedAt = formatTime(now)
	for name, subject := range t.subjects {
		subject.prune(now)
		windows := make(map[string]PersistedWindowState, len(subject.windows))
		for _, window := range subject.windows {
			events := make([]string, 0, len(window.events))
			for _, event := range window.events {
				events = append(events, formatTime(event))
			}
			windows[window.name] = PersistedWindowState{Events: events}
		}
		state.Subjects[name] = PersistedSubject{
			Name:                subject.name,
			Inbound:             subject.inbound,
			CooldownUntil:       formatTime(subject.cooldownUntil),
			NextProbeAt:         formatTime(subject.nextProbeAt),
			LastQuotaExceededAt: formatTime(subject.lastQuotaExceededAt),
			LastSuccessAt:       formatTime(subject.lastSuccessAt),
			Windows:             windows,
		}
	}
	return state
}

func (t *Tracker) ImportState(persisted PersistedState) {
	if t == nil {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	now := t.now().UTC()
	for name, persistedSubject := range persisted.Subjects {
		subject := t.subjects[name]
		if subject == nil {
			continue
		}
		if value, ok := parsePersistedTime(persistedSubject.CooldownUntil); ok {
			subject.cooldownUntil = value
		}
		if value, ok := parsePersistedTime(persistedSubject.NextProbeAt); ok {
			subject.nextProbeAt = value
		}
		if value, ok := parsePersistedTime(persistedSubject.LastQuotaExceededAt); ok {
			subject.lastQuotaExceededAt = value
		}
		if value, ok := parsePersistedTime(persistedSubject.LastSuccessAt); ok {
			subject.lastSuccessAt = value
		}
		for i := range subject.windows {
			persistedWindow, ok := persistedSubject.Windows[subject.windows[i].name]
			if !ok {
				continue
			}
			events := make([]time.Time, 0, len(persistedWindow.Events))
			cutoff := now.Add(-subject.windows[i].duration)
			for _, raw := range persistedWindow.Events {
				value, ok := parsePersistedTime(raw)
				if ok && value.After(cutoff) {
					events = append(events, value)
				}
			}
			sort.Slice(events, func(i, j int) bool { return events[i].Before(events[j]) })
			subject.windows[i].events = events
		}
		subject.prune(now)
	}
}

func (t *Tracker) snapshot(client bool) []SnapshotItem {
	if t == nil {
		return nil
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	now := t.now().UTC()
	items := make([]SnapshotItem, 0, len(t.subjects))
	for _, state := range t.subjects {
		state.prune(now)
		items = append(items, state.snapshot(now, client))
	}
	sort.Slice(items, func(i, j int) bool {
		if client {
			return items[i].Client < items[j].Client
		}
		return items[i].Outbound < items[j].Outbound
	})
	return items
}

func (s *subjectState) inCooldown(now time.Time) bool {
	return !s.cooldownUntil.IsZero() && now.Before(s.cooldownUntil)
}

func (s *subjectState) limitReached(now time.Time) (time.Time, bool) {
	var retryAfter time.Time
	for _, window := range s.windows {
		if len(window.events) < window.maxRequests {
			continue
		}
		resetAt := window.events[0].Add(window.duration)
		if retryAfter.IsZero() || resetAt.After(retryAfter) {
			retryAfter = resetAt
		}
	}
	return retryAfter, !retryAfter.IsZero() && now.Before(retryAfter)
}

func (s *subjectState) prune(now time.Time) {
	for i := range s.windows {
		window := &s.windows[i]
		cutoff := now.Add(-window.duration)
		keep := 0
		for keep < len(window.events) && !window.events[keep].After(cutoff) {
			keep++
		}
		if keep > 0 {
			copy(window.events, window.events[keep:])
			window.events = window.events[:len(window.events)-keep]
		}
	}
	if !s.cooldownUntil.IsZero() && !now.Before(s.cooldownUntil) {
		s.cooldownUntil = time.Time{}
		s.nextProbeAt = time.Time{}
		s.probeInFlight = false
	}
}

func (s *subjectState) snapshot(now time.Time, client bool) SnapshotItem {
	state := StateAvailable
	var retryAfter time.Time
	if s.inCooldown(now) {
		state = StateCooldown
	} else if resetAt, limited := s.limitReached(now); limited {
		state = StateLimited
		retryAfter = resetAt
	}
	windows := make([]SnapshotWindow, 0, len(s.windows))
	for _, window := range s.windows {
		resetAt := ""
		if len(window.events) > 0 {
			resetAt = formatTime(window.events[0].Add(window.duration))
		}
		remaining := window.maxRequests - len(window.events)
		if remaining < 0 {
			remaining = 0
		}
		windows = append(windows, SnapshotWindow{
			Name:      window.name,
			Duration:  window.duration.String(),
			Limit:     window.maxRequests,
			Used:      len(window.events),
			Remaining: remaining,
			ResetAt:   resetAt,
		})
	}
	item := SnapshotItem{
		Enabled:             true,
		State:               state,
		CooldownUntil:       formatTime(s.cooldownUntil),
		NextProbeAt:         formatTime(s.nextProbeAt),
		LastQuotaExceededAt: formatTime(s.lastQuotaExceededAt),
		LastSuccessAt:       formatTime(s.lastSuccessAt),
		Windows:             windows,
	}
	if client {
		item.Client = s.name
		item.Inbound = s.inbound
	} else {
		item.Outbound = s.name
	}
	if state == StateLimited && item.NextProbeAt == "" {
		item.NextProbeAt = formatTime(retryAfter)
	}
	return item
}

func parsePersistedTime(value string) (time.Time, bool) {
	if value == "" {
		return time.Time{}, false
	}
	parsed, err := time.Parse(time.RFC3339Nano, value)
	if err != nil {
		return time.Time{}, false
	}
	return parsed.UTC(), true
}

func formatTime(value time.Time) string {
	if value.IsZero() {
		return ""
	}
	return value.UTC().Format(time.RFC3339Nano)
}
