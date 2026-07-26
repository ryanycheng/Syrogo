package quota

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/ryanycheng/Syrogo/internal/config"
)

func TestClientTrackerBuildsFromTopLevelClients(t *testing.T) {
	tracker := NewClientTrackerFromClients([]config.ClientSpec{
		{Name: "office-key", Quota: config.ClientQuotaConfig{Enabled: true, Windows: []config.QuotaWindowConfig{{Name: "hourly", Duration: "1h", MaxRequests: 2}}}},
		{Name: "unlimited"},
	})
	if tracker == nil {
		t.Fatal("NewClientTrackerFromClients() = nil")
	}
	items := tracker.ClientSnapshot()
	if len(items) != 1 || items[0].Client != "office-key" || items[0].Inbound != "" {
		t.Fatalf("ClientSnapshot() = %#v", items)
	}
}

func TestClientTrackerTypedWindows(t *testing.T) {
	now := time.Date(2026, 6, 12, 9, 0, 0, 0, time.UTC)
	tracker := NewTestClientTracker([]ClientConfig{{Name: "client", Windows: []WindowConfig{
		{Name: "requests", Type: "requests", Duration: time.Hour, MaxRequests: 1},
		{Name: "tokens", Type: "tokens", Duration: time.Hour, MaxTokens: 100},
		{Name: "cost", Type: "cost", Duration: time.Hour, MaxCostMicroUSD: 1_500_000},
	}}}, func() time.Time { return now })

	tracker.RecordClientRequest("client")
	windows := tracker.ClientSnapshot()[0].Windows
	if windows[0].UsedRequests != 1 || windows[1].UsedTokens != 0 || windows[2].UsedCostUSD != "" {
		t.Fatalf("request snapshot = %#v", windows)
	}
	tracker.RecordClientTerminalUsage("client", 100, 1_250_001, true)
	decision := tracker.BeforeClientRequest("client")
	if decision.Allowed || !decision.RetryAfter.Equal(now.Add(time.Hour)) {
		t.Fatalf("typed limit decision = %#v", decision)
	}
	if len(decision.BlockingWindows) != 2 {
		t.Fatalf("blocking windows = %#v, want request and token limits", decision.BlockingWindows)
	}
	if got := decision.BlockingWindows[1]; got.Type != "tokens" || got.Limit != 100 || got.Used != 100 || got.Unit != "tokens" {
		t.Fatalf("token blocking window = %#v", got)
	}
	tracker.RecordClientTerminalUsage("client", 50, 1_500_000, true)
	decision = tracker.BeforeClientRequest("client")
	if len(decision.BlockingWindows) != 3 {
		t.Fatalf("blocking windows after cost limit = %#v, want all typed limits", decision.BlockingWindows)
	}
	if got := decision.BlockingWindows[2]; got.Type != "cost" || got.Limit != json.Number("1.5") || got.Used != json.Number("2.750001") || got.Unit != "USD" {
		t.Fatalf("cost blocking window = %#v", got)
	}
	windows = tracker.ClientSnapshot()[0].Windows
	if windows[1].UsedTokens != 150 || windows[2].UsedCostUSD != "2.750001" || windows[2].RemainingCostUSD != "" {
		t.Fatalf("terminal snapshot = %#v", windows)
	}
}

func TestClientTrackerCostTracksUnpricedUsage(t *testing.T) {
	now := time.Date(2026, 6, 12, 9, 0, 0, 0, time.UTC)
	tracker := NewTestClientTracker([]ClientConfig{{Name: "client", Windows: []WindowConfig{{Name: "cost", Type: "cost", Duration: time.Hour, MaxCostMicroUSD: 1_000_000}}}}, func() time.Time { return now })
	tracker.RecordClientTerminalUsage("client", 50, 900_000, false)
	window := tracker.ClientSnapshot()[0].Windows[0]
	if window.UsedCostUSD != "" || window.UnpricedCount != 1 || window.Warning == "" {
		t.Fatalf("unpriced snapshot = %#v", window)
	}
	if decision := tracker.BeforeClientRequest("client"); !decision.Allowed {
		t.Fatalf("unpriced usage limited request = %#v", decision)
	}
}

