package quota

import (
	"bytes"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
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
	Name            string
	Type            string
	Reset           string
	Duration        time.Duration
	FixedPeriod     string
	Anchor          time.Time
	Time            string
	Timezone        string
	Weekday         time.Weekday
	MaxRequests     int
	MaxTokens       int
	MaxCostMicroUSD int64
}

type ResetAllConfig struct {
	Enabled  bool
	Period   string
	Duration time.Duration
	Anchor   time.Time
	Time     string
	Timezone string
	Weekday  time.Weekday
}

type OutboundConfig struct {
	Name          string
	Windows       []WindowConfig
	Cooldown      time.Duration
	ProbeInterval time.Duration
	ResetAll      ResetAllConfig
}

type ClientConfig struct {
	Name    string
	Inbound string
	Windows []WindowConfig
}

type Decision struct {
	Allowed         bool             `json:"allowed"`
	Reason          string           `json:"reason,omitempty"`
	Probe           bool             `json:"probe,omitempty"`
	RetryAfter      time.Time        `json:"-"`
	BlockingWindows []BlockingWindow `json:"blocking_windows,omitempty"`
}

type BlockingWindow struct {
	Window     string `json:"window"`
	Type       string `json:"type"`
	Limit      any    `json:"limit"`
	Used       any    `json:"used"`
	Unit       string `json:"unit"`
	RetryAfter string `json:"retry_after"`
}

type Tracker struct {
	mu       sync.Mutex
	now      func() time.Time
	client   bool
	subjects map[string]*subjectState
}

type subjectState struct {
	name                string
	inbound             string
	windows             []windowState
	cooldown            time.Duration
	probeInterval       time.Duration
	resetAll            schedule
	cooldownUntil       time.Time
	nextProbeAt         time.Time
	lastQuotaExceededAt time.Time
	lastSuccessAt       time.Time
	probeInFlight       bool
}

type usageEvent struct {
	At           time.Time `json:"-"`
	Requests     int       `json:"requests"`
	Tokens       int       `json:"tokens"`
	CostMicroUSD int64     `json:"cost_micro_usd,omitempty"`
	Unpriced     int       `json:"unpriced,omitempty"`
}

type windowState struct {
	name            string
	typeName        string
	reset           string
	duration        time.Duration
	fixed           schedule
	maxRequests     int
	maxTokens       int
	maxCostMicroUSD int64
	events          []usageEvent
}

type schedule struct {
	period   string
	duration time.Duration
	anchor   time.Time
	clock    string
	location *time.Location
	weekday  time.Weekday
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
	Name              string      `json:"name"`
	Type              string      `json:"type,omitempty"`
	Reset             string      `json:"reset"`
	FixedPeriod       string      `json:"fixed_period,omitempty"`
	Duration          string      `json:"duration,omitempty"`
	MaxRequests       int         `json:"max_requests"`
	UsedRequests      int         `json:"used_requests"`
	RemainingRequests int         `json:"remaining_requests"`
	MaxTokens         int         `json:"max_tokens"`
	UsedTokens        int         `json:"used_tokens"`
	RemainingTokens   int         `json:"remaining_tokens"`
	MaxCostUSD        json.Number `json:"max_cost_usd,omitempty"`
	UsedCostUSD       json.Number `json:"used_cost_usd,omitempty"`
	RemainingCostUSD  json.Number `json:"remaining_cost_usd,omitempty"`
	UnpricedCount     int         `json:"unpriced_count,omitempty"`
	Warning           string      `json:"warning,omitempty"`
	ResetAt           string      `json:"reset_at"`
	Limit             int         `json:"limit"`
	Used              int         `json:"used"`
	Remaining         int         `json:"remaining"`
}

type PersistedState struct {
	Version    int                         `json:"version,omitempty"`
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
	ResetAll            string                          `json:"reset_all,omitempty"`
	Windows             map[string]PersistedWindowState `json:"windows"`
}

type PersistedEvent struct {
	At           string `json:"at"`
	Requests     int    `json:"requests"`
	Tokens       int    `json:"tokens"`
	CostMicroUSD int64  `json:"cost_micro_usd,omitempty"`
	Unpriced     int    `json:"unpriced,omitempty"`
}

