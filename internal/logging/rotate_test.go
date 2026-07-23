package logging

import (
	"compress/gzip"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestRotatingWriterRotatesBeforeOversizedWrite(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "app.log")
	now := time.Date(2026, 7, 23, 12, 34, 56, 123456789, time.Local)
	w, err := NewRotatingWriter(Options{Path: path, MaxSizeBytes: 5, Now: func() time.Time { return now }})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.Write([]byte("1234")); err != nil {
		t.Fatal(err)
	}
	if _, err := w.Write([]byte("567")); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	archives, err := DiscoverArchives(path)
	if err != nil || len(archives) != 1 {
		t.Fatalf("archives = %#v, err = %v", archives, err)
	}
	if archives[0].Name != "app.20260723-123456.123456789.1.log" {
		t.Fatalf("archive name = %q", archives[0].Name)
	}
	assertFileContent(t, archives[0].Path, "1234")
	assertFileContent(t, path, "567")
}

func TestRotatingWriterRotatesAcrossLocalDate(t *testing.T) {
	path := filepath.Join(t.TempDir(), "app.log")
	start := time.Date(2026, 7, 23, 23, 59, 0, 0, time.Local)
	var unixNano atomic.Int64
	unixNano.Store(start.UnixNano())
	w, err := NewRotatingWriter(Options{Path: path, MaxSizeBytes: 100, Now: func() time.Time { return time.Unix(0, unixNano.Load()).In(time.Local) }})
	if err != nil {
		t.Fatal(err)
	}
	_, _ = w.Write([]byte("old"))
	unixNano.Store(start.Add(2 * time.Minute).UnixNano())
	_, _ = w.Write([]byte("new"))
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	archives, _ := DiscoverArchives(path)
	if len(archives) != 1 {
		t.Fatalf("archives = %#v", archives)
	}
	assertFileContent(t, archives[0].Path, "old")
}

func TestRotatingWriterGzip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "app.log")
	w, err := NewRotatingWriter(Options{Path: path, MaxSizeBytes: 3, Compress: true})
	if err != nil {
		t.Fatal(err)
	}
	_, _ = w.Write([]byte("abc"))
	_, _ = w.Write([]byte("d"))
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	archives, _ := DiscoverArchives(path)
	if len(archives) != 1 || !archives[0].Compressed {
		t.Fatalf("archives = %#v, want one gzip", archives)
	}
	file, err := os.Open(archives[0].Path)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	zr, err := gzip.NewReader(file)
	if err != nil {
		t.Fatal(err)
	}
	got, err := io.ReadAll(zr)
	if err != nil || string(got) != "abc" {
		t.Fatalf("gzip content = %q, err = %v", got, err)
	}
}

func TestRotatingWriterCompressesPlaintextArchivesAtStartup(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "app.log")
	archive := filepath.Join(dir, "app.20260723-123456.123456789.1.log")
	if err := os.WriteFile(archive, []byte("crash survivor"), 0o644); err != nil {
		t.Fatal(err)
	}

	w, err := NewRotatingWriter(Options{Path: path, MaxSizeBytes: 100, Compress: true})
	if err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}

	archives, err := DiscoverArchives(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(archives) != 1 || !archives[0].Compressed {
		t.Fatalf("archives = %#v, want startup archive compressed", archives)
	}
	assertGzipContent(t, archives[0].Path, "crash survivor")
}

