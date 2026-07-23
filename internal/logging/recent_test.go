package logging

import (
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"
)

func newTestRecentBuffer(t *testing.T, age time.Duration, maxBytes int64, now time.Time) (*RecentBuffer, *time.Time) {
	t.Helper()
	buffer := NewRecentBuffer(age, maxBytes)
	clock := now
	buffer.mu.Lock()
	buffer.startedAt = now
	buffer.coverageFrom = now
	buffer.now = func() time.Time { return clock }
	buffer.mu.Unlock()
	return buffer, &clock
}

func snapshotContents(snapshot RecentSnapshot) string {
	parts := make([]string, len(snapshot.Lines))
	for index, line := range snapshot.Lines {
		parts[index] = string(line.Content)
	}
	return strings.Join(parts, ",")
}

func TestRecentBufferCombinesPartialAndMultipleLines(t *testing.T) {
	now := time.Date(2026, 7, 23, 1, 0, 0, 0, time.UTC)
	buffer, clock := newTestRecentBuffer(t, 5*time.Minute, 1024, now)
	_, _ = buffer.Write([]byte("first"))
	*clock = now.Add(time.Second)
	_, _ = buffer.Write([]byte(" line\nsecond\nthird"))

	snapshot, covered := buffer.Snapshot(now, now.Add(time.Minute))
	if !covered || snapshotContents(snapshot) != "first line,second" {
		t.Fatalf("Snapshot() = %#v, covered %v", snapshot, covered)
	}
	if snapshot.LineCount != 2 || snapshot.Bytes != int64(len("first line\nsecond\n")) {
		t.Fatalf("snapshot stats = %#v", snapshot)
	}
}

func TestRecentBufferEvictsByBytesFIFO(t *testing.T) {
	now := time.Date(2026, 7, 23, 1, 0, 0, 0, time.UTC)
	buffer, clock := newTestRecentBuffer(t, 5*time.Minute, 8, now)
	_, _ = buffer.Write([]byte("one\n"))
	*clock = now.Add(time.Second)
	_, _ = buffer.Write([]byte("two\n"))
	*clock = now.Add(2 * time.Second)
	_, _ = buffer.Write([]byte("x\n"))

	snapshot, covered := buffer.Snapshot(now, now.Add(time.Minute))
	if covered || snapshotContents(snapshot) != "two,x" || snapshot.Bytes != 6 {
		t.Fatalf("Snapshot() = %#v, covered %v", snapshot, covered)
	}
}

func TestRecentBufferEvictsByAge(t *testing.T) {
	now := time.Date(2026, 7, 23, 1, 0, 0, 0, time.UTC)
	buffer, clock := newTestRecentBuffer(t, time.Minute, 1024, now)
	_, _ = buffer.Write([]byte("old\n"))
	*clock = now.Add(2 * time.Minute)
	_, _ = buffer.Write([]byte("new\n"))

	snapshot, covered := buffer.Snapshot(now, now.Add(3*time.Minute))
	if covered || snapshotContents(snapshot) != "new" {
		t.Fatalf("Snapshot() = %#v, covered %v", snapshot, covered)
	}
	_, covered = buffer.Snapshot(now.Add(time.Minute), now.Add(3*time.Minute))
	if !covered {
		t.Fatal("Snapshot() covered = false at age boundary")
	}
}

func TestRecentBufferCoverageRequiresStartedAtAndRejectsOversizeLine(t *testing.T) {
	now := time.Date(2026, 7, 23, 1, 0, 0, 0, time.UTC)
	buffer, clock := newTestRecentBuffer(t, 5*time.Minute, 8, now)
	_, _ = buffer.Write([]byte("ok\n"))
	if _, covered := buffer.Snapshot(now.Add(-time.Second), now.Add(time.Minute)); covered {
		t.Fatal("Snapshot() covered = true before buffer start")
	}
	*clock = now.Add(time.Second)
	_, _ = buffer.Write([]byte("oversized partial"))
	_, _ = buffer.Write([]byte(" still oversized\nnext\n"))
	_, _ = buffer.Write([]byte("later\n"))

	snapshot, covered := buffer.Snapshot(now, now.Add(time.Minute))
	if covered || snapshotContents(snapshot) != "later" {
		t.Fatalf("Snapshot() = %#v, covered %v", snapshot, covered)
	}
	if _, covered := buffer.Snapshot(now.Add(2*time.Second), now.Add(time.Minute)); !covered {
		t.Fatal("Snapshot() should cover interval after dropped line")
	}
}

func TestRecentBufferSnapshotReturnsCopies(t *testing.T) {
	now := time.Date(2026, 7, 23, 1, 0, 0, 0, time.UTC)
	buffer, _ := newTestRecentBuffer(t, time.Minute, 1024, now)
	_, _ = buffer.Write([]byte("line\n"))
	first, _ := buffer.Snapshot(now, now.Add(time.Minute))
	first.Lines[0].Content[0] = 'X'
	second, _ := buffer.Snapshot(now, now.Add(time.Minute))
	if string(second.Lines[0].Content) != "line" {
		t.Fatalf("Snapshot() content = %q", second.Lines[0].Content)
	}
}

func TestRecentBufferConcurrentWritesAndSnapshots(t *testing.T) {
	buffer := NewRecentBuffer(time.Hour, 1<<20)
	started := buffer.startedAt
	var writers sync.WaitGroup
	for worker := 0; worker < 8; worker++ {
		writers.Add(1)
		go func(worker int) {
			defer writers.Done()
			for line := 0; line < 100; line++ {
				_, _ = fmt.Fprintf(buffer, "%d-%d\n", worker, line)
				_, _ = buffer.Snapshot(started, time.Now().Add(time.Hour))
			}
		}(worker)
	}
	writers.Wait()
	snapshot, covered := buffer.Snapshot(started, time.Now().Add(time.Hour))
	if !covered || snapshot.LineCount != 800 {
		t.Fatalf("Snapshot() line count = %d, covered %v", snapshot.LineCount, covered)
	}
}
