// Package cognitive provides high-level abstractions for session management,
// multi-agent coordination, and cognitive workflows on top of the KektorDB client.
package cognitive

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/sanonone/kektordb/pkg/client"
)

// Session represents an active conversational session with context management.
type Session struct {
	ID        string
	client    *client.Client
	indexName string
	Context   []client.ConversationMessage
	mu        sync.RWMutex
	createdAt time.Time
	updatedAt time.Time
}

// SessionManager provides high-level session lifecycle management.
type SessionManager struct {
	client     *client.Client
	indexName  string
	sessions   map[string]*Session
	mu         sync.RWMutex
	autoEnd    bool
	defaultTTL time.Duration
}

// NewSessionManager creates a new session manager.
func NewSessionManager(c *client.Client, indexName string) *SessionManager {
	return &SessionManager{
		client:     c,
		indexName:  indexName,
		sessions:   make(map[string]*Session),
		autoEnd:    true,
		defaultTTL: 30 * time.Minute,
	}
}

// SessionOptions configures session creation behavior.
type SessionOptions struct {
	UserID         string
	Metadata       map[string]interface{}
	InitialContext []client.ConversationMessage
	AutoEnd        bool
	TTL            time.Duration
}

// CreateSession starts a new session and returns a managed Session object.
func (sm *SessionManager) CreateSession(opts SessionOptions) (*Session, error) {
	req := client.StartSessionRequest{
		UserID:       opts.UserID,
		Metadata:     opts.Metadata,
		Conversation: opts.InitialContext,
	}

	resp, err := sm.client.StartSession(req)
	if err != nil {
		return nil, fmt.Errorf("failed to start session: %w", err)
	}

	session := &Session{
		ID:        resp.SessionID,
		client:    sm.client,
		indexName: sm.indexName,
		Context:   resp.Conversation,
		createdAt: time.Now(),
		updatedAt: time.Now(),
	}

	sm.mu.Lock()
	sm.sessions[session.ID] = session
	sm.mu.Unlock()

	return session, nil
}

// GetSession retrieves an existing session by ID.
func (sm *SessionManager) GetSession(sessionID string) (*Session, error) {
	sm.mu.RLock()
	session, exists := sm.sessions[sessionID]
	sm.mu.RUnlock()

	if !exists {
		return nil, fmt.Errorf("session %s not found", sessionID)
	}

	return session, nil
}

// EndSession terminates a session and removes it from management.
func (sm *SessionManager) EndSession(sessionID string) error {
	sm.mu.Lock()
	_, exists := sm.sessions[sessionID]
	if exists {
		delete(sm.sessions, sessionID)
	}
	sm.mu.Unlock()

	if !exists {
		return fmt.Errorf("session %s not found", sessionID)
	}

	return sm.client.EndSession(sessionID, sm.indexName)
}

// ListSessions returns all active session IDs.
func (sm *SessionManager) ListSessions() []string {
	sm.mu.RLock()
	defer sm.mu.RUnlock()

	ids := make([]string, 0, len(sm.sessions))
	for id := range sm.sessions {
		ids = append(ids, id)
	}
	return ids
}

// CleanupEndedSessions removes all sessions from memory (doesn't end them on server).
func (sm *SessionManager) CleanupEndedSessions() {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	sm.sessions = make(map[string]*Session)
}

// Session methods

// AddMessage adds a message to the session context (local only).
func (s *Session) AddMessage(role, content string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.Context = append(s.Context, client.ConversationMessage{
		Role:    role,
		Content: content,
	})
	s.updatedAt = time.Now()
}

// GetContext returns the current conversation context.
func (s *Session) GetContext() []client.ConversationMessage {
	s.mu.RLock()
	defer s.mu.RUnlock()

	// Return a copy to prevent external modification
	ctx := make([]client.ConversationMessage, len(s.Context))
	copy(ctx, s.Context)
	return ctx
}

// GetCreatedAt returns the session creation time.
func (s *Session) GetCreatedAt() time.Time {
	return s.createdAt
}

// GetUpdatedAt returns the last update time.
func (s *Session) GetUpdatedAt() time.Time {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.updatedAt
}

// End terminates the session on the server.
func (s *Session) End() error {
	return s.client.EndSession(s.ID, s.indexName)
}

// ManagedSession provides automatic cleanup via context pattern.
type ManagedSession struct {
	*Session
	manager *SessionManager
	ctx     context.Context
	cancel  context.CancelFunc
}

// Context returns the session's context for cancellation propagation.
func (ms *ManagedSession) Context() context.Context {
	return ms.ctx
}

// Close manually ends the managed session.
func (ms *ManagedSession) Close() error {
	ms.cancel()
	return ms.manager.EndSession(ms.Session.ID)
}

// WithSession creates a session, executes a function, and automatically cleans up.
// Usage:
//
//	err := WithSession(manager, opts, func(session *ManagedSession) error {
//	    // Use session...
//	    return nil
//	})
func WithSession(sm *SessionManager, opts SessionOptions, fn func(*ManagedSession) error) error {
	session, err := sm.CreateSession(opts)
	if err != nil {
		return err
	}

	ctx, cancel := context.WithCancel(context.Background())
	managed := &ManagedSession{
		Session: session,
		manager: sm,
		ctx:     ctx,
		cancel:  cancel,
	}

	defer func() {
		cancel()
		sm.EndSession(session.ID)
	}()

	return fn(managed)
}

// WithSessionContext creates a session with a provided context for timeout/cancellation control.
func WithSessionContext(ctx context.Context, sm *SessionManager, opts SessionOptions, fn func(*ManagedSession) error) error {
	session, err := sm.CreateSession(opts)
	if err != nil {
		return err
	}

	ctx, cancel := context.WithCancel(ctx)
	managed := &ManagedSession{
		Session: session,
		manager: sm,
		ctx:     ctx,
		cancel:  cancel,
	}

	defer func() {
		cancel()
		sm.EndSession(session.ID)
	}()

	// Check for context cancellation
	done := make(chan error, 1)
	go func() {
		done <- fn(managed)
	}()

	select {
	case err := <-done:
		return err
	case <-ctx.Done():
		return ctx.Err()
	}
}
