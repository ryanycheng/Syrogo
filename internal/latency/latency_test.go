package latency

import (
	"context"
	"testing"
	"time"
)

func TestStoreKeepsRecentTraces(t *testing.T) {
	store := NewStore(2)
	store.Record(Trace{RequestID: "one"})
	store.Record(Trace{RequestID: "two"})
	store.Record(Trace{RequestID: "three"})

	got := store.Snapshot()
	if len(got.Items) != 2 {
		t.Fatalf("items len = %d, want 2", len(got.Items))
	}
	if got.Items[0].RequestID != "two" || got.Items[1].RequestID != "three" {
		t.Fatalf("items = %#v, want latest two", got.Items)
	}
}

func TestRecorderStoresFinishedTraceWithSpans(t *testing.T) {
	store := NewStore(10)
	startedAt := time.Now().Add(-10 * time.Millisecond)
	ctx, recorder := Start(context.Background(), store, "req-1", "POST", "/v1/chat/completions", startedAt)
	recorder.SetRoute("openai-entry", "openai_chat", "office-key", "office")
	RecordSpan(ctx, "route_plan", time.Now().Add(-2*time.Millisecond), map[string]string{"outbound": "mock"})
	recorder.Finish(200, time.Now())

	got := store.Snapshot()
	if len(got.Items) != 1 {
		t.Fatalf("items len = %d, want 1", len(got.Items))
	}
	item := got.Items[0]
	if item.RequestID != "req-1" || item.Status != 200 || item.Inbound != "openai-entry" || item.ClientName != "office-key" {
		t.Fatalf("trace = %#v, want request metadata", item)
	}
	if len(item.Spans) != 1 || item.Spans[0].Name != "route_plan" || item.Spans[0].Attrs["outbound"] != "mock" {
		t.Fatalf("spans = %#v, want route_plan span", item.Spans)
	}
}
