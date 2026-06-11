package accounting

import (
	"testing"
	"time"

	"github.com/ryanycheng/Syrogo/internal/runtime"
)

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
