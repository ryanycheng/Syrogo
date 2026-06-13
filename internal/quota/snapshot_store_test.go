package quota

import (
	"context"
	"testing"
	"time"

	"github.com/ryanycheng/Syrogo/internal/config"
)

func TestSnapshotStoreSavesAndLoadsTrackers(t *testing.T) {
	now := time.Date(2026, 6, 12, 9, 0, 0, 0, time.UTC)
	outbound := NewTestTracker([]OutboundConfig{{
		Name:          "primary",
		Cooldown:      10 * time.Minute,
		ProbeInterval: time.Minute,
		Windows:       []WindowConfig{{Name: "hourly", Duration: time.Hour, MaxRequests: 1}},
	}}, func() time.Time { return now })
	client := NewTestClientTracker([]ClientConfig{{
		Name:    "office-key",
		Inbound: "openai-entry",
		Windows: []WindowConfig{{Name: "hourly", Duration: time.Hour, MaxRequests: 1}},
	}}, func() time.Time { return now })
	outbound.RecordSuccess("primary")
	client.RecordClientRequest("office-key")

	cfg := config.GovernanceQuotaSnapshotConfig{Enabled: true, Dir: t.TempDir(), FlushInterval: config.DurationValue("1h")}
	store, err := NewSnapshotStore(cfg, outbound, client)
	if err != nil {
		t.Fatalf("NewSnapshotStore() error = %v", err)
	}
	if err := store.Save(); err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	if err := store.Close(context.Background()); err != nil {
		t.Fatalf("Close() error = %v", err)
	}

	restoredOutbound := NewTestTracker([]OutboundConfig{{
		Name:          "primary",
		Cooldown:      10 * time.Minute,
		ProbeInterval: time.Minute,
		Windows:       []WindowConfig{{Name: "hourly", Duration: time.Hour, MaxRequests: 1}},
	}}, func() time.Time { return now })
	restoredClient := NewTestClientTracker([]ClientConfig{{
		Name:    "office-key",
		Inbound: "openai-entry",
		Windows: []WindowConfig{{Name: "hourly", Duration: time.Hour, MaxRequests: 1}},
	}}, func() time.Time { return now })
	restored, err := NewSnapshotStore(cfg, restoredOutbound, restoredClient)
	if err != nil {
		t.Fatalf("NewSnapshotStore() restore error = %v", err)
	}
	defer func() { _ = restored.Close(context.Background()) }()

	if decision := restoredOutbound.BeforeAttempt("primary"); decision.Allowed || decision.Reason != StateLimited {
		t.Fatalf("restored outbound decision = %#v, want limited", decision)
	}
	if decision := restoredClient.BeforeClientRequest("office-key"); decision.Allowed || decision.Reason != StateLimited {
		t.Fatalf("restored client decision = %#v, want limited", decision)
	}
}
