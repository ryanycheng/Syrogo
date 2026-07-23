package gateway

import (
	"compress/gzip"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/ryanycheng/Syrogo/internal/config"
	"github.com/ryanycheng/Syrogo/internal/logging"
)

func TestAdminLogPageFromRecentFiltersRedactsAndOrdersNewestFirst(t *testing.T) {
	snapshot := logging.RecentSnapshot{
		Lines: []logging.RecentLine{
			{Time: time.Date(2026, 7, 23, 1, 0, 0, 0, time.UTC), Content: []byte("time=2026-07-23T01:00:00Z level=ERROR msg=first status=502 token=secret")},
			{Time: time.Date(2026, 7, 23, 1, 1, 0, 0, time.UTC), Content: []byte("time=2026-07-23T01:01:00Z level=INFO msg=ignored status=200")},
			{Time: time.Date(2026, 7, 23, 1, 2, 0, 0, time.UTC), Content: []byte("time=2026-07-23T01:02:00Z level=ERROR msg=second status=503")},
		},
		Bytes: 256,
	}
	query := adminLogQuery{
		Since: time.Date(2026, 7, 23, 0, 59, 0, 0, time.UTC),
		Until: time.Date(2026, 7, 23, 1, 3, 0, 0, time.UTC),
		Limit: 2, Statuses: []string{"5xx"},
	}
	page, ok := adminLogPageFromRecent(snapshot, query)
	if !ok || page.Source != "memory" || page.LineCount != 2 || page.HasMore {
		t.Fatalf("page = %#v, ok = %v", page, ok)
	}
	if page.Items[0].Message != "second" || page.Items[1].Message != "first" {
		t.Fatalf("items = %#v, want newest first", page.Items)
	}
	if strings.Contains(page.Content, "secret") || !strings.Contains(page.Content, "<redacted>") {
		t.Fatalf("content = %q, want redacted", page.Content)
	}
	query.Limit = 1
	if _, ok := adminLogPageFromRecent(snapshot, query); ok {
		t.Fatal("adminLogPageFromRecent() ok = true with more matches than limit")
	}
}

func TestReadAdminLogPageFiltersAndPaginates(t *testing.T) {
	path := writeAdminLogTestFile(t, []string{
		"time=2026-07-20T11:54:00Z level=INFO msg=old",
		"time=2026-07-20T11:56:00Z level=INFO msg=first",
		"continuation",
		"time=2026-07-20T11:58:00Z level=ERROR msg=second token=secret",
		"time=2026-07-20T12:01:00Z level=INFO msg=future",
	})
	query := adminLogQuery{
		Since:    time.Date(2026, 7, 20, 11, 55, 0, 0, time.UTC),
		Until:    time.Date(2026, 7, 20, 12, 0, 0, 0, time.UTC),
		Limit:    2,
		MaxBytes: 4096,
	}

	first, err := readAdminLogPage(adminLogTestConfig(path), query)
	if err != nil {
		t.Fatalf("readAdminLogPage() error = %v", err)
	}
	if first.LineCount != 2 || !first.HasMore || first.NextCursor == "" {
		t.Fatalf("first page = %#v, want 2 lines and next cursor", first)
	}
	if !strings.Contains(first.Content, "continuation") || !strings.Contains(first.Content, "second") || strings.Contains(first.Content, "old") || strings.Contains(first.Content, "future") {
		t.Fatalf("first content = %q, want latest matching lines", first.Content)
	}

	query.Cursor = first.NextCursor
	second, err := readAdminLogPage(adminLogTestConfig(path), query)
	if err != nil {
		t.Fatalf("second readAdminLogPage() error = %v", err)
	}
	if second.LineCount != 1 || second.HasMore || !strings.Contains(second.Content, "first") {
		t.Fatalf("second page = %#v, want remaining matching line", second)
	}
}

