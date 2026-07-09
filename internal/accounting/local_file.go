package accounting

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"

	"github.com/ryanycheng/Syrogo/internal/config"
	"github.com/ryanycheng/Syrogo/internal/runtime"
)

type LocalFileStore struct {
	memory        *MemoryStore
	cfg           config.AccountingLocalFileConfig
	queue         chan runtime.UsageRecord
	flushTicker   *time.Ticker
	closeOnce     sync.Once
	wg            sync.WaitGroup
	mu            sync.Mutex
	writer        *bufio.Writer
	file          *os.File
	currentPath   string
	currentSize   int64
	currentLine   int64
	currentDay    string
	currentPart   int
	dropped       int64
	lastRecordRef snapshotCursor
}

func NewLocalFileStore(cfg config.AccountingLocalFileConfig) (*LocalFileStore, error) {
	if err := os.MkdirAll(filepath.Join(cfg.Dir, "records"), 0o755); err != nil {
		return nil, fmt.Errorf("create records dir: %w", err)
	}
	if err := os.MkdirAll(filepath.Join(cfg.Dir, "snapshots"), 0o755); err != nil {
		return nil, fmt.Errorf("create snapshots dir: %w", err)
	}
	store := &LocalFileStore{
		memory:      NewMemoryStore(),
		cfg:         cfg,
		queue:       make(chan runtime.UsageRecord, cfg.QueueSize),
		flushTicker: time.NewTicker(cfg.FlushInterval.Duration()),
	}
	if err := store.recover(); err != nil {
		return nil, err
	}
	store.wg.Add(1)
	go store.run()
	return store, nil
}

func (s *LocalFileStore) Record(record runtime.UsageRecord) {
	s.memory.Record(record)
	select {
	case s.queue <- record:
	default:
		s.mu.Lock()
		s.dropped++
		s.mu.Unlock()
	}
}

func (s *LocalFileStore) Query(query Query) ([]StatsItem, error) {
	return s.memory.Query(query)
}

func (s *LocalFileStore) RecentRecords(query RecentRecordsQuery) ([]runtime.UsageRecord, error) {
	return s.memory.RecentRecords(query)
}

func (s *LocalFileStore) Close(ctx context.Context) error {
	var err error
	s.closeOnce.Do(func() {
		close(s.queue)
		done := make(chan struct{})
		go func() {
			s.wg.Wait()
			close(done)
		}()
		select {
		case <-done:
		case <-ctx.Done():
			err = ctx.Err()
		}
	})
	return err
}

func (s *LocalFileStore) run() {
	defer s.wg.Done()
	defer s.flushTicker.Stop()
	batch := make([]runtime.UsageRecord, 0, s.cfg.WriteBufferRecords)
	flush := func(forceSnapshot bool) {
		for _, record := range batch {
			if err := s.appendRecord(record); err != nil {
				break
			}
		}
		batch = batch[:0]
		_ = s.flushWriter()
		if forceSnapshot {
			_ = s.writeSnapshot()
			_ = s.cleanupExpired()
		}
	}
	for {
		select {
		case record, ok := <-s.queue:
			if !ok {
				flush(true)
				_ = s.closeWriter()
				return
			}
			batch = append(batch, record)
			if len(batch) >= s.cfg.WriteBufferRecords {
				flush(false)
			}
		case <-s.flushTicker.C:
			flush(true)
		}
	}
}

func (s *LocalFileStore) appendRecord(record runtime.UsageRecord) error {
	if err := s.rotateIfNeeded(record); err != nil {
		return err
	}
	payload, err := json.Marshal(recordEnvelope{Record: record})
	if err != nil {
		return err
	}
	payload = append(payload, '\n')
	n, err := s.writer.Write(payload)
	if err != nil {
		return err
	}
	s.currentSize += int64(n)
	s.currentLine++
	s.lastRecordRef = snapshotCursor{RecordFile: s.currentPath, RecordLine: s.currentLine}
	return nil
}

