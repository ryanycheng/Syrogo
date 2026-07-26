package quota

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/ryanycheng/Syrogo/internal/config"
)

type SnapshotStore struct {
	outboundTracker *Tracker
	clientTracker   *Tracker
	dir             string
	flushInterval   time.Duration
	ticker          *time.Ticker
	done            chan struct{}
	closeOnce       sync.Once
	wg              sync.WaitGroup
}

type snapshotFile struct {
	Version    int            `json:"version"`
	CapturedAt string         `json:"captured_at"`
	Outbound   PersistedState `json:"outbound"`
	Client     PersistedState `json:"client"`
}

func NewSnapshotStore(cfg config.GovernanceQuotaSnapshotConfig, outboundTracker *Tracker, clientTracker *Tracker) (*SnapshotStore, error) {
	return newSnapshotStore(cfg, outboundTracker, clientTracker, true)
}

// NewSnapshotStoreWithoutLoad starts snapshot persistence without importing the
// on-disk snapshot. It is intended for replacing a store around live trackers.
func NewSnapshotStoreWithoutLoad(cfg config.GovernanceQuotaSnapshotConfig, outboundTracker *Tracker, clientTracker *Tracker) (*SnapshotStore, error) {
	return newSnapshotStore(cfg, outboundTracker, clientTracker, false)
}

func newSnapshotStore(cfg config.GovernanceQuotaSnapshotConfig, outboundTracker *Tracker, clientTracker *Tracker, load bool) (*SnapshotStore, error) {
	if !cfg.Enabled || (outboundTracker == nil && clientTracker == nil) {
		return nil, nil
	}
	if err := os.MkdirAll(cfg.Dir, 0o755); err != nil {
		return nil, fmt.Errorf("create quota snapshot dir: %w", err)
	}
	store := &SnapshotStore{outboundTracker: outboundTracker, clientTracker: clientTracker, dir: cfg.Dir, flushInterval: cfg.FlushInterval.Duration(), done: make(chan struct{})}
	if load {
		if err := store.Load(); err != nil {
			return nil, err
		}
	}
	store.ticker = time.NewTicker(store.flushInterval)
	store.wg.Add(1)
	go store.run()
	return store, nil
}

func (s *SnapshotStore) Load() error {
	if s == nil {
		return nil
	}
	payload, err := os.ReadFile(filepath.Join(s.dir, "latest.json"))
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("read quota snapshot: %w", err)
	}
	var snapshot snapshotFile
	if err := json.Unmarshal(payload, &snapshot); err != nil {
		return fmt.Errorf("decode quota snapshot: %w", err)
	}
	if snapshot.Outbound.Version == 0 {
		snapshot.Outbound.Version = snapshot.Version
	}
	if snapshot.Client.Version == 0 {
		snapshot.Client.Version = snapshot.Version
	}
	if s.outboundTracker != nil {
		s.outboundTracker.ImportState(snapshot.Outbound)
	}
	if s.clientTracker != nil {
		s.clientTracker.ImportState(snapshot.Client)
	}
	return nil
}

func (s *SnapshotStore) Save() error {
	if s == nil {
		return nil
	}
	snapshot := snapshotFile{Version: 3, CapturedAt: time.Now().UTC().Format(time.RFC3339Nano)}
	if s.outboundTracker != nil {
		snapshot.Outbound = s.outboundTracker.ExportState()
	}
	if s.clientTracker != nil {
		snapshot.Client = s.clientTracker.ExportState()
	}
	payload, err := json.MarshalIndent(snapshot, "", "  ")
	if err != nil {
		return err
	}
	tmp := filepath.Join(s.dir, "latest.json.tmp")
	if err := os.WriteFile(tmp, payload, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, filepath.Join(s.dir, "latest.json"))
}

func (s *SnapshotStore) Close(ctx context.Context) error {
	if s == nil {
		return nil
	}
	var err error
	s.closeOnce.Do(func() {
		if s.ticker != nil {
			s.ticker.Stop()
		}
		if s.done != nil {
			close(s.done)
		}
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
		if saveErr := s.Save(); saveErr != nil && err == nil {
			err = saveErr
		}
	})
	return err
}

func (s *SnapshotStore) run() {
	defer s.wg.Done()
	for {
		select {
		case <-s.ticker.C:
			_ = s.Save()
		case <-s.done:
			return
		}
	}
}