func TestReadAdminLogPageKeepsSnapshotAcrossAppend(t *testing.T) {
	path := writeAdminLogTestFile(t, []string{
		"time=2026-07-20T11:56:00Z level=INFO msg=first",
		"time=2026-07-20T11:57:00Z level=INFO msg=second",
		"time=2026-07-20T11:58:00Z level=INFO msg=third",
	})
	query := adminLogQuery{Until: time.Date(2026, 7, 20, 12, 0, 0, 0, time.UTC), Limit: 1, MaxBytes: 4096}
	first, err := readAdminLogPage(adminLogTestConfig(path), query)
	if err != nil {
		t.Fatalf("readAdminLogPage() error = %v", err)
	}
	file, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatalf("os.OpenFile() error = %v", err)
	}
	if _, err := file.WriteString("time=2026-07-20T11:59:00Z level=INFO msg=appended\n"); err != nil {
		t.Fatalf("WriteString() error = %v", err)
	}
	_ = file.Close()

	query.Cursor = first.NextCursor
	second, err := readAdminLogPage(adminLogTestConfig(path), query)
	if err != nil {
		t.Fatalf("second readAdminLogPage() error = %v", err)
	}
	if strings.Contains(second.Content, "appended") || !strings.Contains(second.Content, "second") {
		t.Fatalf("second content = %q, want original snapshot", second.Content)
	}
}

func TestReadAdminLogPageRejectsStaleCursor(t *testing.T) {
	path := writeAdminLogTestFile(t, []string{
		"time=2026-07-20T11:56:00Z level=INFO msg=first",
		"time=2026-07-20T11:57:00Z level=INFO msg=second",
	})
	query := adminLogQuery{Until: time.Date(2026, 7, 20, 12, 0, 0, 0, time.UTC), Limit: 1, MaxBytes: 4096}
	page, err := readAdminLogPage(adminLogTestConfig(path), query)
	if err != nil {
		t.Fatalf("readAdminLogPage() error = %v", err)
	}
	if err := os.WriteFile(path, []byte("new\n"), 0o600); err != nil {
		t.Fatalf("os.WriteFile() error = %v", err)
	}
	query.Cursor = page.NextCursor
	if _, err := readAdminLogPage(adminLogTestConfig(path), query); !errorsIs(err, errAdminLogCursorStale) {
		t.Fatalf("readAdminLogPage() error = %v, want stale cursor", err)
	}
}

func TestParseAdminLogQueryBounds(t *testing.T) {
	now := time.Date(2026, 7, 20, 12, 0, 0, 0, time.UTC)
	query, err := parseAdminLogQuery(map[string][]string{
		"since": {"2026-07-20T11:55:00Z"},
		"until": {"2026-07-20T12:00:00Z"},
		"limit": {"1000"},
	}, 65536, now)
	if err != nil || query.Limit != hardAdminLogLines {
		t.Fatalf("parseAdminLogQuery() = %#v, %v", query, err)
	}
	for _, values := range []map[string][]string{
		{"limit": {"1001"}},
		{"limit": {"0"}},
		{"since": {"invalid"}},
		{"since": {"2026-07-20T12:01:00Z"}, "until": {"2026-07-20T12:00:00Z"}},
	} {
		if _, err := parseAdminLogQuery(values, 65536, now); err == nil {
			t.Fatalf("parseAdminLogQuery(%v) error = nil", values)
		}
	}
}

func TestParseAdminLogItemQuotedFields(t *testing.T) {
	item := parseAdminLogItem(`time=2026-07-20T11:58:00.123Z level=ERROR msg="request failed = retry" client_name="Claude Code" resolved_to="primary, fallback" detail="quote: \"value\" path: C:\\tmp" status=502 duration=1.25s`)
	if !item.Parsed || item.Time != "2026-07-20T11:58:00.123Z" || item.Level != "ERROR" {
		t.Fatalf("parseAdminLogItem() = %#v", item)
	}
	if item.Message != "request failed = retry" || item.Client != "Claude Code" || item.Status == nil || *item.Status != 502 {
		t.Fatalf("parsed fields = %#v", item)
	}
	if len(item.Outbound) != 2 || item.Outbound[0] != "primary" || item.Outbound[1] != "fallback" {
		t.Fatalf("outbound = %#v", item.Outbound)
	}
	if item.Fields["detail"] != `quote: "value" path: C:\tmp` {
		t.Fatalf("detail = %q", item.Fields["detail"])
	}
}

