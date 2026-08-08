package sessions

import (
	"errors"
	"reflect"
	"sort"
	"strings"
	"sync"
	"time"
)

const (
	DefaultStoppedRetention = time.Hour
	DefaultLeaseTTL         = 45 * time.Second
	DefaultTransientTTL     = 15 * time.Minute
)

var (
	ErrSessionOwnerMismatch           = errors.New("session ID belongs to a different owner")
	ErrHeartbeatCapabilityUnsupported = errors.New("session does not support heartbeat")
)

type SweepResult struct {
	LeaseExpired      int
	TransientDegraded int
	StoppedPruned     int
}

type Store struct {
	mu         sync.RWMutex
	sessions   map[string]Session
	generation uint64
	now        func() time.Time
}

func NewStore() *Store {
	return &Store{
		sessions: make(map[string]Session),
		now:      time.Now,
	}
}

func (s *Store) Register(session Session) (Session, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	existing, exists := s.sessions[session.ID]
	if exists && (existing.ClientName != session.ClientName || existing.InboundName != session.InboundName) {
		return Session{}, ErrSessionOwnerMismatch
	}
	if exists && existing.Status == StatusStopped {
		return cloneSession(existing), nil
	}
	now := s.now()
	if exists {
		session.Status = existing.Status
		session.LastEvent = existing.LastEvent
		session.StartedAt = existing.StartedAt
		session.LastSeenAt = existing.LastSeenAt
		session.StoppedAt = existing.StoppedAt
		session.ExitCode = existing.ExitCode
		session.RecoveryPending = existing.RecoveryPending
		session.RecoveredAt = existing.RecoveredAt
		session.statusObservedAt = existing.statusObservedAt
		if existing.HeartbeatCapability != "" && session.HeartbeatCapability == "" {
			session.HeartbeatCapability = existing.HeartbeatCapability
		}
	}
	if session.StartedAt.IsZero() {
		session.StartedAt = now
	}
	if session.LastSeenAt.IsZero() {
		session.LastSeenAt = now
	}
	if session.Status == "" {
		session.Status = StatusUnknown
	}
	if session.statusObservedAt.IsZero() {
		session.statusObservedAt = now
	}
	if session.HeartbeatCapability == HeartbeatCapabilityV1 {
		heartbeatAt := now
		expiresAt := now.Add(DefaultLeaseTTL)
		session.LastHeartbeatAt = &heartbeatAt
		session.LeaseExpiresAt = &expiresAt
		session.RecoveryPending = false
	} else {
		session.LastHeartbeatAt = nil
		session.LeaseExpiresAt = nil
	}
	session.Command = append([]string(nil), session.Command...)
	s.setSessionLocked(session.ID, session)
	return cloneSession(session), nil
}

func (s *Store) GetOwned(sessionID, clientName, inboundName string) (Session, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	session, ok := s.sessions[sessionID]
	if !ok || session.ClientName != clientName || session.InboundName != inboundName {
		return Session{}, false
	}
	return cloneSession(session), true
}

func (s *Store) ApplyHookEvent(clientName, inboundName string, event HookEvent) (Session, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	session, ok := s.sessions[event.SessionID]
	if !ok || session.ClientName != clientName || session.InboundName != inboundName {
		return Session{}, false
	}
	if session.Status == StatusStopped {
		return cloneSession(session), true
	}
	if event.ReceivedAt.IsZero() {
		event.ReceivedAt = s.now()
	}
	now := s.now()
	session.Status = StatusForHookEvent(event, session.Status)
	session.LastEvent = event.EventName
	session.LastSeenAt = event.ReceivedAt
	session.statusObservedAt = now
	if session.Status == StatusStopped && session.StoppedAt == nil {
		stoppedAt := event.ReceivedAt
		session.StoppedAt = &stoppedAt
	}
	s.setSessionLocked(event.SessionID, session)
	return cloneSession(session), true
}

func (s *Store) MarkStopped(clientName, inboundName, sessionID string, exitCode int) (Session, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	session, ok := s.sessions[sessionID]
	if !ok || session.ClientName != clientName || session.InboundName != inboundName {
		return Session{}, false
	}
	if session.Status == StatusStopped {
		return cloneSession(session), true
	}
	now := s.now()
	session.Status = StatusStopped
	session.LastSeenAt = now
	session.StoppedAt = &now
	session.ExitCode = &exitCode
	session.statusObservedAt = now
	s.setSessionLocked(sessionID, session)
	return cloneSession(session), true
}

