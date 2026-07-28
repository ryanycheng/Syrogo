package sessions

import (
	"errors"
	"sync"
	"testing"
	"time"
)

func TestStoreRegisterListAndFilter(t *testing.T) {
	store := NewStore()
	startedAt := time.Date(2026, 7, 13, 10, 0, 0, 0, time.UTC)
	store.now = func() time.Time { return startedAt }

	_, _ = store.Register(Session{
		ID:          "session-a",
		ClientName:  "alice",
		InboundName: "claude",
		Host:        "devbox",
		CWD:         "/repo/a",
		Command:     []string{"claude"},
	})
	_, _ = store.Register(Session{
		ID:         "session-b",
		ClientName: "bob",
		Host:       "worker",
		CWD:        "/repo/b",
		Status:     StatusRunning,
	})

	sessions := store.List(ListFilter{Client: "alice"})
	if len(sessions) != 1 || sessions[0].ID != "session-a" {
		t.Fatalf("expected only session-a, got %#v", sessions)
	}
	if sessions[0].Status != StatusUnknown {
		t.Fatalf("expected default unknown status, got %s", sessions[0].Status)
	}
	if sessions[0].StartedAt != startedAt || sessions[0].LastSeenAt != startedAt {
		t.Fatalf("expected timestamps to default to now")
	}
}

func TestStoreRegisterRejectsCrossOwnerReplacement(t *testing.T) {
	store := NewStore()
	original, err := store.Register(Session{ID: "s1", ClientName: "first-client", InboundName: "first-inbound", Tag: "original"})
	if err != nil {
		t.Fatalf("first Register() error = %v", err)
	}

	if _, err := store.Register(Session{ID: "s1", ClientName: "second-client", InboundName: "first-inbound", Tag: "client-attack"}); !errors.Is(err, ErrSessionOwnerMismatch) {
		t.Fatalf("cross-client Register() error = %v, want ErrSessionOwnerMismatch", err)
	}
	if _, err := store.Register(Session{ID: "s1", ClientName: "first-client", InboundName: "second-inbound", Tag: "inbound-attack"}); !errors.Is(err, ErrSessionOwnerMismatch) {
		t.Fatalf("cross-inbound Register() error = %v, want ErrSessionOwnerMismatch", err)
	}

	owned, ok := store.GetOwned("s1", "first-client", "first-inbound")
	if !ok || owned.Tag != original.Tag {
		t.Fatalf("original session was replaced: %#v, ok=%v", owned, ok)
	}

	repeated, err := store.Register(Session{ID: "s1", ClientName: "first-client", InboundName: "first-inbound", Tag: "refreshed"})
	if err != nil {
		t.Fatalf("same-owner Register() error = %v", err)
	}
	if repeated.Tag != "refreshed" {
		t.Fatalf("same-owner registration did not update session: %#v", repeated)
	}
}

func TestHookEventStatusTransitions(t *testing.T) {
	store := NewStore()
	_, _ = store.Register(Session{ID: "s1", ClientName: "client", InboundName: "inbound", Status: StatusRunning})

	updated, ok := store.ApplyHookEvent("client", "inbound", HookEvent{SessionID: "s1", EventName: "PreToolUse"})
	if !ok || updated.Status != StatusToolRunning {
		t.Fatalf("expected tool_running, got %#v ok=%v", updated, ok)
	}

	updated, ok = store.ApplyHookEvent("client", "inbound", HookEvent{SessionID: "s1", EventName: "Stop"})
	if !ok || updated.Status != StatusIdle {
		t.Fatalf("expected Stop to mark idle, got %#v ok=%v", updated, ok)
	}
	if updated.StoppedAt != nil {
		t.Fatalf("Stop hook must not mark process stopped")
	}

	updated, ok = store.MarkStopped("client", "inbound", "s1", 0)
	if !ok || updated.Status != StatusStopped {
		t.Fatalf("expected MarkStopped to mark stopped, got %#v ok=%v", updated, ok)
	}
	if updated.StoppedAt == nil || updated.ExitCode == nil || *updated.ExitCode != 0 {
		t.Fatalf("expected stopped metadata, got %#v", updated)
	}
}