func TestReadAdminLogPageAppliesStructuredFiltersBeforeLimit(t *testing.T) {
	path := writeAdminLogTestFile(t, []string{
		`time=2026-07-20T11:55:00Z level=ERROR msg="older match" client=desktop outbound=backup status=503 error_kind=upstream`,
		`time=2026-07-20T11:56:00Z level=INFO msg=ignored client_name=desktop resolved_to=primary status=200`,
		`time=2026-07-20T11:57:00Z level=ERROR msg="newer match" client_name=desktop resolved_to="primary, backup" status=502 error_kind=upstream`,
		`time=2026-07-20T11:58:00Z level=ERROR msg="wrong client" client_name=mobile resolved_to=backup status=504 error_kind=upstream`,
	})
	query := adminLogQuery{
		Until: time.Date(2026, 7, 20, 12, 0, 0, 0, time.UTC), Limit: 1, MaxBytes: 4096,
		Text: []string{"match"}, Levels: []string{"ERROR"}, Statuses: []string{"5xx"},
		Clients: []string{"desktop"}, Outbounds: []string{"backup"}, ErrorKinds: []string{"upstream"},
	}

	first, err := readAdminLogPage(adminLogTestConfig(path), query)
	if err != nil {
		t.Fatalf("readAdminLogPage() error = %v", err)
	}
	if first.LineCount != 1 || !strings.Contains(first.Content, "newer match") || !first.HasMore {
		t.Fatalf("first page = %#v", first)
	}
	query.Cursor = first.NextCursor
	second, err := readAdminLogPage(adminLogTestConfig(path), query)
	if err != nil {
		t.Fatalf("second readAdminLogPage() error = %v", err)
	}
	if second.LineCount != 1 || !strings.Contains(second.Content, "older match") {
		t.Fatalf("second page = %#v", second)
	}
}

func TestReadAdminLogPageCursorBindsFilters(t *testing.T) {
	path := writeAdminLogTestFile(t, []string{
		"time=2026-07-20T11:55:00Z level=ERROR msg=older",
		"time=2026-07-20T11:56:00Z level=INFO msg=first",
		"time=2026-07-20T11:57:00Z level=ERROR msg=second",
	})
	query := adminLogQuery{Until: time.Date(2026, 7, 20, 12, 0, 0, 0, time.UTC), Limit: 1, MaxBytes: 4096, Levels: []string{"ERROR"}}
	page, err := readAdminLogPage(adminLogTestConfig(path), query)
	if err != nil {
		t.Fatalf("readAdminLogPage() error = %v", err)
	}
	query.Cursor = page.NextCursor
	query.Levels = []string{"INFO"}
	if _, err := readAdminLogPage(adminLogTestConfig(path), query); err == nil || !strings.Contains(err.Error(), "does not match") {
		t.Fatalf("readAdminLogPage() error = %v, want filter mismatch", err)
	}
}

func TestSplitAdminLogLinesKeepsCRLFOffsets(t *testing.T) {
	content := []byte("time=2026-07-20T11:56:00Z level=INFO msg=first\r\ntime=2026-07-20T11:57:00Z level=INFO msg=second\r\n")
	lines, _ := splitAdminLogLines(content, 0, int64(len(content)), false)
	want := int64(strings.Index(string(content), "time=2026-07-20T11:57:00Z"))
	if len(lines) != 2 || lines[1].Start != want {
		t.Fatalf("line offsets = %#v, want second start %d", lines, want)
	}
}

func TestReadAdminLogPageAcrossCurrentAndArchives(t *testing.T) {
	path := writeAdminLogTestFile(t, []string{"time=2026-07-20T12:00:00Z level=INFO msg=current"})
	writeAdminLogArchive(t, path, "20260720-115900", 2, []string{"time=2026-07-20T11:59:00Z level=INFO msg=new-archive"}, false)
	writeAdminLogArchive(t, path, "20260720-115800", 1, []string{"time=2026-07-20T11:58:00Z level=INFO msg=old-archive"}, false)
	query := adminLogQuery{Until: time.Date(2026, 7, 20, 13, 0, 0, 0, time.UTC), Limit: 10, MaxBytes: 4096}

	page, err := readAdminLogPage(adminLogTestConfig(path), query)
	if err != nil {
		t.Fatalf("readAdminLogPage() error = %v", err)
	}
	if page.ScannedFileCount != 3 || !page.IncludesArchives || page.LineCount != 3 {
		t.Fatalf("page = %#v, want all three files", page)
	}
	if strings.Index(page.Content, "current") > strings.Index(page.Content, "new-archive") || strings.Index(page.Content, "new-archive") > strings.Index(page.Content, "old-archive") {
		t.Fatalf("content order = %q", page.Content)
	}
}

