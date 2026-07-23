package accounting

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/ryanycheng/Syrogo/internal/config"
	"github.com/ryanycheng/Syrogo/internal/runtime"
)

func testLocalFileConfig(dir string) config.AccountingLocalFileConfig {
	return config.AccountingLocalFileConfig{
		Dir:                   dir,
		RotateMaxSizeMB:       1,
		RetentionDays:         7,
		SnapshotRetentionDays: 7,
		WriteBufferRecords:    1,
		FlushInterval:         config.DurationValue("10ms"),
		QueueSize:             8,
	}
}

func testUsageRecord(client string, ts time.Time) runtime.UsageRecord {
	return runtime.UsageRecord{
		ClientName:     client,
		ProviderName:   "openai",
		RequestedModel: "gpt-4o-mini",
		ExecutedModel:  "gpt-4o-mini",
		UsageSource:    runtime.UsageSourceProvider,
		Status:         runtime.UsageStatusSuccess,
		Breakdown: runtime.UsageBreakdown{
			RequestCount: 1,
			InputTokens:  10,
			OutputTokens: 5,
			TotalTokens:  15,
		},
		StartedAt:  ts.UTC().Format(time.RFC3339Nano),
		FinishedAt: ts.UTC().Format(time.RFC3339Nano),
	}
}

func TestLocalFileStoreSupportsDateRangeAfterRecovery(t *testing.T) {
	dir := t.TempDir()
	cfg := testLocalFileConfig(dir)
	store, err := NewLocalFileStore(cfg)
	if err != nil {
		t.Fatalf("NewLocalFileStore() error = %v", err)
	}
	store.Record(testUsageRecord("office-key", time.Date(2026, 4, 27, 9, 0, 0, 0, time.UTC)))
	store.Record(testUsageRecord("office-key", time.Date(2026, 4, 28, 9, 0, 0, 0, time.UTC)))
	store.Record(testUsageRecord("office-key", time.Date(2026, 4, 29, 9, 0, 0, 0, time.UTC)))
	if err := store.Close(context.Background()); err != nil {
		t.Fatalf("Close() error = %v", err)
	}

	recovered, err := NewLocalFileStore(cfg)
	if err != nil {
		t.Fatalf("NewLocalFileStore() recover error = %v", err)
	}
	defer func() { _ = recovered.Close(context.Background()) }()
	items, err := recovered.Query(Query{GroupBy: "key", StartDate: "2026-04-27", EndDate: "2026-04-29"})
	if err != nil {
		t.Fatalf("Query() error = %v", err)
	}
	if len(items) != 1 || items[0].RequestCount != 2 || items[0].TotalTokens != 30 {
		t.Fatalf("items = %#v, want recovered half-open date range", items)
	}
}

