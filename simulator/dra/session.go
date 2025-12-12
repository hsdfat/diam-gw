package main

import (
	"sync"
	"time"
)

// Session represents a Diameter session
type Session struct {
	ID            string
	OriginHost    string
	OriginRealm   string
	DestHost      string
	DestRealm     string
	ApplicationID uint32
	CreatedAt     time.Time
	LastActivity  time.Time
	State         SessionState
	Metadata      map[string]interface{}
}

// SessionState represents the state of a session
type SessionState int

const (
	SessionStateActive SessionState = iota
	SessionStateTerminating
	SessionStateTerminated
)

// String returns string representation of session state
func (s SessionState) String() string {
	switch s {
	case SessionStateActive:
		return "ACTIVE"
	case SessionStateTerminating:
		return "TERMINATING"
	case SessionStateTerminated:
		return "TERMINATED"
	default:
		return "UNKNOWN"
	}
}

// SessionManager manages Diameter sessions
type SessionManager struct {
	mu       sync.RWMutex
	sessions map[string]*Session
}

// NewSessionManager creates a new session manager
func NewSessionManager() *SessionManager {
	return &SessionManager{
		sessions: make(map[string]*Session),
	}
}

// CreateSession creates a new session
func (sm *SessionManager) CreateSession(sessionID string, originHost, originRealm string) *Session {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	now := time.Now()
	session := &Session{
		ID:           sessionID,
		OriginHost:   originHost,
		OriginRealm:  originRealm,
		CreatedAt:    now,
		LastActivity: now,
		State:        SessionStateActive,
		Metadata:     make(map[string]interface{}),
	}

	sm.sessions[sessionID] = session
	return session
}

// GetSession retrieves a session by ID
func (sm *SessionManager) GetSession(sessionID string) (*Session, bool) {
	sm.mu.RLock()
	defer sm.mu.RUnlock()

	session, exists := sm.sessions[sessionID]
	return session, exists
}

// UpdateSession updates session activity
func (sm *SessionManager) UpdateSession(sessionID string) {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	if session, exists := sm.sessions[sessionID]; exists {
		session.LastActivity = time.Now()
	}
}

// TerminateSession marks a session as terminated
func (sm *SessionManager) TerminateSession(sessionID string) {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	if session, exists := sm.sessions[sessionID]; exists {
		session.State = SessionStateTerminated
		session.LastActivity = time.Now()
	}
}

// DeleteSession removes a session
func (sm *SessionManager) DeleteSession(sessionID string) {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	delete(sm.sessions, sessionID)
}

// ActiveSessionCount returns the count of active sessions
func (sm *SessionManager) ActiveSessionCount() int {
	sm.mu.RLock()
	defer sm.mu.RUnlock()

	count := 0
	for _, session := range sm.sessions {
		if session.State == SessionStateActive {
			count++
		}
	}
	return count
}

// TotalSessionCount returns the total count of sessions
func (sm *SessionManager) TotalSessionCount() int {
	sm.mu.RLock()
	defer sm.mu.RUnlock()

	return len(sm.sessions)
}

// CleanupExpiredSessions removes sessions inactive for specified duration
func (sm *SessionManager) CleanupExpiredSessions(timeout time.Duration) int {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	now := time.Now()
	cleaned := 0

	for id, session := range sm.sessions {
		if session.State == SessionStateTerminated || now.Sub(session.LastActivity) > timeout {
			delete(sm.sessions, id)
			cleaned++
		}
	}

	return cleaned
}

// ListSessions returns a list of all sessions (for debugging/monitoring)
func (sm *SessionManager) ListSessions() []*Session {
	sm.mu.RLock()
	defer sm.mu.RUnlock()

	sessions := make([]*Session, 0, len(sm.sessions))
	for _, session := range sm.sessions {
		sessions = append(sessions, session)
	}
	return sessions
}
