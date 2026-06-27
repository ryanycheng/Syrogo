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

func TestStoreSummaryAggregatesTotalAndSpans(t *testing.T) {
	store := NewStore(10)
	store.Record(Trace{RequestID: "one", DurationMs: 100, Spans: []Span{{Name: "route_plan", DurationMs: 5}, {Name: "upstream_round_trip", DurationMs: 80}}})
	store.Record(Trace{RequestID: "two", DurationMs: 200, Spans: []Span{{Name: "route_plan", DurationMs: 15}, {Name: "upstream_round_trip", DurationMs: 160}}})
	store.Record(Trace{RequestID: "three", DurationMs: 300, Spans: []Span{{Name: "route_plan", DurationMs: 25}, {Name: "upstream_round_trip", DurationMs: 240}}})

	got := store.Summary()
	if got.Count != 3 || got.Total.Count != 3 || got.Total.AvgMs != 200 || got.Total.P50Ms != 200 || got.Total.P95Ms != 300 || got.Total.MaxMs != 300 {
		t.Fatalf("summary total = %#v, count=%d", got.Total, got.Count)
	}
	route := got.Spans["route_plan"]
	if route.Count != 3 || route.AvgMs != 15 || route.P50Ms != 15 || route.P99Ms != 25 || route.MaxMs != 25 {
		t.Fatalf("route_plan summary = %#v", route)
	}
	upstream := got.Spans["upstream_round_trip"]
	if upstream.Count != 3 || upstream.AvgMs != 160 || upstream.P50Ms != 160 || upstream.P95Ms != 240 || upstream.MaxMs != 240 {
		t.Fatalf("upstream summary = %#v", upstream)
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
