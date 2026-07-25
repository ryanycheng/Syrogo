package quota

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
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

	payload, err := os.ReadFile(filepath.Join(cfg.Dir, "latest.json"))
	if err != nil {
		t.Fatal(err)
	}
	var raw map[string]any
	if err := json.Unmarshal(payload, &raw); err != nil {
		t.Fatal(err)
	}
	if raw["version"] != float64(2) {
		t.Fatalf("snapshot version = %#v, want 2", raw["version"])
	}
}

func TestSnapshotStoreWithoutLoadPreservesLiveTracker(t *testing.T) {
	now := time.Date(2026, 6, 12, 9, 0, 0, 0, time.UTC)
	dir := t.TempDir()
	cfg := config.GovernanceQuotaSnapshotConfig{Enabled: true, Dir: dir, FlushInterval: config.DurationValue("1h")}
	stale := NewTestTracker([]OutboundConfig{{Name: "primary", Windows: []WindowConfig{{Name: "hourly", Duration: time.Hour, MaxRequests: 10}}}}, func() time.Time { return now })
	initial, err := NewSnapshotStore(cfg, stale, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := initial.Save(); err != nil {
		t.Fatal(err)
	}
	if err := initial.Close(context.Background()); err != nil {
		t.Fatal(err)
	}

	live := NewTestTracker([]OutboundConfig{{Name: "primary", Windows: []WindowConfig{{Name: "hourly", Duration: time.Hour, MaxRequests: 10}}}}, func() time.Time { return now })
	live.RecordSuccess("primary")
	store, err := NewSnapshotStoreWithoutLoad(cfg, live, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = store.Close(context.Background()) }()
	if got := live.Snapshot()[0].Windows[0].UsedRequests; got != 1 {
		t.Fatalf("live usage after constructor = %d, want 1", got)
	}
}

func TestSnapshotStoreLoadsLegacyStringEvents(t *testing.T) {
	now := time.Date(2026, 6, 12, 9, 0, 0, 0, time.UTC)
	dir := t.TempDir()
	legacy := `{
  "captured_at":"2026-06-12T09:00:00Z",
  "outbound":{"captured_at":"2026-06-12T09:00:00Z","subjects":{"primary":{"name":"primary","windows":{"hourly":{"events":["2026-06-12T08:30:00Z"]}}}}},
  "client":{"subjects":{}}
}`
	if err := os.WriteFile(filepath.Join(dir, "latest.json"), []byte(legacy), 0o644); err != nil {
		t.Fatal(err)
	}
	tracker := NewTestTracker([]OutboundConfig{{Name: "primary", Windows: []WindowConfig{{Name: "hourly", Duration: time.Hour, MaxRequests: 1, MaxTokens: 10}}}}, func() time.Time { return now })
	cfg := config.GovernanceQuotaSnapshotConfig{Enabled: true, Dir: dir, FlushInterval: config.DurationValue("1h")}
	store, err := NewSnapshotStore(cfg, tracker, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = store.Close(context.Background()) }()
	window := tracker.Snapshot()[0].Windows[0]
	if window.UsedRequests != 1 || window.UsedTokens != 0 {
		t.Fatalf("legacy restored window = %#v", window)
	}
}

func TestImportIgnoresIncompatibleV2Window(t *testing.T) {
	now := time.Date(2026, 6, 12, 9, 0, 0, 0, time.UTC)
	tracker := NewTestTracker([]OutboundConfig{{Name: "primary", Windows: []WindowConfig{{Name: "window", Duration: time.Hour, MaxRequests: 1}}}}, func() time.Time { return now })
	tracker.ImportState(PersistedState{Version: 2, Subjects: map[string]PersistedSubject{"primary": {Windows: map[string]PersistedWindowState{"window": {Reset: "rolling", Schedule: "2h0m0s", Events: []PersistedEvent{{At: "2026-06-12T08:30:00Z", Requests: 1}}}}}}})
	if got := tracker.Snapshot()[0].Windows[0].UsedRequests; got != 0 {
		t.Fatalf("incompatible imported usage = %d, want 0", got)
	}
}