func TestStoreRejectsCrossOwnerUpdates(t *testing.T) {
	store := NewStore()
	_, _ = store.Register(Session{ID: "s1", ClientName: "shared", InboundName: "first", Tag: "first-tag", Status: StatusRunning})

	if _, ok := store.ApplyHookEvent("shared", "second", HookEvent{SessionID: "s1", EventName: "Stop"}); ok {
		t.Fatal("ApplyHookEvent accepted a different inbound owner")
	}
	if _, ok := store.MarkStopped("other", "first", "s1", 0); ok {
		t.Fatal("MarkStopped accepted a different client owner")
	}

	items := store.List(ListFilter{})
	if len(items) != 1 || items[0].Status != StatusRunning || items[0].Tag != "first-tag" {
		t.Fatalf("session changed after rejected updates: %#v", items)
	}
}

func TestPermissionNotification(t *testing.T) {
	if got := StatusForHookEvent(HookEvent{EventName: "PreToolUse", Payload: map[string]any{"permission_required": true}}, StatusRunning); got != StatusWaitingPermission {
		t.Fatalf("expected waiting_permission, got %s", got)
	}
	if got := StatusForHookEvent(HookEvent{EventName: "Notification", Payload: map[string]any{"message": "Claude needs permission approval"}}, StatusRunning); got != StatusWaitingPermission {
		t.Fatalf("expected notification to mark waiting_permission, got %s", got)
	}
}

func TestStoreConcurrentUpdates(t *testing.T) {
	store := NewStore()
	_, _ = store.Register(Session{ID: "s1", ClientName: "client", InboundName: "inbound"})

	var wg sync.WaitGroup
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			store.ApplyHookEvent("client", "inbound", HookEvent{SessionID: "s1", EventName: "UserPromptSubmit"})
			store.ApplyHookEvent("client", "inbound", HookEvent{SessionID: "s1", EventName: "Stop"})
		}()
	}
	wg.Wait()

	sessions := store.List(ListFilter{})
	if len(sessions) != 1 {
		t.Fatalf("expected one session, got %d", len(sessions))
	}
	if sessions[0].Status != StatusIdle && sessions[0].Status != StatusRunning {
		t.Fatalf("unexpected final status %s", sessions[0].Status)
	}
}

func TestStoreListPrioritizesAttentionThenRecentActivity(t *testing.T) {
	store := NewStore()
	now := time.Date(2026, 7, 18, 10, 0, 0, 0, time.UTC)
	store.now = func() time.Time { return now }

	_, _ = store.Register(Session{ID: "tool-oldest", Status: StatusToolRunning, LastSeenAt: now.Add(-30 * time.Minute)})
	_, _ = store.Register(Session{ID: "running-older", Status: StatusRunning, LastSeenAt: now.Add(-20 * time.Minute)})
	_, _ = store.Register(Session{ID: "permission-older", Status: StatusWaitingPermission, LastSeenAt: now.Add(-15 * time.Minute)})
	_, _ = store.Register(Session{ID: "permission-newer", Status: StatusWaitingPermission, LastSeenAt: now.Add(-10 * time.Minute)})
	_, _ = store.Register(Session{ID: "stopped-next", Status: StatusStopped, LastSeenAt: now.Add(-2 * time.Minute)})
	_, _ = store.Register(Session{ID: "idle-notification-newest", Status: StatusIdle, LastEvent: "Notification", LastSeenAt: now.Add(-time.Minute)})

	sessions := store.List(ListFilter{})
	want := []string{
		"permission-newer",
		"permission-older",
		"idle-notification-newest",
		"stopped-next",
		"running-older",
		"tool-oldest",
	}
	if len(sessions) != len(want) {
		t.Fatalf("got %d sessions, want %d", len(sessions), len(want))
	}
	for i, id := range want {
		if sessions[i].ID != id {
			t.Fatalf("session[%d] = %s, want %s; got %#v", i, sessions[i].ID, id, sessions)
		}
	}
}

