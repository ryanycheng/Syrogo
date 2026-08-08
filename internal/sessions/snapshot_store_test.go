package sessions

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func TestSnapshotStoreRoundTripAndRecoveryNormalization(t *testing.T) {
	dir := t.TempDir()
	cfg := SnapshotConfig{Enabled: true, Dir: dir, FlushInterval: time.Hour}
	original := NewStore()
	now := time.Date(2026, 8, 1, 12, 0, 0, 0, time.UTC)
	original.now = func() time.Time { return now }
	_, _ = original.Register(Session{ID: "leased", ClientName: "client", InboundName: "inbound", Status: StatusRunning, Command: []string{"claude"}, HeartbeatCapability: HeartbeatCapabilityV1})
	_, _ = original.Register(Session{ID: "legacy", ClientName: "client", InboundName: "inbound", Status: StatusIdle})
	_, _ = original.Register(Session{ID: "stopped", Status: StatusStopped, LastSeenAt: now.Add(-30 * time.Minute)})
	_, _ = original.Register(Session{ID: "expired", Status: StatusStopped, LastSeenAt: now.Add(-2 * time.Hour)})

	writer, err := NewSnapshotStore(cfg, original)
	if err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(context.Background()); err != nil {
		t.Fatal(err)
	}

	recoveredAt := now.Add(5 * time.Minute)
	restoredStore := NewStore()
	restoredStore.now = func() time.Time { return recoveredAt }
	reader, err := NewSnapshotStore(cfg, restoredStore)
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close(context.Background())

	leased, ok := restoredStore.GetOwned("leased", "client", "inbound")
	if !ok || leased.Status != StatusUnknown || !leased.RecoveryPending || leased.RecoveredAt == nil || !leased.RecoveredAt.Equal(recoveredAt) {
		t.Fatalf("recovered leased session = %#v, ok=%v", leased, ok)
	}
	if leased.LastHeartbeatAt != nil || leased.LeaseExpiresAt == nil || !leased.LeaseExpiresAt.Equal(recoveredAt.Add(DefaultLeaseTTL)) {
		t.Fatalf("recovered lease = %#v", leased)
	}
	if _, ok := restoredStore.LatestActive("client", "inbound"); ok {
		t.Fatal("pending recovered session participated in LatestActive")
	}
	legacy, _ := restoredStore.GetOwned("legacy", "client", "inbound")
	if legacy.Status != StatusStopped || legacy.StoppedAt == nil || !legacy.StoppedAt.Equal(recoveredAt) || legacy.ExitCode != nil || !legacy.LastSeenAt.Equal(recoveredAt) {
		t.Fatalf("recovered legacy session = %#v", legacy)
	}
	if stopped, ok := restoredStore.GetOwned("stopped", "", ""); !ok || stopped.Status != StatusStopped || !stopped.LastSeenAt.Equal(now.Add(-30*time.Minute)) {
		t.Fatalf("restored stopped session = %#v, ok=%v", stopped, ok)
	}
	if _, ok := restoredStore.GetOwned("expired", "", ""); ok {
		t.Fatal("expired stopped session was restored")
	}
}

func TestSnapshotRecoveredHeartbeatClearsPendingAndKeepsUnknown(t *testing.T) {
	store := NewStore()
	now := time.Date(2026, 8, 1, 12, 0, 0, 0, time.UTC)
	store.now = func() time.Time { return now }
	store.importSnapshot([]persistedSession{{Session: Session{ID: "s1", ClientName: "client", InboundName: "inbound", Status: StatusRunning, HeartbeatCapability: HeartbeatCapabilityV1}}}, 7)

	now = now.Add(time.Second)
	updated, ok, err := store.Heartbeat("client", "inbound", "s1")
	if err != nil || !ok || updated.RecoveryPending || updated.Status != StatusUnknown {
		t.Fatalf("Heartbeat() = %#v, %v, %v", updated, ok, err)
	}
	if latest, ok := store.LatestActive("client", "inbound"); !ok || latest.ID != "s1" {
		t.Fatalf("LatestActive() = %#v, %v", latest, ok)
	}
}