func (s *LocalFileStore) rotateIfNeeded(record runtime.UsageRecord) error {
	day, _, _ := timeBuckets(record)
	if s.file == nil || s.currentDay != day || s.currentSize >= int64(s.cfg.RotateMaxSizeMB)*1024*1024 {
		if s.currentDay != day {
			s.currentPart = 0
		}
		s.currentPart++
		if err := s.openWriter(day, s.currentPart); err != nil {
			return err
		}
	}
	return nil
}

func (s *LocalFileStore) openWriter(day string, part int) error {
	if err := s.flushWriter(); err != nil {
		return err
	}
	if err := s.closeWriter(); err != nil {
		return err
	}
	dir := filepath.Join(s.cfg.Dir, "records", day[0:4], day[5:7], day[8:10])
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	path := filepath.Join(dir, fmt.Sprintf("%04d.jsonl", part))
	file, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	info, err := file.Stat()
	if err != nil {
		_ = file.Close()
		return err
	}
	s.file = file
	s.writer = bufio.NewWriter(file)
	s.currentPath = path
	s.currentSize = info.Size()
	s.currentDay = day
	if info.Size() == 0 {
		s.currentLine = 0
	}
	return nil
}

func (s *LocalFileStore) flushWriter() error {
	if s.writer == nil {
		return nil
	}
	if err := s.writer.Flush(); err != nil {
		return err
	}
	if s.file != nil {
		return s.file.Sync()
	}
	return nil
}

func (s *LocalFileStore) closeWriter() error {
	if s.file == nil {
		return nil
	}
	err := s.file.Close()
	s.file = nil
	s.writer = nil
	s.currentPath = ""
	s.currentSize = 0
	return err
}

func (s *LocalFileStore) writeSnapshot() error {
	s.memory.mu.Lock()
	defer s.memory.mu.Unlock()
	state := snapshotState{
		Totals:     cloneStatsGroups(s.memory.totals),
		Windows:    cloneWindowGroups(s.memory.windows),
		Cursor:     s.lastRecordRef,
		CapturedAt: time.Now().UTC().Format(time.RFC3339Nano),
	}
	payload, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return err
	}
	timestampName := time.Now().UTC().Format("2006-01-02T15-04-05Z") + ".json"
	fullPath := filepath.Join(s.cfg.Dir, "snapshots", timestampName)
	if err := os.WriteFile(fullPath, payload, 0o644); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(s.cfg.Dir, "snapshots", "latest.json"), payload, 0o644); err != nil {
		return err
	}
	return s.cleanupSnapshotExpired()
}

func (s *LocalFileStore) recover() error {
	latest := filepath.Join(s.cfg.Dir, "snapshots", "latest.json")
	payload, err := os.ReadFile(latest)
	if err == nil {
		var state snapshotState
		if err := json.Unmarshal(payload, &state); err == nil {
			s.memory.totals = cloneStatsGroups(state.Totals)
			s.memory.windows = cloneWindowGroupsFromStrings(state.Windows)
			s.lastRecordRef = state.Cursor
		}
	}
	return s.replayRecords()
}

func (s *LocalFileStore) replayRecords() error {
	root := filepath.Join(s.cfg.Dir, "records")
	var files []string
	if err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() || filepath.Ext(path) != ".jsonl" {
			return nil
		}
		files = append(files, path)
		return nil
	}); err != nil && !os.IsNotExist(err) {
		return err
	}
	sort.Strings(files)
	for _, path := range files {
		if s.lastRecordRef.RecordFile != "" && path < s.lastRecordRef.RecordFile {
			continue
		}
		if err := s.replayFile(path); err != nil {
			return err
		}
	}
	return nil
}