func TestRotatingWriterCompressesMoreThanSignalBufferCapacity(t *testing.T) {
	path := filepath.Join(t.TempDir(), "app.log")
	w, err := NewRotatingWriter(Options{Path: path, MaxSizeBytes: 1, Compress: true})
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 100; i++ {
		if _, err := w.Write([]byte("x")); err != nil {
			t.Fatal(err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	archives, err := DiscoverArchives(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(archives) != 99 {
		t.Fatalf("archive count = %d, want 99", len(archives))
	}
	for _, archive := range archives {
		if !archive.Compressed {
			t.Fatalf("archive %q was not compressed", archive.Name)
		}
	}
}

func TestRotatingWriterRetentionMaxFiles(t *testing.T) {
	path := filepath.Join(t.TempDir(), "app.log")
	w, err := NewRotatingWriter(Options{Path: path, MaxSizeBytes: 1, MaxFiles: 2})
	if err != nil {
		t.Fatal(err)
	}
	for _, value := range []string{"a", "b", "c", "d"} {
		if _, err := w.Write([]byte(value)); err != nil {
			t.Fatal(err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	archives, _ := DiscoverArchives(path)
	if len(archives) != 2 {
		t.Fatalf("archive count = %d, want 2", len(archives))
	}
	assertFileContent(t, path, "d")
}

func TestRotatingWriterRetentionAgeAndTotalActualBytes(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "app.log")
	now := time.Now()
	old := filepath.Join(dir, "app.20260101-000000.000000001.1.log")
	newer := filepath.Join(dir, "app.20260102-000000.000000001.2.log")
	newest := filepath.Join(dir, "app.20260103-000000.000000001.3.log")
	writeSizedFile(t, old, 2)
	writeSizedFile(t, newer, 4)
	writeSizedFile(t, newest, 4)
	if err := os.Chtimes(old, now.Add(-48*time.Hour), now.Add(-48*time.Hour)); err != nil {
		t.Fatal(err)
	}
	if err := cleanup(Options{Path: path, MaxSizeBytes: 1, MaxAge: 24 * time.Hour, MaxTotalSize: 5, Now: func() time.Time { return now }}); err != nil {
		t.Fatal(err)
	}
	archives, _ := DiscoverArchives(path)
	if len(archives) != 1 || archives[0].Path != newest {
		t.Fatalf("archives = %#v, want newest only", archives)
	}
}

func TestRotatingWriterConcurrentWrites(t *testing.T) {
	path := filepath.Join(t.TempDir(), "app.log")
	w, err := NewRotatingWriter(Options{Path: path, MaxSizeBytes: 64})
	if err != nil {
		t.Fatal(err)
	}
	const goroutines, writes = 8, 100
	var wg sync.WaitGroup
	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < writes; j++ {
				if _, err := w.Write([]byte("x")); err != nil {
					t.Errorf("Write() error = %v", err)
					return
				}
			}
		}()
	}
	wg.Wait()
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	current, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	archives, err := DiscoverArchives(path)
	if err != nil {
		t.Fatal(err)
	}
	total := current.Size()
	for _, archive := range archives {
		total += archive.Size
	}
	if total != goroutines*writes {
		t.Fatalf("total bytes = %d, want %d", total, goroutines*writes)
	}
}

func TestDiscoverArchivesIgnoresUnrelatedAndNonRegularFiles(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "app.log")
	valid := filepath.Join(dir, "app.20260723-123456.123456789.1.log")
	writeSizedFile(t, valid, 1)
	writeSizedFile(t, filepath.Join(dir, "other.20260723-123456.123456789.1.log"), 1)
	writeSizedFile(t, filepath.Join(dir, "app.20260723-123456.123.2.log"), 1)
	writeSizedFile(t, filepath.Join(dir, "app.20261399-123456.123456789.3.log"), 1)
	if err := os.Mkdir(filepath.Join(dir, "app.20260723-123456.123456789.4.log"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(valid, filepath.Join(dir, "app.20260723-123456.123456789.5.log")); err != nil {
		t.Fatal(err)
	}
	archives, err := DiscoverArchives(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(archives) != 1 || archives[0].Path != valid || archives[0].Identity == "" {
		t.Fatalf("archives = %#v, want valid regular file only", archives)
	}
}

func TestRotatingWriterLeavesUnrelatedFilesDuringCleanup(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "app.log")
	unrelated := filepath.Join(dir, "keep.txt")
	writeSizedFile(t, unrelated, 20)
	w, err := NewRotatingWriter(Options{Path: path, MaxSizeBytes: 1, MaxFiles: 1, MaxTotalSize: 1})
	if err != nil {
		t.Fatal(err)
	}
	_, _ = w.Write([]byte("a"))
	_, _ = w.Write([]byte("b"))
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(unrelated); err != nil {
		t.Fatalf("unrelated file removed: %v", err)
	}
}

func assertGzipContent(t *testing.T, path, want string) {
	t.Helper()
	file, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	zr, err := gzip.NewReader(file)
	if err != nil {
		t.Fatal(err)
	}
	defer zr.Close()
	got, err := io.ReadAll(zr)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != want {
		t.Fatalf("%s gzip content = %q, want %q", path, got, want)
	}
}

func assertFileContent(t *testing.T, path, want string) {
	t.Helper()
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != want {
		t.Fatalf("%s = %q, want %q", path, got, want)
	}
}

func writeSizedFile(t *testing.T, path string, size int) {
	t.Helper()
	if err := os.WriteFile(path, []byte(strings.Repeat("x", size)), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func TestArchiveNamingSequenceAvoidsCollision(t *testing.T) {
	path := filepath.Join(t.TempDir(), "app.log")
	now := time.Date(2026, 7, 23, 1, 2, 3, 4, time.Local)
	w, err := NewRotatingWriter(Options{Path: path, MaxSizeBytes: 1, Now: func() time.Time { return now }})
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 3; i++ {
		_, _ = w.Write([]byte(fmt.Sprint(i)))
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	archives, _ := DiscoverArchives(path)
	if len(archives) != 2 || archives[0].Name == archives[1].Name {
		t.Fatalf("archives = %#v", archives)
	}
}
