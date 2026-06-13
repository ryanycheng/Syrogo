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
	if items[1].State != HealthAvailable || items[1].ConsecutiveFailures != 0 || items[1].LastErrorKind != "" || items[1].LastSuccessAt == "" || items[1].NextProbeAt != "" {
		t.Fatalf("primary item after success = %#v, want recovered primary", items[1])
	}
}

func TestHealthTrackerProbesAfterFailureThreshold(t *testing.T) {
	now := time.Date(2026, 6, 13, 9, 0, 0, 0, time.UTC)
	tracker := NewTestHealthTrackerWithProbeInterval([]string{"primary"}, func() time.Time { return now }, 30*time.Second)

	for range DefaultHealthFailureThreshold {
		if decision := tracker.BeforeAttempt("primary"); !decision.Allowed {
			t.Fatalf("BeforeAttempt() before threshold = %#v, want allowed", decision)
		}
		tracker.RecordFailure("primary", ErrorKindTimeout)
	}
	if decision := tracker.BeforeAttempt("primary"); decision.Allowed || decision.Reason != HealthDegraded || decision.RetryAfter.IsZero() {
		t.Fatalf("BeforeAttempt() before probe = %#v, want degraded block", decision)
	}
	items := tracker.Snapshot()
	if len(items) != 1 || items[0].State != HealthDegraded || items[0].NextProbeAt == "" {
		t.Fatalf("Snapshot() = %#v, want degraded with next probe", items)
	}

	now = now.Add(30 * time.Second)
	decision := tracker.BeforeAttempt("primary")
	if !decision.Allowed || !decision.Probe || decision.Reason != HealthProbing {
		t.Fatalf("BeforeAttempt() at probe = %#v, want probing allowed", decision)
	}
	items = tracker.Snapshot()
	if len(items) != 1 || items[0].State != HealthProbing {
		t.Fatalf("Snapshot() during probe = %#v, want probing", items)
	}

	tracker.RecordFailure("primary", ErrorKindUpstreamServerError)
	items = tracker.Snapshot()
	if len(items) != 1 || items[0].State != HealthDegraded || items[0].ConsecutiveFailures != DefaultHealthFailureThreshold+1 {
		t.Fatalf("Snapshot() after failed probe = %#v, want degraded", items)
	}
	if decision := tracker.BeforeAttempt("primary"); decision.Allowed {
		t.Fatalf("BeforeAttempt() after failed probe = %#v, want blocked", decision)
	}

	now = now.Add(30 * time.Second)
	if decision := tracker.BeforeAttempt("primary"); !decision.Allowed || !decision.Probe {
		t.Fatalf("BeforeAttempt() second probe = %#v, want probe", decision)
	}
	tracker.RecordSuccess("primary")
	if decision := tracker.BeforeAttempt("primary"); !decision.Allowed || decision.Probe {
		t.Fatalf("BeforeAttempt() after success = %#v, want normal allowed", decision)
	}
}

func TestHealthTrackerIgnoresNonRecoverableFailures(t *testing.T) {
	tracker := NewTestHealthTracker([]string{"primary"}, func() time.Time { return time.Date(2026, 6, 13, 9, 0, 0, 0, time.UTC) })

	tracker.RecordFailure("primary", ErrorKindAuthFailed)
	tracker.RecordFailure("primary", ErrorKindCapabilityUnsupported)
	tracker.RecordFailure("primary", ErrorKindFatal)
	items := tracker.Snapshot()
	if len(items) != 1 || items[0].State != HealthAvailable || items[0].ConsecutiveFailures != 0 || items[0].LastErrorKind != "" {
		t.Fatalf("Snapshot() = %#v, want non-recoverable failures ignored", items)
	}
}