func (s *LocalFileStore) replayFile(path string) (err error) {
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	defer func() {
		if closeErr := file.Close(); err == nil && closeErr != nil {
			err = closeErr
		}
	}()
	scanner := bufio.NewScanner(file)
	line := int64(0)
	for scanner.Scan() {
		line++
		if path == s.lastRecordRef.RecordFile && line <= s.lastRecordRef.RecordLine {
			continue
		}
		var envelope recordEnvelope
		if err := json.Unmarshal(scanner.Bytes(), &envelope); err != nil {
			if errors.Is(err, io.ErrUnexpectedEOF) || errors.Is(err, io.EOF) {
				return nil
			}
			continue
		}
		s.memory.Record(envelope.Record)
		s.lastRecordRef = snapshotCursor{RecordFile: path, RecordLine: line}
	}
	return scanner.Err()
}

func (s *LocalFileStore) cleanupExpired() error {
	if s.cfg.RetentionDays <= 0 {
		return nil
	}
	cutoff := time.Now().UTC().AddDate(0, 0, -s.cfg.RetentionDays)
	root := filepath.Join(s.cfg.Dir, "records")
	return filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() || filepath.Ext(path) != ".jsonl" {
			return nil
		}
		if info.ModTime().UTC().Before(cutoff) && path != s.lastRecordRef.RecordFile {
			if removeErr := os.Remove(path); removeErr != nil && !os.IsNotExist(removeErr) {
				return removeErr
			}
		}
		return nil
	})
}

func (s *LocalFileStore) cleanupSnapshotExpired() error {
	if s.cfg.SnapshotRetentionDays <= 0 {
		return nil
	}
	cutoff := time.Now().UTC().AddDate(0, 0, -s.cfg.SnapshotRetentionDays)
	root := filepath.Join(s.cfg.Dir, "snapshots")
	return filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() || filepath.Ext(path) != ".json" {
			return nil
		}
		if filepath.Base(path) == "latest.json" {
			return nil
		}
		if info.ModTime().UTC().Before(cutoff) {
			if removeErr := os.Remove(path); removeErr != nil && !os.IsNotExist(removeErr) {
				return removeErr
			}
		}
		return nil
	})
}

func cloneStatsGroups(src map[string]map[string]StatsItem) map[string]map[string]StatsItem {
	if src == nil {
		return make(map[string]map[string]StatsItem)
	}
	out := make(map[string]map[string]StatsItem, len(src))
	for key, group := range src {
		copied := make(map[string]StatsItem, len(group))
		for value, item := range group {
			copied[value] = cloneStatsItem(item)
		}
		out[key] = copied
	}
	return out
}

func cloneWindowGroups(src map[Window]map[string]map[string]StatsItem) map[string]map[string]map[string]StatsItem {
	out := make(map[string]map[string]map[string]StatsItem, len(src))
	for window, groups := range src {
		copiedGroups := make(map[string]map[string]StatsItem, len(groups))
		for key, group := range groups {
			copied := make(map[string]StatsItem, len(group))
			for value, item := range group {
				copied[value] = cloneStatsItem(item)
			}
			copiedGroups[key] = copied
		}
		out[string(window)] = copiedGroups
	}
	return out
}

func cloneWindowGroupsFromStrings(src map[string]map[string]map[string]StatsItem) map[Window]map[string]map[string]StatsItem {
	out := map[Window]map[string]map[string]StatsItem{
		WindowDay:   make(map[string]map[string]StatsItem),
		WindowWeek:  make(map[string]map[string]StatsItem),
		WindowMonth: make(map[string]map[string]StatsItem),
	}
	for rawWindow, groups := range src {
		window := Window(rawWindow)
		if !supportedWindow(window) || window == WindowTotal {
			continue
		}
		for key, group := range groups {
			copied := make(map[string]StatsItem, len(group))
			for value, item := range group {
				copied[value] = cloneStatsItem(item)
			}
			out[window][key] = copied
		}
	}
	return out
}

func cloneStatsItem(item StatsItem) StatsItem {
	cloned := item
	if len(item.ToolUnits) > 0 {
		cloned.ToolUnits = make(map[string]float64, len(item.ToolUnits))
		maps.Copy(cloned.ToolUnits, item.ToolUnits)
	}
	return cloned
}