func TestStorePruneStoppedOnlyRemovesExpiredStoppedSessions(t *testing.T) {
	store := NewStore()
	now := time.Date(2026, 7, 18, 10, 0, 0, 0, time.UTC)
	store.now = func() time.Time { return now }

	_, _ = store.Register(Session{ID: "old-stopped", Status: StatusStopped, LastSeenAt: now.Add(-2 * time.Hour)})
	_, _ = store.Register(Session{ID: "new-stopped", Status: StatusStopped, LastSeenAt: now.Add(-30 * time.Minute)})
	_, _ = store.Register(Session{ID: "old-running", Status: StatusRunning, LastSeenAt: now.Add(-2 * time.Hour)})

	if removed := store.PruneStopped(time.Hour); removed != 1 {
		t.Fatalf("removed = %d, want 1", removed)
	}

	sessions := store.List(ListFilter{})
	ids := map[string]bool{}
	for _, session := range sessions {
		ids[session.ID] = true
	}
	for _, id := range []string{"new-stopped", "old-running"} {
		if !ids[id] {
			t.Fatalf("expected %s to remain, got %#v", id, sessions)
		}
	}
	if ids["old-stopped"] {
		t.Fatalf("expected old-stopped to be pruned, got %#v", sessions)
	}
}

func TestStoreHeartbeatAndSweepLifecycle(t *testing.T) {
	store := NewStore()
	now := time.Date(2026, 7, 28, 12, 0, 0, 0, time.UTC)
	store.now = func() time.Time { return now }
	lastSeen := now.Add(-time.Minute)

	registered, err := store.Register(Session{
		ID:                  "leased",
		ClientName:          "client",
		InboundName:         "inbound",
		Status:              StatusIdle,
		LastSeenAt:          lastSeen,
		HeartbeatCapability: HeartbeatCapabilityV1,
	})
	if err != nil || registered.LastHeartbeatAt == nil || registered.LeaseExpiresAt == nil {
		t.Fatalf("Register() = %#v, %v", registered, err)
	}
	if got := registered.LeaseExpiresAt.Sub(*registered.LastHeartbeatAt); got != DefaultLeaseTTL {
		t.Fatalf("lease duration = %v, want %v", got, DefaultLeaseTTL)
	}

	now = now.Add(10 * time.Second)
	heartbeat, ok, err := store.Heartbeat("client", "inbound", "leased")
	if err != nil || !ok {
		t.Fatalf("Heartbeat() = %#v, %v, %v", heartbeat, ok, err)
	}
	if heartbeat.LastSeenAt != lastSeen {
		t.Fatalf("heartbeat changed LastSeenAt to %v", heartbeat.LastSeenAt)
	}

	now = heartbeat.LeaseExpiresAt.Add(-time.Nanosecond)
	if result := store.Sweep(DefaultStoppedRetention, DefaultTransientTTL); result.LeaseExpired != 0 {
		t.Fatalf("Sweep() before expiry = %#v", result)
	}
	now = heartbeat.LeaseExpiresAt.Add(0)
	if result := store.Sweep(DefaultStoppedRetention, DefaultTransientTTL); result.LeaseExpired != 1 {
		t.Fatalf("Sweep() at expiry = %#v", result)
	}
	stopped, ok := store.GetOwned("leased", "client", "inbound")
	if !ok || stopped.Status != StatusStopped || stopped.StoppedAt == nil || stopped.ExitCode != nil {
		t.Fatalf("expired session = %#v, ok=%v", stopped, ok)
	}
	if _, ok := store.LatestActive("client", "inbound"); ok {
		t.Fatal("expired session remained active")
	}
}