type PersistedWindowState struct {
	Type        string           `json:"type,omitempty"`
	Reset       string           `json:"reset,omitempty"`
	Duration    string           `json:"duration,omitempty"`
	FixedPeriod string           `json:"fixed_period,omitempty"`
	Schedule    string           `json:"schedule,omitempty"`
	Events      []PersistedEvent `json:"events"`
	legacy      bool
}

func (p *PersistedWindowState) UnmarshalJSON(data []byte) error {
	var decoded struct {
		Type        string          `json:"type"`
		Reset       string          `json:"reset"`
		Duration    string          `json:"duration"`
		FixedPeriod string          `json:"fixed_period"`
		Schedule    string          `json:"schedule"`
		Events      json.RawMessage `json:"events"`
	}
	if err := json.Unmarshal(data, &decoded); err != nil {
		return err
	}
	p.Type, p.Reset, p.Duration, p.FixedPeriod, p.Schedule = decoded.Type, decoded.Reset, decoded.Duration, decoded.FixedPeriod, decoded.Schedule
	if len(decoded.Events) == 0 || bytes.Equal(decoded.Events, []byte("null")) {
		return nil
	}
	if err := json.Unmarshal(decoded.Events, &p.Events); err == nil {
		return nil
	}
	var legacy []string
	if err := json.Unmarshal(decoded.Events, &legacy); err != nil {
		return err
	}
	p.legacy = true
	p.Events = make([]PersistedEvent, 0, len(legacy))
	for _, at := range legacy {
		p.Events = append(p.Events, PersistedEvent{At: at, Requests: 1})
	}
	return nil
}

func NewTracker(cfgs []OutboundConfig) *Tracker { return newTrackerFromOutbounds(cfgs, time.Now) }

func NewTrackerFromOutbounds(outbounds []config.OutboundSpec) *Tracker {
	cfgs := outboundConfigs(outbounds)
	if len(cfgs) == 0 {
		return nil
	}
	return NewTracker(cfgs)
}

func NewClientTrackerFromClients(clients []config.ClientSpec) *Tracker {
	cfgs := clientConfigs(clients)
	if len(cfgs) == 0 {
		return nil
	}
	return NewClientTracker(cfgs)
}

func NewClientTracker(cfgs []ClientConfig) *Tracker { return newTrackerFromClients(cfgs, time.Now) }
func NewTestTracker(cfgs []OutboundConfig, now func() time.Time) *Tracker {
	return newTrackerFromOutbounds(cfgs, now)
}
func NewTestClientTracker(cfgs []ClientConfig, now func() time.Time) *Tracker {
	return newTrackerFromClients(cfgs, now)
}

func outboundConfigs(outbounds []config.OutboundSpec) []OutboundConfig {
	result := make([]OutboundConfig, 0)
	for _, outbound := range outbounds {
		if !outbound.Quota.Enabled {
			continue
		}
		result = append(result, OutboundConfig{Name: outbound.Name, Windows: outboundWindowsFromConfig(outbound.Quota.Windows), Cooldown: outbound.Quota.Cooldown.Duration(), ProbeInterval: outbound.Quota.ProbeInterval.Duration(), ResetAll: resetAllFromConfig(outbound.Quota.ResetAll)})
	}
	return result
}

func clientConfigs(clients []config.ClientSpec) []ClientConfig {
	result := make([]ClientConfig, 0)
	for _, client := range clients {
		if client.Quota.Enabled {
			result = append(result, ClientConfig{Name: client.Name, Windows: clientWindowsFromConfig(client.Quota.Windows)})
		}
	}
	return result
}

func clientWindowsFromConfig(windows []config.QuotaWindowConfig) []WindowConfig {
	result := make([]WindowConfig, 0, len(windows))
	for _, window := range windows {
		result = append(result, WindowConfig{Name: window.Name, Type: window.Type, Reset: "rolling", Duration: window.Duration.Duration(), MaxRequests: window.MaxRequests, MaxTokens: window.MaxTokens, MaxCostMicroUSD: window.MaxCostMicroUSD()})
	}
	return result
}

