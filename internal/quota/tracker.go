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

type Decision struct {
	Allowed    bool
	Reason     string
	Probe      bool
	RetryAfter time.Time
}

type Tracker struct {
	mu        sync.Mutex
	now       func() time.Time
	outbounds map[string]*outboundState
}

type outboundState struct {
	name                string
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
	Outbound            string           `json:"outbound"`
	Enabled             bool             `json:"enabled"`
	State               string           `json:"state"`
	CooldownUntil       string           `json:"cooldown_until"`
	NextProbeAt         string           `json:"next_probe_at"`
	LastQuotaExceededAt string           `json:"last_quota_exceeded_at"`
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

func NewTracker(cfgs []OutboundConfig) *Tracker {
	return newTracker(cfgs, time.Now)
}

func NewTrackerFromOutbounds(outbounds []config.OutboundSpec) *Tracker {
	cfgs := make([]OutboundConfig, 0)
	for _, outbound := range outbounds {
		if !outbound.Quota.Enabled {
			continue
		}
		windows := make([]WindowConfig, 0, len(outbound.Quota.Windows))
		for _, window := range outbound.Quota.Windows {
			windows = append(windows, WindowConfig{
				Name:        window.Name,
				Duration:    window.Duration.Duration(),
				MaxRequests: window.MaxRequests,
			})
		}
		cfgs = append(cfgs, OutboundConfig{
			Name:          outbound.Name,
			Windows:       windows,
			Cooldown:      outbound.Quota.Cooldown.Duration(),
			ProbeInterval: outbound.Quota.ProbeInterval.Duration(),
		})
	}
	if len(cfgs) == 0 {
		return nil
	}
	return NewTracker(cfgs)
}

func NewTestTracker(cfgs []OutboundConfig, now func() time.Time) *Tracker {
	return newTracker(cfgs, now)
}

func newTracker(cfgs []OutboundConfig, now func() time.Time) *Tracker {
	if now == nil {
		now = time.Now
	}
	tracker := &Tracker{now: now, outbounds: make(map[string]*outboundState, len(cfgs))}
	for _, cfg := range cfgs {
		state := &outboundState{name: cfg.Name, cooldown: cfg.Cooldown, probeInterval: cfg.ProbeInterval, windows: make([]windowState, 0, len(cfg.Windows))}
		for _, window := range cfg.Windows {
			state.windows = append(state.windows, windowState{name: window.Name, duration: window.Duration, maxRequests: window.MaxRequests})
		}
		tracker.outbounds[cfg.Name] = state
	}
	return tracker
}

func (t *Tracker) BeforeAttempt(outbound string) Decision {
	if t == nil {
		return Decision{Allowed: true}
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	state := t.outbounds[outbound]
	if state == nil {
		return Decision{Allowed: true}
	}
	now := t.now().UTC()
	state.prune(now)
	if state.inCooldown(now) {
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
	if t == nil {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	state := t.outbounds[outbound]
	if state == nil {
		return
	}
	now := t.now().UTC()
	state.prune(now)
	for i := range state.windows {
		state.windows[i].events = append(state.windows[i].events, now)
	}
	state.cooldownUntil = time.Time{}
	state.nextProbeAt = time.Time{}
	state.probeInFlight = false
	state.lastSuccessAt = now
}

func (t *Tracker) RecordQuotaExceeded(outbound string) {
	if t == nil {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	state := t.outbounds[outbound]
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
	if t == nil {
		return nil
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	now := t.now().UTC()
	items := make([]SnapshotItem, 0, len(t.outbounds))
	for _, state := range t.outbounds {
		state.prune(now)
		items = append(items, state.snapshot(now))
	}
	sort.Slice(items, func(i, j int) bool { return items[i].Outbound < items[j].Outbound })
	return items
}

func (s *outboundState) inCooldown(now time.Time) bool {
	return !s.cooldownUntil.IsZero() && now.Before(s.cooldownUntil)
}

func (s *outboundState) limitReached(now time.Time) (time.Time, bool) {
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

func (s *outboundState) prune(now time.Time) {
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

func (s *outboundState) snapshot(now time.Time) SnapshotItem {
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
		Outbound:            s.name,
		Enabled:             true,
		State:               state,
		CooldownUntil:       formatTime(s.cooldownUntil),
		NextProbeAt:         formatTime(s.nextProbeAt),
		LastQuotaExceededAt: formatTime(s.lastQuotaExceededAt),
		LastSuccessAt:       formatTime(s.lastSuccessAt),
		Windows:             windows,
	}
	if state == StateLimited && item.NextProbeAt == "" {
		item.NextProbeAt = formatTime(retryAfter)
	}
	return item
}

func formatTime(value time.Time) string {
	if value.IsZero() {
		return ""
	}
	return value.UTC().Format(time.RFC3339Nano)
}