func TestClientReconfigureTypeChangeResetsUsage(t *testing.T) {
	now := time.Date(2026, 6, 12, 9, 0, 0, 0, time.UTC)
	tracker := NewTestClientTracker([]ClientConfig{{Name: "client", Windows: []WindowConfig{{Name: "window", Type: "requests", Duration: time.Hour, MaxRequests: 10}}}}, func() time.Time { return now })
	tracker.RecordClientRequest("client")
	if cleared := tracker.ReconfigureClients([]ClientConfig{{Name: "client", Windows: []WindowConfig{{Name: "window", Type: "requests", Duration: time.Hour, MaxRequests: 20}}}}); cleared {
		t.Fatal("limit change reset usage")
	}
	if cleared := tracker.ReconfigureClients([]ClientConfig{{Name: "client", Windows: []WindowConfig{{Name: "window", Type: "tokens", Duration: time.Hour, MaxTokens: 20}}}}); !cleared {
		t.Fatal("type change did not reset usage")
	}
}

func TestTrackerAppliesMultipleWindows(t *testing.T) {
	now := time.Date(2026, 6, 12, 9, 0, 0, 0, time.UTC)
	tracker := NewTestTracker([]OutboundConfig{{
		Name:          "primary",
		Cooldown:      10 * time.Minute,
		ProbeInterval: time.Minute,
		Windows: []WindowConfig{
			{Name: "short", Duration: time.Hour, MaxRequests: 2},
			{Name: "long", Duration: 24 * time.Hour, MaxRequests: 10},
		},
	}}, func() time.Time { return now })

	if decision := tracker.BeforeAttempt("primary"); !decision.Allowed {
		t.Fatalf("BeforeAttempt() = %#v, want allowed", decision)
	}
	tracker.RecordSuccess("primary")
	tracker.RecordSuccess("primary")

	decision := tracker.BeforeAttempt("primary")
	if decision.Allowed || decision.Reason != StateLimited {
		t.Fatalf("BeforeAttempt() = %#v, want limited", decision)
	}

	now = now.Add(time.Hour + time.Nanosecond)
	if decision := tracker.BeforeAttempt("primary"); !decision.Allowed {
		t.Fatalf("BeforeAttempt() after window expiry = %#v, want allowed", decision)
	}
}

func TestTrackerCooldownAndProbeRecovery(t *testing.T) {
	now := time.Date(2026, 6, 12, 9, 0, 0, 0, time.UTC)
	tracker := NewTestTracker([]OutboundConfig{{
		Name:          "primary",
		Cooldown:      10 * time.Minute,
		ProbeInterval: time.Minute,
		Windows:       []WindowConfig{{Name: "daily", Duration: 24 * time.Hour, MaxRequests: 100}},
	}}, func() time.Time { return now })

	tracker.RecordQuotaExceeded("primary")
	decision := tracker.BeforeAttempt("primary")
	if decision.Allowed || decision.Reason != StateCooldown {
		t.Fatalf("BeforeAttempt() = %#v, want cooldown", decision)
	}

	now = now.Add(time.Minute)
	decision = tracker.BeforeAttempt("primary")
	if !decision.Allowed || !decision.Probe {
		t.Fatalf("BeforeAttempt() at probe interval = %#v, want probe", decision)
	}
	decision = tracker.BeforeAttempt("primary")
	if decision.Allowed || decision.Reason != StateCooldown {
		t.Fatalf("second BeforeAttempt() during probe = %#v, want cooldown", decision)
	}

	tracker.RecordSuccess("primary")
	decision = tracker.BeforeAttempt("primary")
	if !decision.Allowed || decision.Probe {
		t.Fatalf("BeforeAttempt() after success = %#v, want normal allowed", decision)
	}
}