func outboundWindowsFromConfig(windows []config.OutboundQuotaWindowConfig) []WindowConfig {
	result := make([]WindowConfig, 0, len(windows))
	for _, window := range windows {
		reset := window.Reset
		if reset == "" {
			reset = "rolling"
		}
		result = append(result, WindowConfig{Name: window.Name, Reset: reset, Duration: window.Duration.Duration(), FixedPeriod: window.Fixed.Period, Anchor: parseConfigTime(window.Fixed.Anchor), Time: window.Fixed.Time, Timezone: window.Fixed.Timezone, Weekday: parseWeekday(window.Fixed.Weekday), MaxRequests: window.MaxRequests, MaxTokens: window.MaxTokens})
	}
	return result
}

func resetAllFromConfig(value config.QuotaResetAllConfig) ResetAllConfig {
	s := value.Schedule
	return ResetAllConfig{Enabled: value.Enabled, Period: s.Period, Duration: s.Duration.Duration(), Anchor: parseConfigTime(s.Anchor), Time: s.Time, Timezone: s.Timezone, Weekday: parseWeekday(s.Weekday)}
}

func parseConfigTime(value string) time.Time {
	parsed, _ := time.Parse(time.RFC3339, value)
	return parsed
}

func parseWeekday(value string) time.Weekday {
	for day := time.Sunday; day <= time.Saturday; day++ {
		if strings.EqualFold(value, day.String()) {
			return day
		}
	}
	return time.Sunday
}

func newTrackerFromOutbounds(cfgs []OutboundConfig, now func() time.Time) *Tracker {
	if now == nil {
		now = time.Now
	}
	tracker := &Tracker{now: now, subjects: make(map[string]*subjectState, len(cfgs))}
	for _, cfg := range cfgs {
		tracker.subjects[cfg.Name] = newSubjectState(cfg.Name, "", cfg.Windows, cfg.Cooldown, cfg.ProbeInterval, cfg.ResetAll)
	}
	return tracker
}

func newTrackerFromClients(cfgs []ClientConfig, now func() time.Time) *Tracker {
	if now == nil {
		now = time.Now
	}
	tracker := &Tracker{now: now, client: true, subjects: make(map[string]*subjectState, len(cfgs))}
	for _, cfg := range cfgs {
		windows := normalizeClientWindows(cfg.Windows)
		tracker.subjects[cfg.Name] = newSubjectState(cfg.Name, cfg.Inbound, windows, 0, 0, ResetAllConfig{})
	}
	return tracker
}

func normalizeClientWindows(windows []WindowConfig) []WindowConfig {
	normalized := append([]WindowConfig(nil), windows...)
	for i := range normalized {
		if normalized[i].Type == "" {
			normalized[i].Type = "requests"
		}
	}
	return normalized
}

func newSubjectState(name, inbound string, windows []WindowConfig, cooldown, probeInterval time.Duration, resetAll ResetAllConfig) *subjectState {
	state := &subjectState{name: name, inbound: inbound, cooldown: cooldown, probeInterval: probeInterval, resetAll: newResetSchedule(resetAll), windows: make([]windowState, 0, len(windows))}
	for _, window := range windows {
		state.windows = append(state.windows, newWindowState(window))
	}
	return state
}

func newWindowState(cfg WindowConfig) windowState {
	reset := cfg.Reset
	if reset == "" {
		reset = "rolling"
	}
	return windowState{name: cfg.Name, typeName: cfg.Type, reset: reset, duration: cfg.Duration, fixed: newSchedule(cfg.FixedPeriod, cfg.Duration, cfg.Anchor, cfg.Time, cfg.Timezone, cfg.Weekday), maxRequests: cfg.MaxRequests, maxTokens: cfg.MaxTokens, maxCostMicroUSD: cfg.MaxCostMicroUSD}
}

func newResetSchedule(cfg ResetAllConfig) schedule {
	if !cfg.Enabled {
		return schedule{}
	}
	return newSchedule(cfg.Period, cfg.Duration, cfg.Anchor, cfg.Time, cfg.Timezone, cfg.Weekday)
}

func newSchedule(period string, duration time.Duration, anchor time.Time, clock, timezone string, weekday time.Weekday) schedule {
	location := time.UTC
	if timezone != "" {
		if loaded, err := time.LoadLocation(timezone); err == nil {
			location = loaded
		}
	}
	return schedule{period: period, duration: duration, anchor: anchor, clock: clock, location: location, weekday: weekday}
}