func TestReadAdminLogPagePaginatesAcrossFilesAndReadsGzip(t *testing.T) {
	path := writeAdminLogTestFile(t, []string{"time=2026-07-20T12:00:00Z level=INFO msg=current"})
	writeAdminLogArchive(t, path, "20260720-115900", 2, []string{"time=2026-07-20T11:59:00Z level=INFO msg=plain"}, false)
	writeAdminLogArchive(t, path, "20260720-115800", 1, []string{"time=2026-07-20T11:58:00Z level=INFO msg=gzip"}, true)
	query := adminLogQuery{Until: time.Date(2026, 7, 20, 13, 0, 0, 0, time.UTC), Limit: 1, MaxBytes: 4096}
	var got []string
	for {
		page, err := readAdminLogPage(adminLogTestConfig(path), query)
		if err != nil {
			t.Fatalf("readAdminLogPage() error = %v", err)
		}
		if page.LineCount == 1 {
			got = append(got, page.Items[0].Message)
		}
		if !page.HasMore {
			break
		}
		query.Cursor = page.NextCursor
	}
	if strings.Join(got, ",") != "current,plain,gzip" {
		t.Fatalf("messages = %v", got)
	}
}

func TestReadAdminLogPageAggregateBudgetAdvancesEmptyMatches(t *testing.T) {
	path := writeAdminLogTestFile(t, []string{"time=2026-07-20T12:00:00Z level=INFO msg=ignored"})
	writeAdminLogArchive(t, path, "20260720-115900", 1, []string{"time=2026-07-20T11:59:00Z level=INFO msg=match"}, false)
	query := adminLogQuery{Until: time.Date(2026, 7, 20, 13, 0, 0, 0, time.UTC), Limit: 1, MaxBytes: int(osFileSize(t, path)), Text: []string{"match"}}
	first, err := readAdminLogPage(adminLogTestConfig(path), query)
	if err != nil {
		t.Fatalf("readAdminLogPage() error = %v", err)
	}
	if first.LineCount != 0 || !first.HasMore || first.BytesRead != query.MaxBytes {
		t.Fatalf("first page = %#v", first)
	}
	query.Cursor = first.NextCursor
	second, err := readAdminLogPage(adminLogTestConfig(path), query)
	if err != nil || second.LineCount != 1 || !strings.Contains(second.Content, "match") {
		t.Fatalf("second page = %#v, error = %v", second, err)
	}
}

func TestReadAdminLogPageFiltersAndRedactsArchive(t *testing.T) {
	path := writeAdminLogTestFile(t, []string{"time=2026-07-20T12:00:00Z level=INFO msg=current"})
	writeAdminLogArchive(t, path, "20260720-115900", 1, []string{
		"time=2026-07-20T11:58:00Z level=ERROR msg=history client=desktop token=secret",
		"time=2026-07-20T11:59:00Z level=INFO msg=ignored client=desktop",
	}, false)
	query := adminLogQuery{Since: time.Date(2026, 7, 20, 11, 57, 0, 0, time.UTC), Until: time.Date(2026, 7, 20, 12, 0, 0, 0, time.UTC), Limit: 5, MaxBytes: 4096, Levels: []string{"ERROR"}, Clients: []string{"desktop"}}
	page, err := readAdminLogPage(adminLogTestConfig(path), query)
	if err != nil || page.LineCount != 1 || !strings.Contains(page.Content, "history") || strings.Contains(page.Content, "secret") || !strings.Contains(page.Content, "<redacted>") {
		t.Fatalf("page = %#v, error = %v", page, err)
	}
}

