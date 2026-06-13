package quota

import (
	"testing"
	"time"
)

func TestEventRecorderKeepsLatestEvents(t *testing.T) {
	now := time.Date(2026, 6, 12, 9, 0, 0, 0, time.UTC)
	recorder := NewTestEventRecorder(2, func() time.Time { return now })

	recorder.Record(Event{Type: EventClientLimited, Client: "office-a"})
	recorder.Record(Event{Type: EventClientLimited, Client: "office-b"})
	recorder.Record(Event{Type: EventClientLimited, Client: "office-c"})

	events := recorder.Snapshot()
	if len(events) != 2 {
		t.Fatalf("len(events) = %d, want 2", len(events))
	}
	if events[0].Client != "office-b" || events[1].Client != "office-c" {
		t.Fatalf("events = %#v, want latest two events", events)
	}
	if events[0].Time == "" || events[1].Time == "" {
		t.Fatalf("events = %#v, want timestamps", events)
	}
}
