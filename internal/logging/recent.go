package logging

import (
	"bytes"
	"sync"
	"time"
)

// RecentLine is a complete log line retained by RecentBuffer.
type RecentLine struct {
	Time    time.Time
	Content []byte
}

// RecentSnapshot describes a point-in-time copy of retained log lines.
type RecentSnapshot struct {
	Lines     []RecentLine
	LineCount int
	Bytes     int64
	StartedAt time.Time
}

// RecentBuffer retains complete recent log lines within age and byte bounds.
type RecentBuffer struct {
	mu           sync.Mutex
	maxAge       time.Duration
	maxBytes     int64
	startedAt    time.Time
	coverageFrom time.Time
	lines        []recentLine
	bytes        int64
	partial      []byte
	dropping     bool
	now          func() time.Time
}

type recentLine struct {
	at      time.Time
	content []byte
	size    int64
}

// NewRecentBuffer constructs a bounded recent log buffer.
func NewRecentBuffer(maxAge time.Duration, maxBytes int64) *RecentBuffer {
	now := time.Now
	startedAt := now()
	return &RecentBuffer{
		maxAge:       maxAge,
		maxBytes:     maxBytes,
		startedAt:    startedAt,
		coverageFrom: startedAt,
		now:          now,
	}
}

// Write accepts arbitrary chunks and retains only complete lines.
func (b *RecentBuffer) Write(p []byte) (int, error) {
	written := len(p)
	if b == nil || len(p) == 0 {
		return written, nil
	}
	b.mu.Lock()
	defer b.mu.Unlock()

	now := b.now()
	b.evictLocked(now)
	for len(p) > 0 {
		newline := bytes.IndexByte(p, '\n')
		if b.dropping {
			if newline < 0 {
				return written, nil
			}
			b.dropping = false
			p = p[newline+1:]
			continue
		}
		if newline < 0 {
			b.appendPartialLocked(p, now)
			break
		}
		b.appendPartialLocked(p[:newline], now)
		if b.dropping {
			b.dropping = false
			b.partial = nil
		} else {
			content := append([]byte(nil), b.partial...)
			b.partial = b.partial[:0]
			b.lines = append(b.lines, recentLine{at: now, content: content, size: int64(len(content) + 1)})
			b.bytes += int64(len(content) + 1)
			b.evictLocked(now)
		}
		p = p[newline+1:]
	}
	return written, nil
}

func (b *RecentBuffer) appendPartialLocked(p []byte, now time.Time) {
	if len(p) == 0 {
		return
	}
	if b.maxBytes <= 0 || int64(len(b.partial))+int64(len(p))+1 > b.maxBytes {
		b.partial = nil
		b.dropping = true
		b.advanceCoverageLocked(now.Add(time.Nanosecond))
		return
	}
	b.partial = append(b.partial, p...)
}

func (b *RecentBuffer) evictLocked(now time.Time) {
	cutoff := now.Add(-b.maxAge)
	for len(b.lines) > 0 && ((b.maxAge > 0 && b.lines[0].at.Before(cutoff)) || b.maxBytes <= 0 || b.bytes > b.maxBytes) {
		removed := b.lines[0]
		b.bytes -= removed.size
		b.lines[0] = recentLine{}
		b.lines = b.lines[1:]
		b.advanceCoverageLocked(removed.at.Add(time.Nanosecond))
	}
}

func (b *RecentBuffer) advanceCoverageLocked(value time.Time) {
	if value.After(b.coverageFrom) {
		b.coverageFrom = value
	}
}

// Snapshot returns retained lines in chronological order. covered is true only
// when the buffer can fully cover the requested start time.
func (b *RecentBuffer) Snapshot(since, until time.Time) (snapshot RecentSnapshot, covered bool) {
	if b == nil {
		return RecentSnapshot{}, false
	}
	b.mu.Lock()
	defer b.mu.Unlock()

	now := b.now()
	b.evictLocked(now)
	coverageFrom := b.coverageFrom
	if b.maxAge > 0 {
		ageBoundary := now.Add(-b.maxAge)
		if ageBoundary.After(coverageFrom) {
			coverageFrom = ageBoundary
		}
	}
	covered = !since.IsZero() && !since.Before(coverageFrom)
	snapshot.StartedAt = b.startedAt
	for _, line := range b.lines {
		if (!since.IsZero() && line.at.Before(since)) || (!until.IsZero() && line.at.After(until)) {
			continue
		}
		content := append([]byte(nil), line.content...)
		snapshot.Lines = append(snapshot.Lines, RecentLine{Time: line.at, Content: content})
		snapshot.Bytes += line.size
	}
	snapshot.LineCount = len(snapshot.Lines)
	return snapshot, covered
}
