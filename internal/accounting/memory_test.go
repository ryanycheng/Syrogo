package accounting

import (
	"testing"
	"time"

	"github.com/ryanycheng/Syrogo/internal/runtime"
)

func TestMemoryStoreReportsEphemeralCoverage(t *testing.T) {
	startedAt := time.Date(2026, 4, 27, 12, 30, 0, 123, time.FixedZone("UTC+8", 8*60*60))
	store := newMemoryStore(startedAt, true)
	coverage := store.Coverage()
	if coverage.TrackingStartedAt != startedAt.UTC().Format(time.RFC3339Nano) || !coverage.Known || coverage.Backend != "memory" || coverage.AggregatesPersisted || coverage.RawRetentionDays != 0 {
		t.Fatalf("Coverage() = %#v, want known ephemeral memory coverage", coverage)
	}
}

func TestMemoryStoreClientDateQueryIsolatesClientsAcrossDays(t *testing.T) {
	store := NewMemoryStore()
	for _, record := range []runtime.UsageRecord{
		testUsageRecord("client-a", time.Date(2026, 4, 26, 20, 0, 0, 0, time.FixedZone("UTC-7", -7*60*60))),
		testUsageRecord("client-b", time.Date(2026, 4, 27, 12, 0, 0, 0, time.UTC)),
		testUsageRecord("client-a", time.Date(2026, 4, 28, 23, 59, 0, 0, time.UTC)),
		testUsageRecord("client-b", time.Date(2026, 4, 29, 0, 0, 0, 0, time.UTC)),
	} {
		store.Record(record)
	}

	items, err := store.Query(Query{
		ClientName: "client-a",
		GroupBy:    "date",
		StartDate:  "2026-04-27",
		EndDate:    "2026-04-29",
	})
	if err != nil {
		t.Fatalf("Query() error = %v", err)
	}
	if len(items) != 2 {
		t.Fatalf("len(items) = %d, want 2: %#v", len(items), items)
	}
	if items[0].Value != "2026-04-27" || items[0].RequestCount != 1 || items[0].TotalTokens != 15 {
		t.Fatalf("items[0] = %#v, want client-a UTC day 2026-04-27", items[0])
	}
	if items[1].Value != "2026-04-28" || items[1].RequestCount != 1 || items[1].TotalTokens != 15 {
		t.Fatalf("items[1] = %#v, want client-a UTC day 2026-04-28", items[1])
	}
}

func TestMemoryStoreRejectsUnsupportedClientQueries(t *testing.T) {
	store := NewMemoryStore()
	for _, query := range []Query{
		{ClientName: "client-a", GroupBy: "key", StartDate: "2026-04-27", EndDate: "2026-04-28"},
		{ClientName: "client-a", GroupBy: "provider", StartDate: "2026-04-27", EndDate: "2026-04-28"},
		{ClientName: "client-a", GroupBy: "date"},
		{ClientName: "client-a", GroupBy: "date", Window: WindowTotal, StartDate: "2026-04-27", EndDate: "2026-04-28"},
		{ClientName: "client-a", GroupBy: "date", Bucket: "2026-04-27", StartDate: "2026-04-27", EndDate: "2026-04-28"},
	} {
		if _, err := store.Query(query); err == nil {
			t.Fatalf("Query(%#v) error = nil, want client query validation error", query)
		}
	}
}