func (t *Tracker) BeforeAttempt(outbound string) Decision     { return t.beforeAttempt(outbound, true) }
func (t *Tracker) BeforeClientRequest(client string) Decision { return t.beforeAttempt(client, false) }

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
	now := t.now()
	state.rollover(now)
	if allowProbe && state.inCooldown(now) {
		if !state.nextProbeAt.IsZero() && !now.Before(state.nextProbeAt) && !state.probeInFlight {
			state.probeInFlight = true
			return Decision{Allowed: true, Probe: true}
		}
		return Decision{Allowed: false, Reason: StateCooldown, RetryAfter: state.nextProbeAt}
	}
	if retryAfter, blocking := state.blockingWindows(now); len(blocking) > 0 {
		return Decision{Allowed: false, Reason: StateLimited, RetryAfter: retryAfter, BlockingWindows: blocking}
	}
	return Decision{Allowed: true}
}

func (t *Tracker) RecordSuccess(outbound string, tokens ...int) {
	tokenCount := 0
	for _, value := range tokens {
		tokenCount += value
	}
	t.recordSuccess(outbound, true, tokenCount)
}
func (t *Tracker) RecordClientRequest(client string) {
	t.recordClientUsage(client, usageEvent{Requests: 1})
}

func (t *Tracker) RecordClientTerminalUsage(client string, tokens int, costMicroUSD int64, priced bool) {
	event := usageEvent{Tokens: tokens, CostMicroUSD: costMicroUSD}
	if !priced {
		event.Unpriced = 1
		event.CostMicroUSD = 0
	}
	t.recordClientUsage(client, event)
}

