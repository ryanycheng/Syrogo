package quota

import (
	"testing"
	"time"
)

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
