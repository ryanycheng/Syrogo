package provider

import (
	"testing"
	"time"
)

func TestHealthTrackerRecordsFailureAndRecovery(t *testing.T) {
	now := time.Date(2026, 6, 13, 9, 0, 0, 0, time.UTC)
	tracker := NewTestHealthTracker([]string{"backup", "primary"}, func() time.Time { return now })

	tracker.RecordFailure("primary", ErrorKindTimeout)
	tracker.RecordFailure("primary", ErrorKindQuotaExceeded)
	items := tracker.Snapshot()
	if len(items) != 2 {
		t.Fatalf("len(Snapshot()) = %d, want 2", len(items))
	}
	if items[1].Outbound != "primary" || items[1].State != HealthDegraded || items[1].LastErrorKind != string(ErrorKindQuotaExceeded) || items[1].ConsecutiveFailures != 2 || items[1].LastFailureAt == "" {
		t.Fatalf("primary item = %#v, want degraded primary", items[1])
	}

	now = now.Add(time.Minute)
	tracker.RecordSuccess("primary")
	items = tracker.Snapshot()
	if items[1].State != HealthAvailable || items[1].ConsecutiveFailures != 0 || items[1].LastErrorKind != "" || items[1].LastSuccessAt == "" {
		t.Fatalf("primary item after success = %#v, want recovered primary", items[1])
	}
}

func TestHealthTrackerBlocksAfterFailureThreshold(t *testing.T) {
	tracker := NewTestHealthTracker([]string{"primary"}, func() time.Time { return time.Date(2026, 6, 13, 9, 0, 0, 0, time.UTC) })

	for range DefaultHealthFailureThreshold {
		if decision := tracker.BeforeAttempt("primary"); !decision.Allowed {
			t.Fatalf("BeforeAttempt() before threshold = %#v, want allowed", decision)
		}
		tracker.RecordFailure("primary", ErrorKindTimeout)
	}
	if decision := tracker.BeforeAttempt("primary"); decision.Allowed || decision.Reason != HealthDegraded {
		t.Fatalf("BeforeAttempt() after threshold = %#v, want degraded block", decision)
	}

	tracker.RecordSuccess("primary")
	if decision := tracker.BeforeAttempt("primary"); !decision.Allowed {
		t.Fatalf("BeforeAttempt() after success = %#v, want allowed", decision)
	}
}
