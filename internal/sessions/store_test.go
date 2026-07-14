package sessions

import (
	"sync"
	"testing"
	"time"
)

func TestStoreRegisterListAndFilter(t *testing.T) {
	store := NewStore()
	startedAt := time.Date(2026, 7, 13, 10, 0, 0, 0, time.UTC)
	store.now = func() time.Time { return startedAt }

	store.Register(Session{
		ID:          "session-a",
		ClientName:  "alice",
		InboundName: "claude",
		Host:        "devbox",
		CWD:         "/repo/a",
		Command:     []string{"claude"},
	})
	store.Register(Session{
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

func TestHookEventStatusTransitions(t *testing.T) {
	store := NewStore()
	store.Register(Session{ID: "s1", Status: StatusRunning})

	updated, ok := store.ApplyHookEvent(HookEvent{SessionID: "s1", EventName: "PreToolUse"})
	if !ok || updated.Status != StatusToolRunning {
		t.Fatalf("expected tool_running, got %#v ok=%v", updated, ok)
	}

	updated, ok = store.ApplyHookEvent(HookEvent{SessionID: "s1", EventName: "Stop"})
	if !ok || updated.Status != StatusIdle {
		t.Fatalf("expected Stop to mark idle, got %#v ok=%v", updated, ok)
	}
	if updated.StoppedAt != nil {
		t.Fatalf("Stop hook must not mark process stopped")
	}

	updated, ok = store.MarkStopped("s1", 0)
	if !ok || updated.Status != StatusStopped {
		t.Fatalf("expected MarkStopped to mark stopped, got %#v ok=%v", updated, ok)
	}
	if updated.StoppedAt == nil || updated.ExitCode == nil || *updated.ExitCode != 0 {
		t.Fatalf("expected stopped metadata, got %#v", updated)
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
	store.Register(Session{ID: "s1"})

	var wg sync.WaitGroup
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			store.ApplyHookEvent(HookEvent{SessionID: "s1", EventName: "UserPromptSubmit"})
			store.ApplyHookEvent(HookEvent{SessionID: "s1", EventName: "Stop"})
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