func (t *Tracker) recordClientUsage(name string, event usageEvent) {
	if t == nil {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	state := t.subjects[name]
	if state == nil {
		return
	}
	now := t.now()
	state.rollover(now)
	event.At = now
	for i := range state.windows {
		window := &state.windows[i]
		switch window.typeName {
		case "requests":
			if event.Requests > 0 {
				window.events = append(window.events, event)
			}
		case "tokens":
			if event.Tokens > 0 {
				window.events = append(window.events, event)
			}
		case "cost":
			if event.CostMicroUSD > 0 || event.Unpriced > 0 {
				window.events = append(window.events, event)
			}
		default:
			window.events = append(window.events, event)
		}
	}
	state.lastSuccessAt = now
}

func (t *Tracker) recordSuccess(name string, clearCooldown bool, tokens int) {
	if t == nil {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	state := t.subjects[name]
	if state == nil {
		return
	}
	now := t.now()
	state.rollover(now)
	for i := range state.windows {
		state.windows[i].events = append(state.windows[i].events, usageEvent{At: now, Requests: 1, Tokens: tokens})
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
	now := t.now()
	state.rollover(now)
	state.lastQuotaExceededAt = now
	state.cooldownUntil = now.Add(state.cooldown)
	state.nextProbeAt = now.Add(state.probeInterval)
	state.probeInFlight = false
}

func (t *Tracker) Snapshot() []SnapshotItem       { return t.snapshot(false) }
func (t *Tracker) ClientSnapshot() []SnapshotItem { return t.snapshot(true) }

func (t *Tracker) ExportState() PersistedState {
	state := PersistedState{Version: 3, CapturedAt: formatTime(time.Now().UTC()), Subjects: map[string]PersistedSubject{}}
	if t == nil {
		return state
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	now := t.now()
	state.CapturedAt = formatTime(now)
	for name, subject := range t.subjects {
		subject.rollover(now)
		windows := make(map[string]PersistedWindowState, len(subject.windows))
		for _, window := range subject.windows {
			events := make([]PersistedEvent, 0, len(window.events))
			for _, event := range window.events {
				events = append(events, PersistedEvent{At: formatTime(event.At), Requests: event.Requests, Tokens: event.Tokens, CostMicroUSD: event.CostMicroUSD, Unpriced: event.Unpriced})
			}
			windows[window.name] = PersistedWindowState{Type: window.typeName, Reset: window.reset, Duration: window.duration.String(), FixedPeriod: window.fixed.period, Schedule: window.scheduleKey(), Events: events}
		}
		state.Subjects[name] = PersistedSubject{Name: subject.name, Inbound: subject.inbound, CooldownUntil: formatTime(subject.cooldownUntil), NextProbeAt: formatTime(subject.nextProbeAt), LastQuotaExceededAt: formatTime(subject.lastQuotaExceededAt), LastSuccessAt: formatTime(subject.lastSuccessAt), ResetAll: subject.resetAll.key(), Windows: windows}
	}
	return state
}

func (t *Tracker) ImportState(persisted PersistedState) {
	if t == nil {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	now := t.now()
	for name, saved := range persisted.Subjects {
		subject := t.subjects[name]
		if subject == nil {
			continue
		}
		resetAllCompatible := persisted.Version < 2 || saved.ResetAll == subject.resetAll.key()
		if value, ok := parsePersistedTime(saved.CooldownUntil); ok {
			subject.cooldownUntil = value
		}
		if value, ok := parsePersistedTime(saved.NextProbeAt); ok {
			subject.nextProbeAt = value
		}
		if value, ok := parsePersistedTime(saved.LastQuotaExceededAt); ok {
			subject.lastQuotaExceededAt = value
		}
		if value, ok := parsePersistedTime(saved.LastSuccessAt); ok {
			subject.lastSuccessAt = value
		}
		for i := range subject.windows {
			window := &subject.windows[i]
			if !resetAllCompatible {
				continue
			}
			savedWindow, ok := saved.Windows[window.name]
			if !ok || !window.persistedCompatible(savedWindow, persisted.Version, t.client) {
				continue
			}
			events := make([]usageEvent, 0, len(savedWindow.Events))
			for _, event := range savedWindow.Events {
				at, ok := parsePersistedTime(event.At)
				if ok {
					events = append(events, usageEvent{At: at, Requests: event.Requests, Tokens: event.Tokens, CostMicroUSD: event.CostMicroUSD, Unpriced: event.Unpriced})
				}
			}
			sort.SliceStable(events, func(i, j int) bool { return events[i].At.Before(events[j].At) })
			window.events = events
		}
		subject.rollover(now)
	}
}

func (t *Tracker) Reconfigure(cfgs []OutboundConfig) bool {
	if t == nil {
		return false
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.reconfigureOutbound(cfgs)
}

func (t *Tracker) ReconfigureOutbounds(outbounds []config.OutboundSpec) bool {
	return t.Reconfigure(outboundConfigs(outbounds))
}

func (t *Tracker) ReconfigureClients(cfgs []ClientConfig) bool {
	if t == nil {
		return false
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	changed := false
	next := make(map[string]*subjectState, len(cfgs))
	for _, cfg := range cfgs {
		fresh := newSubjectState(cfg.Name, cfg.Inbound, normalizeClientWindows(cfg.Windows), 0, 0, ResetAllConfig{})
		if old := t.subjects[cfg.Name]; old != nil {
			changed = mergeWindows(old, fresh) || changed
		}
		next[cfg.Name] = fresh
	}
	for name, old := range t.subjects {
		if _, ok := next[name]; !ok && hasUsage(old) {
			changed = true
		}
	}
	t.subjects = next
	return changed
}

func (t *Tracker) ReconfigureClientSpecs(clients []config.ClientSpec) bool {
	return t.ReconfigureClients(clientConfigs(clients))
}

func (t *Tracker) reconfigureOutbound(cfgs []OutboundConfig) bool {
	changed := false
	next := make(map[string]*subjectState, len(cfgs))
	for _, cfg := range cfgs {
		fresh := newSubjectState(cfg.Name, "", cfg.Windows, cfg.Cooldown, cfg.ProbeInterval, cfg.ResetAll)
		if old := t.subjects[cfg.Name]; old != nil {
			fresh.cooldownUntil, fresh.nextProbeAt, fresh.lastQuotaExceededAt, fresh.lastSuccessAt, fresh.probeInFlight = old.cooldownUntil, old.nextProbeAt, old.lastQuotaExceededAt, old.lastSuccessAt, old.probeInFlight
			if old.resetAll.key() != fresh.resetAll.key() {
				changed = hasUsage(old) || changed
			} else {
				changed = mergeWindows(old, fresh) || changed
			}
		}
		next[cfg.Name] = fresh
	}
	for name, old := range t.subjects {
		if _, ok := next[name]; !ok && hasUsage(old) {
			changed = true
		}
	}
	t.subjects = next
	return changed
}

func mergeWindows(old, fresh *subjectState) bool {
	cleared := false
	oldByName := make(map[string]windowState, len(old.windows))
	for _, window := range old.windows {
		oldByName[window.name] = window
	}
	for i := range fresh.windows {
		previous, ok := oldByName[fresh.windows[i].name]
		if !ok {
			continue
		}
		delete(oldByName, fresh.windows[i].name)
		if fresh.windows[i].compatible(previous) {
			fresh.windows[i].events = previous.events
		} else if len(previous.events) > 0 {
			cleared = true
		}
	}
	for _, removed := range oldByName {
		if len(removed.events) > 0 {
			cleared = true
		}
	}
	return cleared
}

func hasUsage(subject *subjectState) bool {
	for _, window := range subject.windows {
		if len(window.events) > 0 {
			return true
		}
	}
	return false
}

func (t *Tracker) snapshot(client bool) []SnapshotItem {
	if t == nil {
		return nil
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	now := t.now()
	items := make([]SnapshotItem, 0, len(t.subjects))
	for _, state := range t.subjects {
		state.rollover(now)
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
	retryAfter, blocking := s.blockingWindows(now)
	return retryAfter, len(blocking) > 0
}

func (s *subjectState) blockingWindows(now time.Time) (time.Time, []BlockingWindow) {
	var retryAfter time.Time
	blocking := make([]BlockingWindow, 0)
	for i := range s.windows {
		window := &s.windows[i]
		resetAt, hit := window.retryAfter(now)
		if !hit {
			continue
		}
		if retryAfter.IsZero() || resetAt.After(retryAfter) {
			retryAfter = resetAt
		}
		requests, tokens, costMicroUSD, _ := window.usage()
		typeName := window.typeName
		if typeName == "" {
			typeName = "requests"
		}
		item := BlockingWindow{Window: window.name, Type: typeName, RetryAfter: formatTime(resetAt)}
		switch typeName {
		case "tokens":
			item.Limit, item.Used, item.Unit = window.maxTokens, tokens, "tokens"
		case "cost":
			item.Limit, item.Used, item.Unit = formatMicroUSD(window.maxCostMicroUSD), formatMicroUSD(costMicroUSD), "USD"
		default:
			item.Limit, item.Used, item.Unit = window.maxRequests, requests, "requests"
		}
		blocking = append(blocking, item)
	}
	return retryAfter, blocking
}

func (s *subjectState) rollover(now time.Time) {
	if s.resetAll.period != "" {
		start, _ := s.resetAll.bounds(now)
		for i := range s.windows {
			s.windows[i].discardBefore(start)
		}
	}
	for i := range s.windows {
		s.windows[i].rollover(now)
	}
	if !s.cooldownUntil.IsZero() && !now.Before(s.cooldownUntil) {
		s.cooldownUntil, s.nextProbeAt, s.probeInFlight = time.Time{}, time.Time{}, false
	}
}

func (w *windowState) rollover(now time.Time) {
	if w.reset == "fixed" {
		start, _ := w.fixed.bounds(now)
		w.discardBefore(start)
		return
	}
	w.discardThrough(now.Add(-w.duration))
}

func (w *windowState) discardBefore(cutoff time.Time) {
	keep := 0
	for keep < len(w.events) && w.events[keep].At.Before(cutoff) {
		keep++
	}
	w.drop(keep)
}
func (w *windowState) discardThrough(cutoff time.Time) {
	keep := 0
	for keep < len(w.events) && !w.events[keep].At.After(cutoff) {
		keep++
	}
	w.drop(keep)
}
func (w *windowState) drop(count int) {
	if count == 0 {
		return
	}
	copy(w.events, w.events[count:])
	w.events = w.events[:len(w.events)-count]
}

func (w windowState) usage() (requests, tokens int, costMicroUSD int64, unpriced int) {
	for _, event := range w.events {
		requests += event.Requests
		tokens += event.Tokens
		costMicroUSD += event.CostMicroUSD
		unpriced += event.Unpriced
	}
	return
}

func (w windowState) retryAfter(now time.Time) (time.Time, bool) {
	requests, tokens, costMicroUSD, _ := w.usage()
	requestLimited := w.maxRequests > 0 && requests >= w.maxRequests
	tokenLimited := w.maxTokens > 0 && tokens >= w.maxTokens
	costLimited := w.maxCostMicroUSD > 0 && costMicroUSD >= w.maxCostMicroUSD
	if !requestLimited && !tokenLimited && !costLimited {
		return time.Time{}, false
	}
	if w.reset == "fixed" {
		_, end := w.fixed.bounds(now)
		return end, true
	}
	needRequests, needTokens := 0, 0
	needCost := int64(0)
	if requestLimited {
		needRequests = requests - w.maxRequests + 1
	}
	if tokenLimited {
		needTokens = tokens - w.maxTokens + 1
	}
	if costLimited {
		needCost = costMicroUSD - w.maxCostMicroUSD + 1
	}
	removedRequests, removedTokens := 0, 0
	removedCost := int64(0)
	for _, event := range w.events {
		removedRequests += event.Requests
		removedTokens += event.Tokens
		removedCost += event.CostMicroUSD
		if removedRequests >= needRequests && removedTokens >= needTokens && removedCost >= needCost {
			return event.At.Add(w.duration), true
		}
	}
	return w.events[len(w.events)-1].At.Add(w.duration), true
}

func (w windowState) compatible(other windowState) bool {
	if w.typeName != other.typeName || w.reset != other.reset {
		return false
	}
	if w.reset == "rolling" {
		return w.duration == other.duration
	}
	return w.scheduleKey() == other.scheduleKey()
}
func (w windowState) scheduleKey() string {
	if w.reset == "rolling" {
		return w.duration.String()
	}
	return w.fixed.key()
}
func (w windowState) persistedCompatible(saved PersistedWindowState, version int, client bool) bool {
	if version < 2 || saved.legacy {
		if client && w.typeName != "requests" {
			return false
		}
		return w.reset == "rolling" && (saved.Duration == "" || saved.Duration == w.duration.String())
	}
	if version < 3 {
		if client && w.typeName != "requests" {
			return false
		}
		return saved.Reset == w.reset && saved.Schedule == w.scheduleKey()
	}
	return saved.Type == w.typeName && saved.Reset == w.reset && saved.Schedule == w.scheduleKey()
}

func (s schedule) key() string {
	if s.period == "" {
		return ""
	}
	zone := ""
	if s.location != nil {
		zone = s.location.String()
	}
	return strings.Join([]string{s.period, s.duration.String(), formatTime(s.anchor), s.clock, zone, s.weekday.String()}, "|")
}

func (s schedule) bounds(now time.Time) (time.Time, time.Time) {
	switch s.period {
	case "daily":
		return s.civilBounds(now, 1, false)
	case "weekly":
		return s.civilBounds(now, 7, true)
	default:
		if s.duration <= 0 {
			return time.Time{}, time.Time{}
		}
		anchor := s.anchor
		if anchor.IsZero() {
			anchor = time.Unix(0, 0).UTC()
		}
		delta := now.Sub(anchor)
		steps := floorDuration(delta, s.duration)
		start := anchor.Add(steps * s.duration)
		return start, start.Add(s.duration)
	}
}

func floorDuration(delta, interval time.Duration) time.Duration {
	steps := delta / interval
	if delta < 0 && delta%interval != 0 {
		steps--
	}
	return steps
}

func (s schedule) civilBounds(now time.Time, days int, weekly bool) (time.Time, time.Time) {
	loc := s.location
	if loc == nil {
		loc = time.UTC
	}
	local := now.In(loc)
	hour, minute, second := parseClock(s.clock)
	date := local
	if weekly {
		delta := (int(local.Weekday()) - int(s.weekday) + 7) % 7
		date = local.AddDate(0, 0, -delta)
	}
	start := time.Date(date.Year(), date.Month(), date.Day(), hour, minute, second, 0, loc)
	if now.Before(start) {
		start = start.AddDate(0, 0, -days)
	}
	return start, start.AddDate(0, 0, days)
}

func parseClock(value string) (hour, minute, second int) {
	layout := "15:04"
	if strings.Count(value, ":") == 2 {
		layout = "15:04:05"
	}
	parsed, err := time.Parse(layout, value)
	if err != nil {
		return 0, 0, 0
	}
	return parsed.Hour(), parsed.Minute(), parsed.Second()
}

func (s *subjectState) snapshot(now time.Time, client bool) SnapshotItem {
	state := StateAvailable
	var retryAfter time.Time
	if s.inCooldown(now) {
		state = StateCooldown
	} else if resetAt, limited := s.limitReached(now); limited {
		state, retryAfter = StateLimited, resetAt
	}
	windows := make([]SnapshotWindow, 0, len(s.windows))
	for _, window := range s.windows {
		requests, tokens, costMicroUSD, unpriced := window.usage()
		remainingRequests := remaining(window.maxRequests, requests)
		remainingTokens := remaining(window.maxTokens, tokens)
		remainingCostMicroUSD := remaining64(window.maxCostMicroUSD, costMicroUSD)
		resetAt := time.Time{}
		if window.reset == "fixed" {
			_, resetAt = window.fixed.bounds(now)
		} else if limitedAt, limited := window.retryAfter(now); limited {
			resetAt = limitedAt
		} else if len(window.events) > 0 {
			resetAt = window.events[0].At.Add(window.duration)
		}
		warning := ""
		if unpriced > 0 {
			warning = "cost quota excludes unpriced terminal usage"
		}
		windows = append(windows, SnapshotWindow{Name: window.name, Type: window.typeName, Reset: window.reset, FixedPeriod: window.fixed.period, Duration: durationString(window), MaxRequests: window.maxRequests, UsedRequests: requests, RemainingRequests: remainingRequests, MaxTokens: window.maxTokens, UsedTokens: tokens, RemainingTokens: remainingTokens, MaxCostUSD: formatMicroUSD(window.maxCostMicroUSD), UsedCostUSD: formatMicroUSD(costMicroUSD), RemainingCostUSD: formatMicroUSD(remainingCostMicroUSD), UnpricedCount: unpriced, Warning: warning, ResetAt: formatTime(resetAt), Limit: window.maxRequests, Used: requests, Remaining: remainingRequests})
	}
	item := SnapshotItem{Enabled: true, State: state, CooldownUntil: formatTime(s.cooldownUntil), NextProbeAt: formatTime(s.nextProbeAt), LastQuotaExceededAt: formatTime(s.lastQuotaExceededAt), LastSuccessAt: formatTime(s.lastSuccessAt), Windows: windows}
	if client {
		item.Client, item.Inbound = s.name, s.inbound
	} else {
		item.Outbound = s.name
	}
	if state == StateLimited && item.NextProbeAt == "" {
		item.NextProbeAt = formatTime(retryAfter)
	}
	return item
}

func remaining(maximum, used int) int {
	if maximum == 0 {
		return 0
	}
	value := maximum - used
	if value < 0 {
		return 0
	}
	return value
}
func remaining64(maximum, used int64) int64 {
	if maximum == 0 {
		return 0
	}
	value := maximum - used
	if value < 0 {
		return 0
	}
	return value
}

func formatMicroUSD(value int64) json.Number {
	if value == 0 {
		return ""
	}
	whole := value / 1_000_000
	fraction := value % 1_000_000
	if fraction == 0 {
		return json.Number(fmt.Sprintf("%d", whole))
	}
	return json.Number(strings.TrimRight(fmt.Sprintf("%d.%06d", whole, fraction), "0"))
}

func durationString(window windowState) string {
	if window.duration <= 0 {
		return ""
	}
	return window.duration.String()
}

func parsePersistedTime(value string) (time.Time, bool) {
	if value == "" {
		return time.Time{}, false
	}
	parsed, err := time.Parse(time.RFC3339Nano, value)
	if err != nil {
		return time.Time{}, false
	}
	return parsed, true
}
func formatTime(value time.Time) string {
	if value.IsZero() {
		return ""
	}
	return value.UTC().Format(time.RFC3339Nano)
}