func TestTrackerProbeQuotaExceededRefreshesCooldown(t *testing.T) {
	now := time.Date(2026, 6, 12, 9, 0, 0, 0, time.UTC)
	tracker := NewTestTracker([]OutboundConfig{{
		Name:          "primary",
		Cooldown:      10 * time.Minute,
		ProbeInterval: time.Minute,
		Windows:       []WindowConfig{{Name: "daily", Duration: 24 * time.Hour, MaxRequests: 100}},
	}}, func() time.Time { return now })

	tracker.RecordQuotaExceeded("primary")
	now = now.Add(time.Minute)
	if decision := tracker.BeforeAttempt("primary"); !decision.Allowed || !decision.Probe {
		t.Fatalf("BeforeAttempt() = %#v, want probe", decision)
	}
	tracker.RecordQuotaExceeded("primary")

	now = now.Add(30 * time.Second)
	decision := tracker.BeforeAttempt("primary")
	if decision.Allowed || decision.RetryAfter.Before(now.Add(29*time.Second)) {
		t.Fatalf("BeforeAttempt() = %#v, want refreshed cooldown", decision)
	}
}

func TestTrackerExportsAndImportsState(t *testing.T) {
	now := time.Date(2026, 6, 12, 9, 0, 0, 0, time.UTC)
	source := NewTestTracker([]OutboundConfig{{
		Name:          "primary",
		Cooldown:      10 * time.Minute,
		ProbeInterval: time.Minute,
		Windows:       []WindowConfig{{Name: "hourly", Duration: time.Hour, MaxRequests: 1}},
	}}, func() time.Time { return now })
	source.RecordSuccess("primary")
	source.RecordQuotaExceeded("primary")
	state := source.ExportState()

	target := NewTestTracker([]OutboundConfig{{
		Name:          "primary",
		Cooldown:      10 * time.Minute,
		ProbeInterval: time.Minute,
		Windows:       []WindowConfig{{Name: "hourly", Duration: time.Hour, MaxRequests: 1}},
	}}, func() time.Time { return now })
	target.ImportState(state)

	decision := target.BeforeAttempt("primary")
	if decision.Allowed || decision.Reason != StateCooldown {
		t.Fatalf("BeforeAttempt() after import = %#v, want cooldown", decision)
	}
	items := target.Snapshot()
	if len(items) != 1 || items[0].Windows[0].Used != 1 {
		t.Fatalf("Snapshot() after import = %#v, want restored window usage", items)
	}
}
func TestTrackerSnapshotReportsWindowUsageAndCooldown(t *testing.T) {
	now := time.Date(2026, 6, 12, 9, 0, 0, 0, time.UTC)
	tracker := NewTestTracker([]OutboundConfig{{
		Name:          "primary",
		Cooldown:      10 * time.Minute,
		ProbeInterval: time.Minute,
		Windows:       []WindowConfig{{Name: "daily", Duration: 24 * time.Hour, MaxRequests: 2}},
	}}, func() time.Time { return now })
	tracker.RecordSuccess("primary")
	tracker.RecordQuotaExceeded("primary")

	items := tracker.Snapshot()
	if len(items) != 1 {
		t.Fatalf("len(Snapshot()) = %d, want 1", len(items))
	}
	item := items[0]
	if item.Outbound != "primary" || item.State != StateCooldown || item.CooldownUntil == "" || item.NextProbeAt == "" {
		t.Fatalf("Snapshot()[0] = %#v, want cooldown state", item)
	}
	if len(item.Windows) != 1 || item.Windows[0].Used != 1 || item.Windows[0].Remaining != 1 {
		t.Fatalf("Snapshot()[0].Windows = %#v, want usage", item.Windows)
	}
}