func (s *Store) Heartbeat(clientName, inboundName, sessionID string) (Session, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	session, ok := s.sessions[sessionID]
	if !ok || session.ClientName != clientName || session.InboundName != inboundName {
		return Session{}, false, nil
	}
	if session.Status == StatusStopped {
		return cloneSession(session), true, nil
	}
	if session.HeartbeatCapability != HeartbeatCapabilityV1 {
		return Session{}, true, ErrHeartbeatCapabilityUnsupported
	}
	now := s.now()
	expiresAt := now.Add(DefaultLeaseTTL)
	session.LastHeartbeatAt = &now
	session.LeaseExpiresAt = &expiresAt
	if session.RecoveryPending {
		session.RecoveryPending = false
		session.Status = StatusUnknown
	}
	s.setSessionLocked(sessionID, session)
	return cloneSession(session), true, nil
}

func (s *Store) List(filter ListFilter) []Session {
	s.mu.RLock()
	defer s.mu.RUnlock()
	result := make([]Session, 0, len(s.sessions))
	for _, session := range s.sessions {
		if !matchesFilter(session, filter) {
			continue
		}
		result = append(result, cloneSession(session))
	}
	sort.Slice(result, func(i, j int) bool {
		leftPriority := statusPriority(result[i].Status)
		rightPriority := statusPriority(result[j].Status)
		if leftPriority != rightPriority {
			return leftPriority < rightPriority
		}
		return result[i].LastSeenAt.After(result[j].LastSeenAt)
	})
	return result
}

func (s *Store) Sweep(stoppedRetention, transientTTL time.Duration) SweepResult {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.now()
	result := SweepResult{}
	for id, session := range s.sessions {
		if session.Status == StatusStopped {
			if stoppedRetention > 0 && session.LastSeenAt.Before(now.Add(-stoppedRetention)) {
				delete(s.sessions, id)
				s.generation++
				result.StoppedPruned++
			}
			continue
		}
		if session.HeartbeatCapability == HeartbeatCapabilityV1 && session.LeaseExpiresAt != nil && !now.Before(*session.LeaseExpiresAt) {
			session.Status = StatusStopped
			session.LastSeenAt = now
			session.StoppedAt = &now
			session.ExitCode = nil
			session.statusObservedAt = now
			s.setSessionLocked(id, session)
			result.LeaseExpired++
			continue
		}
		if transientTTL > 0 && (session.Status == StatusToolRunning || session.Status == StatusCompacting) && !session.statusObservedAt.IsZero() && !now.Before(session.statusObservedAt.Add(transientTTL)) {
			session.Status = StatusUnknown
			session.statusObservedAt = now
			s.setSessionLocked(id, session)
			result.TransientDegraded++
		}
	}
	return result
}

func (s *Store) PruneStopped(maxAge time.Duration) int {
	return s.Sweep(maxAge, 0).StoppedPruned
}

func (s *Store) LatestActive(clientName, inboundName string) (Session, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var latest Session
	found := false
	for _, session := range s.sessions {
		if session.ClientName != clientName || session.InboundName != inboundName || session.Status == StatusStopped || session.RecoveryPending {
			continue
		}
		if !found || session.LastSeenAt.After(latest.LastSeenAt) {
			latest = session
			found = true
		}
	}
	if !found {
		return Session{}, false
	}
	return cloneSession(latest), true
}

func statusPriority(status Status) int {
	if status == StatusWaitingPermission {
		return 0
	}
	return 1
}

func matchesFilter(session Session, filter ListFilter) bool {
	if filter.Client != "" && session.ClientName != filter.Client {
		return false
	}
	if filter.Status != "" && session.Status != filter.Status {
		return false
	}
	if filter.Host != "" && !strings.Contains(session.Host, filter.Host) {
		return false
	}
	if filter.CWD != "" && !strings.Contains(session.CWD, filter.CWD) {
		return false
	}
	return true
}

func (s *Store) setSessionLocked(id string, session Session) bool {
	if existing, ok := s.sessions[id]; ok && reflect.DeepEqual(existing, session) {
		return false
	}
	s.sessions[id] = session
	s.generation++
	return true
}

func cloneSession(session Session) Session {
	session.Command = append([]string(nil), session.Command...)
	if session.LeaseExpiresAt != nil {
		leaseExpiresAt := *session.LeaseExpiresAt
		session.LeaseExpiresAt = &leaseExpiresAt
	}
	if session.LastHeartbeatAt != nil {
		lastHeartbeatAt := *session.LastHeartbeatAt
		session.LastHeartbeatAt = &lastHeartbeatAt
	}
	if session.RecoveredAt != nil {
		recoveredAt := *session.RecoveredAt
		session.RecoveredAt = &recoveredAt
	}
	if session.StoppedAt != nil {
		stoppedAt := *session.StoppedAt
		session.StoppedAt = &stoppedAt
	}
	if session.ExitCode != nil {
		exitCode := *session.ExitCode
		session.ExitCode = &exitCode
	}
	return session
}