func TestReadAdminLogPageArchiveDeletionIsStale(t *testing.T) {
	path := writeAdminLogTestFile(t, []string{"time=2026-07-20T12:00:00Z level=INFO msg=current"})
	archive := writeAdminLogArchive(t, path, "20260720-115900", 1, []string{"time=2026-07-20T11:59:00Z level=INFO msg=archive"}, false)
	query := adminLogQuery{Until: time.Date(2026, 7, 20, 13, 0, 0, 0, time.UTC), Limit: 1, MaxBytes: 4096}
	page, err := readAdminLogPage(adminLogTestConfig(path), query)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(archive); err != nil {
		t.Fatal(err)
	}
	query.Cursor = page.NextCursor
	if _, err := readAdminLogPage(adminLogTestConfig(path), query); !errorsIs(err, errAdminLogCursorStale) {
		t.Fatalf("error = %v, want stale", err)
	}
}

func TestReadAdminLogPageFollowsCurrentInodeAfterRotationRename(t *testing.T) {
	path := writeAdminLogTestFile(t, []string{
		"time=2026-07-20T11:59:00Z level=INFO msg=older",
		"time=2026-07-20T12:00:00Z level=INFO msg=newer",
	})
	query := adminLogQuery{Until: time.Date(2026, 7, 20, 13, 0, 0, 0, time.UTC), Limit: 1, MaxBytes: 4096}
	page, err := readAdminLogPage(adminLogTestConfig(path), query)
	if err != nil {
		t.Fatal(err)
	}
	archive := archivePath(path, "20260720-120100", 1, false)
	if err := os.Rename(path, archive); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("time=2026-07-20T12:01:00Z level=INFO msg=replacement\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	query.Cursor = page.NextCursor
	next, err := readAdminLogPage(adminLogTestConfig(path), query)
	if err != nil || !strings.Contains(next.Content, "older") || strings.Contains(next.Content, "replacement") {
		t.Fatalf("next = %#v, error = %v", next, err)
	}
}

func TestReadAdminLogPageCorruptGzipReturnsError(t *testing.T) {
	path := writeAdminLogTestFile(t, []string{"time=2026-07-20T12:00:00Z level=INFO msg=current"})
	archive := archivePath(path, "20260720-115900", 1, true)
	if err := os.WriteFile(archive, []byte("not gzip"), 0o600); err != nil {
		t.Fatal(err)
	}
	query := adminLogQuery{Until: time.Date(2026, 7, 20, 13, 0, 0, 0, time.UTC), Limit: 10, MaxBytes: 4096}
	if _, err := readAdminLogPage(adminLogTestConfig(path), query); err == nil || !strings.Contains(err.Error(), "gzip") {
		t.Fatalf("error = %v, want explicit gzip error", err)
	}
}

func archivePath(current, stamp string, sequence int, compressed bool) string {
	name := strings.TrimSuffix(filepath.Base(current), filepath.Ext(current)) + "." + stamp + ".000000000." + strconv.Itoa(sequence) + ".log"
	if compressed {
		name += ".gz"
	}
	return filepath.Join(filepath.Dir(current), name)
}

func writeAdminLogArchive(t *testing.T, current, stamp string, sequence int, lines []string, compressed bool) string {
	t.Helper()
	path := archivePath(current, stamp, sequence, compressed)
	content := []byte(strings.Join(lines, "\n") + "\n")
	if !compressed {
		if err := os.WriteFile(path, content, 0o600); err != nil {
			t.Fatal(err)
		}
		return path
	}
	file, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	writer := gzip.NewWriter(file)
	if _, err := writer.Write(content); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	return path
}

func osFileSize(t *testing.T, path string) int64 {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	return info.Size()
}

func adminLogTestConfig(path string) config.AdminLogsConfig {
	return config.AdminLogsConfig{Path: path, Rotation: config.AdminLogsRotationConfig{MaxSizeMB: 1}}
}

func writeAdminLogTestFile(t *testing.T, lines []string) string {
	t.Helper()
	path := t.TempDir() + "/dev.log"
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o600); err != nil {
		t.Fatalf("os.WriteFile() error = %v", err)
	}
	return path
}

func errorsIs(err, target error) bool {
	return err != nil && (err == target || strings.Contains(err.Error(), target.Error()))
}