func TestClientTrackerAppliesWindows(t *testing.T) {
	now := time.Date(2026, 6, 12, 9, 0, 0, 0, time.UTC)
	tracker := NewTestClientTracker([]ClientConfig{{
		Name:    "office-key",
		Inbound: "openai-entry",
		Windows: []WindowConfig{
			{Name: "hourly", Duration: time.Hour, MaxRequests: 1},
			{Name: "daily", Duration: 24 * time.Hour, MaxRequests: 100},
		},
	}}, func() time.Time { return now })

	if decision := tracker.BeforeClientRequest("office-key"); !decision.Allowed {
		t.Fatalf("BeforeClientRequest() = %#v, want allowed", decision)
	}
	tracker.RecordClientRequest("office-key")

	decision := tracker.BeforeClientRequest("office-key")
	if decision.Allowed || decision.Reason != StateLimited || decision.RetryAfter.IsZero() {
		t.Fatalf("BeforeClientRequest() = %#v, want limited", decision)
	}

	now = now.Add(time.Hour + time.Nanosecond)
	if decision := tracker.BeforeClientRequest("office-key"); !decision.Allowed {
		t.Fatalf("BeforeClientRequest() after window expiry = %#v, want allowed", decision)
	}
}

func TestTrackerDualDimensionsAndRollingRetryAfter(t *testing.T) {
	now := time.Date(2026, 6, 12, 9, 0, 0, 0, time.UTC)
	tracker := NewTestTracker([]OutboundConfig{{
		Name: "primary", Windows: []WindowConfig{{Name: "hourly", Duration: time.Hour, MaxRequests: 10, MaxTokens: 100}},
	}}, func() time.Time { return now })
	tracker.RecordSuccess("primary", 60)
	now = now.Add(10 * time.Minute)
	tracker.RecordSuccess("primary", 50)

	decision := tracker.BeforeAttempt("primary")
	if decision.Allowed || !decision.RetryAfter.Equal(time.Date(2026, 6, 12, 10, 0, 0, 0, time.UTC)) {
		t.Fatalf("BeforeAttempt() = %#v, want token limit until first event expires", decision)
	}
	window := tracker.Snapshot()[0].Windows[0]
	if window.UsedRequests != 2 || window.UsedTokens != 110 || window.RemainingTokens != 0 || window.Used != window.UsedRequests {
		t.Fatalf("Snapshot window = %#v, want dual usage and request aliases", window)
	}
}

func TestTrackerFixedPeriods(t *testing.T) {
	location, err := time.LoadLocation("America/New_York")
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 3, 8, 1, 30, 0, 0, location)
	tracker := NewTestTracker([]OutboundConfig{{Name: "primary", Windows: []WindowConfig{
		{Name: "interval", Reset: "fixed", FixedPeriod: "interval", Duration: 2 * time.Hour, Anchor: time.Date(2026, 3, 8, 8, 0, 0, 0, time.UTC), MaxRequests: 1},
		{Name: "daily", Reset: "fixed", FixedPeriod: "daily", Time: "01:00", Timezone: "America/New_York", MaxRequests: 1},
		{Name: "weekly", Reset: "fixed", FixedPeriod: "weekly", Time: "01:00", Timezone: "America/New_York", Weekday: time.Sunday, MaxRequests: 1},
	}}}, func() time.Time { return now })
	tracker.RecordSuccess("primary")
	windows := tracker.Snapshot()[0].Windows
	if windows[0].ResetAt != "2026-03-08T08:00:00Z" { // now is before the anchor; floor selects 06:00Z-08:00Z.
		t.Fatalf("interval reset_at = %q", windows[0].ResetAt)
	}
	if windows[1].ResetAt != "2026-03-09T05:00:00Z" || windows[2].ResetAt != "2026-03-15T05:00:00Z" {
		t.Fatalf("civil reset_at daily=%q weekly=%q, want calendar boundaries across DST", windows[1].ResetAt, windows[2].ResetAt)
	}

	now = time.Date(2026, 3, 9, 1, 0, 0, 0, location)
	windows = tracker.Snapshot()[0].Windows
	if windows[1].UsedRequests != 0 || windows[2].UsedRequests != 1 {
		t.Fatalf("usage after daily boundary = %#v, want weekly retained", windows)
	}
}