func TestSnapshotRecoveredRegisterClearsPending(t *testing.T) {
	store := NewStore()
	now := time.Date(2026, 8, 1, 12, 0, 0, 0, time.UTC)
	store.now = func() time.Time { return now }
	store.importSnapshot([]persistedSession{{Session: Session{ID: "s1", ClientName: "client", InboundName: "inbound", Status: StatusRunning, HeartbeatCapability: HeartbeatCapabilityV1}}}, 7)

	now = now.Add(time.Second)
	updated, err := store.Register(Session{ID: "s1", ClientName: "client", InboundName: "inbound", HeartbeatCapability: HeartbeatCapabilityV1})
	if err != nil || updated.RecoveryPending || updated.Status != StatusUnknown {
		t.Fatalf("Register() = %#v, %v", updated, err)
	}
	if latest, ok := store.LatestActive("client", "inbound"); !ok || latest.ID != "s1" {
		t.Fatalf("LatestActive() = %#v, %v", latest, ok)
	}
}

func TestSnapshotStorePermissionsCorruptionAndUnknownVersion(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "sessions")
	store := NewStore()
	_, _ = store.Register(Session{ID: "s1"})
	cfg := SnapshotConfig{Enabled: true, Dir: dir, FlushInterval: time.Hour}
	snapshotStore, err := NewSnapshotStore(cfg, store)
	if err != nil {
		t.Fatal(err)
	}
	if err := snapshotStore.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	assertMode(t, dir, 0o700)
	assertMode(t, filepath.Join(dir, "latest.json"), 0o600)

	for name, payload := range map[string]string{
		"corrupt": "{",
		"unknown": `{"version":99,"sessions":[]}`,
	} {
		t.Run(name, func(t *testing.T) {
			caseDir := t.TempDir()
			if err := os.WriteFile(filepath.Join(caseDir, "latest.json"), []byte(payload), 0o600); err != nil {
				t.Fatal(err)
			}
			loaded, err := NewSnapshotStore(SnapshotConfig{Enabled: true, Dir: caseDir, FlushInterval: time.Hour}, NewStore())
			if err == nil || loaded != nil {
				t.Fatalf("NewSnapshotStore() = %#v, %v", loaded, err)
			}
		})
	}
}

func TestSnapshotStoreCloseFinalFlushAndConcurrentMutations(t *testing.T) {
	dir := t.TempDir()
	store := NewStore()
	snapshotStore, err := NewSnapshotStore(SnapshotConfig{Enabled: true, Dir: dir, FlushInterval: time.Hour}, store)
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _ = store.Register(Session{ID: "shared", ClientName: "client", InboundName: "inbound", Status: StatusRunning, HeartbeatCapability: HeartbeatCapabilityV1})
			_, _, _ = store.Heartbeat("client", "inbound", "shared")
			_ = snapshotStore.Save()
		}()
	}
	wg.Wait()
	if err := snapshotStore.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := snapshotStore.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	payload, err := os.ReadFile(filepath.Join(dir, "latest.json"))
	if err != nil {
		t.Fatal(err)
	}
	var snapshot sessionSnapshot
	if err := json.Unmarshal(payload, &snapshot); err != nil {
		t.Fatal(err)
	}
	if len(snapshot.Sessions) != 1 || snapshot.Sessions[0].ID != "shared" || snapshot.Generation != store.generation {
		t.Fatalf("final snapshot = %#v, generation = %d", snapshot, store.generation)
	}
}

func TestStoreMutationGenerationOnlyChangesOnMutation(t *testing.T) {
	store := NewStore()
	registered, _ := store.Register(Session{ID: "s1", ClientName: "client", InboundName: "inbound", Status: StatusStopped})
	generation := store.generation
	_, _ = store.Register(Session{ID: "s1", ClientName: "client", InboundName: "inbound", Status: StatusRunning})
	_, _ = store.MarkStopped("client", "inbound", "s1", 1)
	_, _ = store.ApplyHookEvent("client", "inbound", HookEvent{SessionID: "s1", EventName: "Stop"})
	if store.generation != generation {
		t.Fatalf("no-op operations changed generation from %d to %d; session %#v", generation, store.generation, registered)
	}
	store.Sweep(DefaultStoppedRetention, DefaultTransientTTL)
	if store.generation != generation {
		t.Fatalf("no-op sweep changed generation from %d to %d", generation, store.generation)
	}
}

func assertMode(t *testing.T, path string, want os.FileMode) {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != want {
		t.Fatalf("%s mode = %o, want %o", path, got, want)
	}
}
