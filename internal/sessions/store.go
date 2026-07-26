package sessions

import (
	"errors"
	"sort"
	"strings"
	"sync"
	"time"
)

const DefaultStoppedRetention = time.Hour

type Store struct {
	mu       sync.RWMutex
	sessions map[string]Session
	now      func() time.Time
}

func NewStore() *Store {
	return &Store{
		sessions: make(map[string]Session),
		now:      time.Now,
	}
}

var ErrSessionOwnerMismatch = errors.New("session ID belongs to a different owner")

func (s *Store) Register(session Session) (Session, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if existing, ok := s.sessions[session.ID]; ok && (existing.ClientName != session.ClientName || existing.InboundName != session.InboundName) {
		return Session{}, ErrSessionOwnerMismatch
	}
	now := s.now()
	if session.StartedAt.IsZero() {
		session.StartedAt = now
	}
	if session.LastSeenAt.IsZero() {
		session.LastSeenAt = now
	}
	if session.Status == "" {
		session.Status = StatusUnknown
	}
	session.Command = append([]string(nil), session.Command...)
	s.sessions[session.ID] = session
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
	if event.ReceivedAt.IsZero() {
		event.ReceivedAt = s.now()
	}
	session.Status = StatusForHookEvent(event, session.Status)
	session.LastEvent = event.EventName
	session.LastSeenAt = event.ReceivedAt
	if session.Status == StatusStopped && session.StoppedAt == nil {
		stoppedAt := event.ReceivedAt
		session.StoppedAt = &stoppedAt
	}
	s.sessions[event.SessionID] = session
	return cloneSession(session), true
}

func (s *Store) MarkStopped(clientName, inboundName, sessionID string, exitCode int) (Session, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	session, ok := s.sessions[sessionID]
	if !ok || session.ClientName != clientName || session.InboundName != inboundName {
		return Session{}, false
	}
	now := s.now()
	session.Status = StatusStopped
	session.LastSeenAt = now
	session.StoppedAt = &now
	session.ExitCode = &exitCode
	s.sessions[sessionID] = session
	return cloneSession(session), true
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

func (s *Store) PruneStopped(maxAge time.Duration) int {
	if maxAge <= 0 {
		return 0
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	cutoff := s.now().Add(-maxAge)
	removed := 0
	for id, session := range s.sessions {
		if session.Status == StatusStopped && session.LastSeenAt.Before(cutoff) {
			delete(s.sessions, id)
			removed++
		}
	}
	return removed
}

func (s *Store) LatestActive(clientName, inboundName string) (Session, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var latest Session
	found := false
	for _, session := range s.sessions {
		if session.ClientName != clientName || session.InboundName != inboundName || session.Status == StatusStopped {
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
	switch status {
	case StatusWaitingPermission:
		return 0
	case StatusToolRunning, StatusCompacting:
		return 1
	case StatusRunning:
		return 2
	case StatusIdle, StatusUnknown:
		return 3
	case StatusStopped:
		return 4
	default:
		return 3
	}
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

func cloneSession(session Session) Session {
	session.Command = append([]string(nil), session.Command...)
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