func TestTrackerResetAllPreservesCooldownMetadata(t *testing.T) {
	now := time.Date(2026, 6, 12, 9, 30, 0, 0, time.UTC)
	tracker := NewTestTracker([]OutboundConfig{{
		Name: "primary", Cooldown: 2 * time.Hour, ProbeInterval: time.Hour,
		ResetAll: ResetAllConfig{Enabled: true, Period: "daily", Time: "10:00", Timezone: "UTC"},
		Windows:  []WindowConfig{{Name: "daily", Duration: 24 * time.Hour, MaxRequests: 2}},
	}}, func() time.Time { return now })
	tracker.RecordSuccess("primary")
	tracker.RecordQuotaExceeded("primary")
	now = time.Date(2026, 6, 12, 10, 0, 0, 0, time.UTC)
	item := tracker.Snapshot()[0]
	if item.Windows[0].UsedRequests != 0 || item.State != StateCooldown || item.LastQuotaExceededAt == "" || item.LastSuccessAt == "" {
		t.Fatalf("Snapshot() = %#v, reset-all must only clear window usage", item)
	}
}

func TestTrackerReconfigureCompatibility(t *testing.T) {
	now := time.Date(2026, 6, 12, 9, 0, 0, 0, time.UTC)
	base := OutboundConfig{Name: "primary", Cooldown: time.Hour, ProbeInterval: time.Minute, Windows: []WindowConfig{{Name: "window", Duration: time.Hour, MaxRequests: 2}}}
	tracker := NewTestTracker([]OutboundConfig{base}, func() time.Time { return now })
	tracker.RecordSuccess("primary", 20)
	tracker.RecordQuotaExceeded("primary")

	limitChanged := base
	limitChanged.Windows = []WindowConfig{{Name: "window", Duration: time.Hour, MaxRequests: 5, MaxTokens: 100}}
	if cleared := tracker.Reconfigure([]OutboundConfig{limitChanged}); cleared {
		t.Fatal("compatible limit change reported a reset")
	}
	if got := tracker.Snapshot()[0].Windows[0].UsedTokens; got != 20 {
		t.Fatalf("compatible usage = %d, want 20", got)
	}

	incompatible := limitChanged
	incompatible.Windows = []WindowConfig{{Name: "window", Duration: 2 * time.Hour, MaxRequests: 5}}
	if cleared := tracker.Reconfigure([]OutboundConfig{incompatible}); !cleared {
		t.Fatal("incompatible duration change did not report reset")
	}
	item := tracker.Snapshot()[0]
	if item.Windows[0].UsedRequests != 0 || item.State != StateCooldown {
		t.Fatalf("Snapshot() = %#v, want local usage reset and cooldown retained", item)
	}
}

func TestClientTrackerSnapshotReportsClientState(t *testing.T) {
	now := time.Date(2026, 6, 12, 9, 0, 0, 0, time.UTC)
	tracker := NewTestClientTracker([]ClientConfig{{
		Name:    "office-key",
		Inbound: "openai-entry",
		Windows: []WindowConfig{{Name: "hourly", Duration: time.Hour, MaxRequests: 1}},
	}}, func() time.Time { return now })
	tracker.RecordClientRequest("office-key")

	items := tracker.ClientSnapshot()
	if len(items) != 1 {
		t.Fatalf("len(ClientSnapshot()) = %d, want 1", len(items))
	}
	item := items[0]
	if item.Client != "office-key" || item.Inbound != "openai-entry" || item.Outbound != "" || item.State != StateLimited {
		t.Fatalf("ClientSnapshot()[0] = %#v, want limited client state", item)
	}
	if len(item.Windows) != 1 || item.Windows[0].Used != 1 || item.Windows[0].Remaining != 0 {
		t.Fatalf("ClientSnapshot()[0].Windows = %#v, want exhausted window", item.Windows)
	}
}