func TestLocalFileStoreRecoversFromSnapshotAndRecords(t *testing.T) {
	dir := t.TempDir()
	cfg := testLocalFileConfig(dir)

	store, err := NewLocalFileStore(cfg)
	if err != nil {
		t.Fatalf("NewLocalFileStore() error = %v", err)
	}
	store.Record(testUsageRecord("office-key", time.Date(2026, 4, 27, 9, 0, 0, 0, time.UTC)))
	store.Record(testUsageRecord("office-key", time.Date(2026, 4, 27, 10, 0, 0, 0, time.UTC)))
	if err := store.Close(context.Background()); err != nil {
		t.Fatalf("Close() error = %v", err)
	}

	recovered, err := NewLocalFileStore(cfg)
	if err != nil {
		t.Fatalf("NewLocalFileStore() recover error = %v", err)
	}
	defer func() { _ = recovered.Close(context.Background()) }()

	items, err := recovered.Query(Query{GroupBy: "key", Window: WindowTotal})
	if err != nil {
		t.Fatalf("Query() error = %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("len(items) = %d, want 1", len(items))
	}
	if items[0].Value != "office-key" || items[0].RequestCount != 2 || items[0].TotalTokens != 30 {
		t.Fatalf("items[0] = %#v, want request_count=2 total_tokens=30", items[0])
	}
}

func TestLocalFileStoreSupportsDayBucketQuery(t *testing.T) {
	dir := t.TempDir()
	cfg := testLocalFileConfig(dir)
	store, err := NewLocalFileStore(cfg)
	if err != nil {
		t.Fatalf("NewLocalFileStore() error = %v", err)
	}
	defer func() { _ = store.Close(context.Background()) }()

	store.Record(testUsageRecord("office-key", time.Date(2026, 4, 27, 9, 0, 0, 0, time.UTC)))
	store.Record(testUsageRecord("office-key", time.Date(2026, 4, 28, 9, 0, 0, 0, time.UTC)))

	items, err := store.Query(Query{GroupBy: "key", Window: WindowDay, Bucket: "2026-04-27"})
	if err != nil {
		t.Fatalf("Query() error = %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("len(items) = %d, want 1", len(items))
	}
	if items[0].RequestCount != 1 || items[0].TotalTokens != 15 {
		t.Fatalf("items[0] = %#v, want request_count=1 total_tokens=15", items[0])
	}
}

func TestLocalFileStoreRotatesByDay(t *testing.T) {
	dir := t.TempDir()
	cfg := testLocalFileConfig(dir)
	store, err := NewLocalFileStore(cfg)
	if err != nil {
		t.Fatalf("NewLocalFileStore() error = %v", err)
	}

	store.Record(testUsageRecord("office-key", time.Date(2026, 4, 27, 9, 0, 0, 0, time.UTC)))
	store.Record(testUsageRecord("office-key", time.Date(2026, 4, 28, 9, 0, 0, 0, time.UTC)))
	if err := store.Close(context.Background()); err != nil {
		t.Fatalf("Close() error = %v", err)
	}

	if _, err := os.Stat(filepath.Join(dir, "records", "2026", "04", "27", "0001.jsonl")); err != nil {
		t.Fatalf("day-1 record file missing: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "records", "2026", "04", "28", "0001.jsonl")); err != nil {
		t.Fatalf("day-2 record file missing: %v", err)
	}
}

func TestLocalFileStoreIgnoresBadTailRecordDuringRecover(t *testing.T) {
	dir := t.TempDir()
	cfg := testLocalFileConfig(dir)
	store, err := NewLocalFileStore(cfg)
	if err != nil {
		t.Fatalf("NewLocalFileStore() error = %v", err)
	}
	store.Record(testUsageRecord("office-key", time.Date(2026, 4, 27, 9, 0, 0, 0, time.UTC)))
	if err := store.Close(context.Background()); err != nil {
		t.Fatalf("Close() error = %v", err)
	}

	recordPath := filepath.Join(dir, "records", "2026", "04", "27", "0001.jsonl")
	file, err := os.OpenFile(recordPath, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatalf("OpenFile() error = %v", err)
	}
	if _, err := file.WriteString("{\"record\":"); err != nil {
		_ = file.Close()
		t.Fatalf("WriteString() error = %v", err)
	}
	_ = file.Close()

	recovered, err := NewLocalFileStore(cfg)
	if err != nil {
		t.Fatalf("NewLocalFileStore() recover error = %v", err)
	}
	defer func() { _ = recovered.Close(context.Background()) }()

	items, err := recovered.Query(Query{GroupBy: "key", Window: WindowTotal})
	if err != nil {
		t.Fatalf("Query() error = %v", err)
	}
	if len(items) != 1 || items[0].RequestCount != 1 {
		t.Fatalf("items = %#v, want one recovered valid record", items)
	}
}

func TestLocalFileStoreCleanupSnapshotExpiredRemovesOldSnapshots(t *testing.T) {
	dir := t.TempDir()
	cfg := testLocalFileConfig(dir)
	cfg.SnapshotRetentionDays = 1
	store, err := NewLocalFileStore(cfg)
	if err != nil {
		t.Fatalf("NewLocalFileStore() error = %v", err)
	}
	defer func() { _ = store.Close(context.Background()) }()

	snapshotDir := filepath.Join(dir, "snapshots")
	oldPath := filepath.Join(snapshotDir, "2026-04-01T00-00-00Z.json")
	if err := os.WriteFile(oldPath, []byte("{}"), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	oldTime := time.Now().AddDate(0, 0, -3)
	if err := os.Chtimes(oldPath, oldTime, oldTime); err != nil {
		t.Fatalf("Chtimes() error = %v", err)
	}
	latestPath := filepath.Join(snapshotDir, "latest.json")
	if err := os.WriteFile(latestPath, []byte("{}"), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	if err := store.cleanupSnapshotExpired(); err != nil {
		t.Fatalf("cleanupSnapshotExpired() error = %v", err)
	}
	if _, err := os.Stat(oldPath); !os.IsNotExist(err) {
		t.Fatalf("old snapshot still exists, err = %v", err)
	}
	if _, err := os.Stat(latestPath); err != nil {
		t.Fatalf("latest snapshot missing: %v", err)
	}
}