func TestStoreSweepDegradesTransientStatesOnly(t *testing.T) {
	store := NewStore()
	now := time.Date(2026, 7, 28, 12, 0, 0, 0, time.UTC)
	store.now = func() time.Time { return now }
	lastSeen := now.Add(-time.Hour)
	for _, session := range []Session{
		{ID: "tool", Status: StatusToolRunning, LastSeenAt: lastSeen},
		{ID: "compact", Status: StatusCompacting, LastSeenAt: lastSeen},
		{ID: "permission", Status: StatusWaitingPermission, LastSeenAt: lastSeen},
		{ID: "legacy-idle", Status: StatusIdle, LastSeenAt: lastSeen},
	} {
		if _, err := store.Register(session); err != nil {
			t.Fatal(err)
		}
	}
	now = now.Add(DefaultTransientTTL)
	result := store.Sweep(DefaultStoppedRetention, DefaultTransientTTL)
	if result.TransientDegraded != 2 || result.LeaseExpired != 0 {
		t.Fatalf("Sweep() = %#v", result)
	}
	items := store.List(ListFilter{})
	statuses := map[string]Status{}
	for _, item := range items {
		statuses[item.ID] = item.Status
		if item.LastSeenAt != lastSeen {
			t.Fatalf("Sweep changed %s LastSeenAt to %v", item.ID, item.LastSeenAt)
		}
	}
	if statuses["tool"] != StatusUnknown || statuses["compact"] != StatusUnknown || statuses["permission"] != StatusWaitingPermission || statuses["legacy-idle"] != StatusIdle {
		t.Fatalf("statuses after Sweep = %#v", statuses)
	}
}

func TestStoreStoppedIsAbsorbing(t *testing.T) {
	store := NewStore()
	now := time.Date(2026, 7, 28, 12, 0, 0, 0, time.UTC)
	store.now = func() time.Time { return now }
	_, _ = store.Register(Session{ID: "s1", ClientName: "client", InboundName: "inbound", Status: StatusRunning, HeartbeatCapability: HeartbeatCapabilityV1})
	stopped, _ := store.MarkStopped("client", "inbound", "s1", 0)

	now = now.Add(time.Minute)
	if updated, ok := store.ApplyHookEvent("client", "inbound", HookEvent{SessionID: "s1", EventName: "UserPromptSubmit", ReceivedAt: now}); !ok || updated.Status != StatusStopped || *updated.StoppedAt != *stopped.StoppedAt {
		t.Fatalf("late hook changed stopped session: %#v", updated)
	}
	if updated, ok, err := store.Heartbeat("client", "inbound", "s1"); err != nil || !ok || updated.Status != StatusStopped || *updated.LeaseExpiresAt != *stopped.LeaseExpiresAt {
		t.Fatalf("late heartbeat changed stopped session: %#v, %v", updated, err)
	}
	if updated, err := store.Register(Session{ID: "s1", ClientName: "client", InboundName: "inbound", Status: StatusRunning}); err != nil || updated.Status != StatusStopped || *updated.StoppedAt != *stopped.StoppedAt {
		t.Fatalf("repeat register changed stopped session: %#v, %v", updated, err)
	}
	if updated, ok := store.MarkStopped("client", "inbound", "s1", 9); !ok || updated.ExitCode == nil || *updated.ExitCode != 0 || *updated.StoppedAt != *stopped.StoppedAt {
		t.Fatalf("repeat stopped changed session: %#v", updated)
	}
}

func TestStoreActiveRegisterRetryPreservesLifecycle(t *testing.T) {
	store := NewStore()
	now := time.Date(2026, 7, 28, 12, 0, 0, 0, time.UTC)
	store.now = func() time.Time { return now }
	_, _ = store.Register(Session{ID: "s1", ClientName: "client", InboundName: "inbound", Status: StatusRunning, Host: "old"})
	now = now.Add(time.Minute)
	active, _ := store.ApplyHookEvent("client", "inbound", HookEvent{SessionID: "s1", EventName: "PreToolUse", ReceivedAt: now})
	now = now.Add(time.Minute)
	repeated, err := store.Register(Session{ID: "s1", ClientName: "client", InboundName: "inbound", Host: "new", HeartbeatCapability: HeartbeatCapabilityV1})
	if err != nil {
		t.Fatal(err)
	}
	if repeated.Status != StatusToolRunning || repeated.LastEvent != "PreToolUse" || repeated.LastSeenAt != active.LastSeenAt || repeated.Host != "new" || repeated.LeaseExpiresAt == nil {
		t.Fatalf("repeat registration lost lifecycle: %#v", repeated)
	}
}
