package sessions

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"time"
)

const snapshotVersion = 1

type SnapshotConfig struct {
	Enabled       bool
	Dir           string
	FlushInterval time.Duration
}

type persistedSession struct {
	Session
	StatusObservedAt time.Time `json:"status_observed_at"`
}

type sessionSnapshot struct {
	Version    int                `json:"version"`
	CapturedAt time.Time          `json:"captured_at"`
	Generation uint64             `json:"generation"`
	Sessions   []persistedSession `json:"sessions"`
}

type SnapshotStore struct {
	store     *Store
	dir       string
	ticker    *time.Ticker
	done      chan struct{}
	closed    chan struct{}
	closeOnce sync.Once
	wg        sync.WaitGroup
	saveMu    sync.Mutex
	stateMu   sync.Mutex
	persisted uint64
	closeErr  error
}

func NewSnapshotStore(cfg SnapshotConfig, store *Store) (*SnapshotStore, error) {
	if !cfg.Enabled || store == nil {
		return nil, nil
	}
	if cfg.Dir == "" {
		return nil, errors.New("session snapshot dir is required")
	}
	if cfg.FlushInterval <= 0 {
		return nil, errors.New("session snapshot flush interval must be positive")
	}
	if err := os.MkdirAll(cfg.Dir, 0o700); err != nil {
		return nil, fmt.Errorf("create session snapshot dir: %w", err)
	}
	if err := os.Chmod(cfg.Dir, 0o700); err != nil {
		return nil, fmt.Errorf("secure session snapshot dir: %w", err)
	}

	snapshotStore := &SnapshotStore{
		store:  store,
		dir:    cfg.Dir,
		done:   make(chan struct{}),
		closed: make(chan struct{}),
	}
	if err := snapshotStore.Load(); err != nil {
		return nil, err
	}
	snapshotStore.ticker = time.NewTicker(cfg.FlushInterval)
	snapshotStore.wg.Add(1)
	go snapshotStore.run()
	return snapshotStore, nil
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
		return fmt.Errorf("read session snapshot: %w", err)
	}
	var snapshot sessionSnapshot
	if err := json.Unmarshal(payload, &snapshot); err != nil {
		return fmt.Errorf("decode session snapshot: %w", err)
	}
	if snapshot.Version != snapshotVersion {
		return fmt.Errorf("unsupported session snapshot version %d", snapshot.Version)
	}
	s.store.importSnapshot(snapshot.Sessions, snapshot.Generation)
	s.stateMu.Lock()
	s.persisted = snapshot.Generation
	s.stateMu.Unlock()
	return nil
}

func (s *SnapshotStore) Save() error {
	return s.save(false)
}

func (s *SnapshotStore) save(force bool) error {
	if s == nil {
		return nil
	}
	s.saveMu.Lock()
	defer s.saveMu.Unlock()

	snapshot := s.store.exportSnapshot()
	s.stateMu.Lock()
	persisted := s.persisted
	s.stateMu.Unlock()
	if !force && snapshot.Generation == persisted {
		return nil
	}
	snapshot.Version = snapshotVersion
	snapshot.CapturedAt = time.Now().UTC()
	if err := writeSnapshotAtomic(s.dir, snapshot); err != nil {
		return err
	}
	s.stateMu.Lock()
	s.persisted = snapshot.Generation
	s.stateMu.Unlock()
	return nil
}

func (s *SnapshotStore) Close(ctx context.Context) error {
	if s == nil {
		return nil
	}
	s.closeOnce.Do(func() {
		s.ticker.Stop()
		close(s.done)
		go func() {
			defer close(s.closed)
			s.wg.Wait()
			if err := s.save(true); err != nil {
				s.stateMu.Lock()
				s.closeErr = err
				s.stateMu.Unlock()
			}
		}()
	})
	select {
	case <-s.closed:
		s.stateMu.Lock()
		defer s.stateMu.Unlock()
		return s.closeErr
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (s *SnapshotStore) run() {
	defer s.wg.Done()
	for {
		select {
		case <-s.ticker.C:
			if err := s.Save(); err != nil {
				slog.Error("save session snapshot", "error", err)
			}
		case <-s.done:
			return
		}
	}
}

func (s *Store) exportSnapshot() sessionSnapshot {
	s.mu.RLock()
	defer s.mu.RUnlock()
	snapshot := sessionSnapshot{
		Generation: s.generation,
		Sessions:   make([]persistedSession, 0, len(s.sessions)),
	}
	for _, session := range s.sessions {
		snapshot.Sessions = append(snapshot.Sessions, persistedSession{
			Session:          cloneSession(session),
			StatusObservedAt: session.statusObservedAt,
		})
	}
	return snapshot
}

func (s *Store) importSnapshot(persisted []persistedSession, generation uint64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.now()
	imported := make(map[string]Session, len(persisted))
	normalized := false
	for _, item := range persisted {
		session := cloneSession(item.Session)
		session.statusObservedAt = item.StatusObservedAt
		if session.Status == StatusStopped {
			if session.LastSeenAt.Before(now.Add(-DefaultStoppedRetention)) {
				normalized = true
				continue
			}
		} else if session.HeartbeatCapability == HeartbeatCapabilityV1 {
			normalized = true
			session.Status = StatusUnknown
			session.RecoveryPending = true
			session.RecoveredAt = timePointer(now)
			session.LastHeartbeatAt = nil
			session.LeaseExpiresAt = timePointer(now.Add(DefaultLeaseTTL))
			session.statusObservedAt = now
		} else {
			normalized = true
			session.Status = StatusStopped
			session.RecoveryPending = false
			session.RecoveredAt = nil
			session.StoppedAt = timePointer(now)
			session.LastSeenAt = now
			session.ExitCode = nil
			session.statusObservedAt = now
		}
		imported[session.ID] = session
	}
	s.sessions = imported
	s.generation = generation
	if normalized {
		s.generation++
	}
}

func writeSnapshotAtomic(dir string, snapshot sessionSnapshot) error {
	tmp, err := os.CreateTemp(dir, ".latest.json-*.tmp")
	if err != nil {
		return fmt.Errorf("create session snapshot temp file: %w", err)
	}
	tmpName := tmp.Name()
	removeTemp := true
	defer func() {
		if removeTemp {
			_ = os.Remove(tmpName)
		}
	}()
	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("secure session snapshot temp file: %w", err)
	}
	encoder := json.NewEncoder(tmp)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(snapshot); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("encode session snapshot: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("sync session snapshot temp file: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close session snapshot temp file: %w", err)
	}
	path := filepath.Join(dir, "latest.json")
	if err := os.Rename(tmpName, path); err != nil {
		return fmt.Errorf("replace session snapshot: %w", err)
	}
	removeTemp = false
	parent, err := os.Open(dir)
	if err != nil {
		return fmt.Errorf("open session snapshot dir: %w", err)
	}
	defer parent.Close()
	if err := parent.Sync(); err != nil {
		return fmt.Errorf("sync session snapshot dir: %w", err)
	}
	return nil
}

func timePointer(value time.Time) *time.Time {
	return &value
}
