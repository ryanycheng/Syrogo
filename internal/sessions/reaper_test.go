package sessions

import (
	"context"
	"testing"
	"time"
)

func TestReaperSweepsAndCloses(t *testing.T) {
	store := NewStore()
	now := time.Date(2026, 7, 28, 12, 0, 0, 0, time.UTC)
	store.now = func() time.Time { return now }
	_, _ = store.Register(Session{ID: "leased", Status: StatusIdle, HeartbeatCapability: HeartbeatCapabilityV1})
	ticks := make(chan time.Time)
	reaper := newReaper(store, ticks, nil)

	now = now.Add(DefaultLeaseTTL)
	ticks <- now
	deadline := time.Now().Add(time.Second)
	for {
		items := store.List(ListFilter{})
		if len(items) == 1 && items[0].Status == StatusStopped {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("reaper did not sweep leased session")
		}
	}

	if err := reaper.Close(context.Background()); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	if err := reaper.Close(context.Background()); err != nil {
		t.Fatalf("second Close() error = %v", err)
	}
}