func TestMemoryStoreQueriesUTCDateRangeFromDayBuckets(t *testing.T) {
	store := NewMemoryStore()
	before := testUsageRecord("office-key", time.Date(2026, 4, 26, 23, 59, 0, 0, time.UTC))
	inside1 := testUsageRecord("office-key", time.Date(2026, 4, 27, 0, 0, 0, 0, time.UTC))
	inside1.FallbackCount = 1
	inside1.CostUSD = 0.25
	inside1.Breakdown.CachedInputReadTokens = 2
	inside1.Breakdown.CachedInputWriteTokens = 3
	inside1.Breakdown.ToolUnits = map[string]float64{"search": 1.5}
	inside2 := testUsageRecord("office-key", time.Date(2026, 4, 28, 23, 59, 59, 0, time.UTC))
	inside2.Status = runtime.UsageStatusError
	inside2.UsageSource = runtime.UsageSourceEstimated
	inside2.CostUSD = 0.75
	inside2.Breakdown.CachedInputReadTokens = 5
	inside2.Breakdown.CachedInputWriteTokens = 7
	inside2.Breakdown.ToolUnits = map[string]float64{"search": 2, "code": 4}
	after := testUsageRecord("office-key", time.Date(2026, 4, 29, 0, 0, 0, 0, time.UTC))
	for _, record := range []runtime.UsageRecord{before, inside1, inside2, after} {
		store.Record(record)
	}

	items, err := store.Query(Query{GroupBy: "key", StartDate: "2026-04-27", EndDate: "2026-04-29"})
	if err != nil {
		t.Fatalf("Query() error = %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("len(items) = %d, want 1", len(items))
	}
	item := items[0]
	if item.RequestCount != 2 || item.SuccessCount != 1 || item.ErrorCount != 1 || item.FallbackCount != 1 {
		t.Fatalf("counts = %#v, want both in-range days only", item)
	}
	if item.InputTokens != 20 || item.OutputTokens != 10 || item.TotalTokens != 30 || item.CostUSD != 1 {
		t.Fatalf("usage totals = %#v, want fully merged totals", item)
	}
	if item.CachedInputReadTokens != 7 || item.CacheReadTokens != 7 || item.CachedInputWriteTokens != 10 || item.CacheCreateTokens != 10 {
		t.Fatalf("cache totals = %#v, want canonical and alias totals", item)
	}
	if item.ProviderUsageCount != 1 || item.EstimatedUsageCount != 1 || item.ToolUnits["search"] != 3.5 || item.ToolUnits["code"] != 4 {
		t.Fatalf("source/tool totals = %#v, want fully merged totals", item)
	}
	if item.LastSeenAt != inside2.FinishedAt {
		t.Fatalf("LastSeenAt = %q, want %q", item.LastSeenAt, inside2.FinishedAt)
	}
}

func TestMemoryStoreDateRangeKeepsDailyDateGroups(t *testing.T) {
	store := NewMemoryStore()
	store.Record(testUsageRecord("office-key", time.Date(2026, 4, 27, 9, 0, 0, 0, time.UTC)))
	store.Record(testUsageRecord("office-key", time.Date(2026, 4, 28, 9, 0, 0, 0, time.UTC)))

	items, err := store.Query(Query{GroupBy: "date", StartDate: "2026-04-27", EndDate: "2026-04-29"})
	if err != nil {
		t.Fatalf("Query() error = %v", err)
	}
	if len(items) != 2 || items[0].Value != "2026-04-27" || items[1].Value != "2026-04-28" {
		t.Fatalf("items = %#v, want one sorted item per day", items)
	}
}

func TestMemoryStoreRejectsInvalidDateRanges(t *testing.T) {
	store := NewMemoryStore()
	for _, query := range []Query{
		{StartDate: "2026-04-27"},
		{StartDate: "2026-04-31", EndDate: "2026-05-01"},
		{StartDate: "2026-04-27T00:00:00Z", EndDate: "2026-04-28"},
		{StartDate: "2026-04-28", EndDate: "2026-04-28"},
		{Window: WindowDay, Bucket: "2026-04-27", StartDate: "2026-04-27", EndDate: "2026-04-28"},
	} {
		if _, err := store.Query(query); err == nil {
			t.Fatalf("Query(%#v) error = nil, want validation error", query)
		}
	}
}

func TestMemoryStoreGroupsUsageBillingDimensions(t *testing.T) {
	store := NewMemoryStore()
	record := testUsageRecord("office-key", time.Date(2026, 4, 27, 9, 0, 0, 0, time.UTC))
	record.SessionID = "session-1"
	record.Agent = "codex"
	record.CostUSD = 0.42
	record.Breakdown.CachedInputReadTokens = 7
	record.Breakdown.CachedInputWriteTokens = 3
	store.Record(record)

	for _, groupBy := range []string{"date", "agent", "session", "model"} {
		items, err := store.Query(Query{GroupBy: groupBy, Window: WindowTotal})
		if err != nil {
			t.Fatalf("group_by=%s Query() error = %v", groupBy, err)
		}
		if len(items) != 1 {
			t.Fatalf("group_by=%s len(items) = %d, want 1", groupBy, len(items))
		}
		item := items[0]
		if item.Date != "2026-04-27" || item.Agent != "codex" || item.SessionID != "session-1" || item.Model != "gpt-4o-mini" {
			t.Fatalf("group_by=%s item = %#v, want billing dimensions", groupBy, item)
		}
		if item.CacheReadTokens != 7 || item.CacheCreateTokens != 3 || item.CostUSD != 0.42 {
			t.Fatalf("group_by=%s item = %#v, want cache aliases and cost", groupBy, item)
		}
	}
}

func TestMemoryStoreGroupsByErrorKind(t *testing.T) {
	store := NewMemoryStore()
	base := testUsageRecord("office-key", time.Date(2026, 4, 27, 9, 0, 0, 0, time.UTC))
	store.Record(base)

	failed := base
	failed.Status = runtime.UsageStatusError
	failed.ErrorKind = "auth_failed"
	store.Record(failed)

	items, err := store.Query(Query{GroupBy: "error_kind", Window: WindowTotal})
	if err != nil {
		t.Fatalf("Query() error = %v", err)
	}
	if len(items) != 2 {
		t.Fatalf("len(items) = %d, want 2", len(items))
	}
	if items[0].Value != "auth_failed" || items[0].ErrorCount != 1 {
		t.Fatalf("items[0] = %#v, want auth_failed error_count=1", items[0])
	}
	if items[1].Value != "none" || items[1].SuccessCount != 1 {
		t.Fatalf("items[1] = %#v, want none success_count=1", items[1])
	}
}
