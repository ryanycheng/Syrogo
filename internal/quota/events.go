package quota

import (
	"sync"
	"time"

	"github.com/ryanycheng/Syrogo/internal/config"
)

const (
	EventClientLimited          = "client_limited"
	EventOutboundLimited        = "outbound_limited"
	EventOutboundQuotaExceeded  = "outbound_quota_exceeded"
	EventOutboundProbeSucceeded = "outbound_probe_succeeded"
	EventProviderHealthLimited  = "provider_health_limited"
	EventProviderProbeSucceeded = "provider_probe_succeeded"
)

type Event struct {
	Time       string `json:"time"`
	Type       string `json:"type"`
	Client     string `json:"client,omitempty"`
	Inbound    string `json:"inbound,omitempty"`
	Outbound   string `json:"outbound,omitempty"`
	Reason     string `json:"reason,omitempty"`
	RetryAfter string `json:"retry_after,omitempty"`
}

type EventRecorder struct {
	mu         sync.Mutex
	now        func() time.Time
	maxEntries int
	events     []Event
}

func NewEventRecorder(cfg config.GovernanceQuotaEventsConfig) *EventRecorder {
	if !cfg.Enabled {
		return nil
	}
	return NewTestEventRecorder(cfg.MaxEntries, time.Now)
}

func NewTestEventRecorder(maxEntries int, now func() time.Time) *EventRecorder {
	if maxEntries <= 0 {
		return nil
	}
	if now == nil {
		now = time.Now
	}
	return &EventRecorder{now: now, maxEntries: maxEntries, events: make([]Event, 0, maxEntries)}
}

func (r *EventRecorder) Record(event Event) {
	if r == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if event.Time == "" {
		event.Time = formatTime(r.now().UTC())
	}
	if len(r.events) == r.maxEntries {
		copy(r.events, r.events[1:])
		r.events[len(r.events)-1] = event
		return
	}
	r.events = append(r.events, event)
}

func (r *EventRecorder) Snapshot() []Event {
	if r == nil {
		return nil
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	items := make([]Event, len(r.events))
	copy(items, r.events)
	return items
}
