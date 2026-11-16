package session

import (
	"context"
	"fmt"
	"sort"
	"sync"
	"time"
)

// Event describes a session change, broadcast to interested transports
// (WebSocket clients, AI engine, ...).
type Event struct {
	SessionID ID     `json:"sessionId"`
	Type      string `json:"event"`         // "created" | "closed" | "cwd"
	Cwd       string `json:"cwd,omitempty"` // set when Type == "cwd"
}

// EventListener receives session events. Must not block.
type EventListener func(Event)

// Options tunes session creation.
type Options struct {
	Label    string
	Cols     int
	Rows     int
	RingSize int // replay buffer capacity in bytes; 0 = default
	// ShellIntegration installs the OSC 7 cwd hook in the remote shell so
	// the file panel follows `cd`. Off by default: it writes a visible
	// command into the user's terminal.
	ShellIntegration bool
}

const defaultRingSize = 1 << 20 // 1 MiB

// Manager owns all live sessions. Sessions persist until explicitly closed
// or their backend dies — never because a client disconnected.
type Manager struct {
	mu        sync.Mutex
	sessions  map[ID]*Session
	listeners []EventListener
}

func NewManager() *Manager {
	return &Manager{sessions: make(map[ID]*Session)}
}

// OnEvent registers a lifecycle listener.
func (m *Manager) OnEvent(l EventListener) {
	m.mu.Lock()
	m.listeners = append(m.listeners, l)
	m.mu.Unlock()
}

func (m *Manager) emit(ev Event) {
	m.mu.Lock()
	ls := make([]EventListener, len(m.listeners))
	copy(ls, m.listeners)
	m.mu.Unlock()
	for _, l := range ls {
		l(ev)
	}
}

// Create opens a terminal on the given backend and registers the session.
// The backend is owned by the session from this point on (closed on failure).
func (m *Manager) Create(backend Backend, opts Options) (*Session, error) {
	cols, rows := opts.Cols, opts.Rows
	if cols <= 0 {
		cols = 80
	}
	if rows <= 0 {
		rows = 24
	}
	ringSize := opts.RingSize
	if ringSize <= 0 {
		ringSize = defaultRingSize
	}

	tio, err := backend.OpenTerminal(cols, rows)
	if err != nil {
		backend.Close()
		return nil, fmt.Errorf("open terminal: %w", err)
	}

	s := &Session{
		id:        newID(),
		label:     opts.Label,
		backend:   backend,
		state:     StateActive,
		createdAt: time.Now(),
	}
	if s.label == "" {
		s.label = backend.Target()
	}
	s.term = makeTerminal(tio, cols, rows, ringSize, func() {
		// Backend stream ended (remote exit, network drop, ...).
		s.setState(StateClosed)
		s.closeBackend()
		m.emit(Event{SessionID: s.id, Type: "closed"})
	})
	// Shell integration (OSC 7) drives the file panel's directory.
	s.term.OnCwdChange(func(dir string) {
		m.emit(Event{SessionID: s.id, Type: "cwd", Cwd: dir})
	})

	m.mu.Lock()
	m.sessions[s.id] = s
	m.mu.Unlock()
	m.emit(Event{SessionID: s.id, Type: "created"})
	go s.term.readPump()
	if opts.ShellIntegration {
		s.term.Write([]byte(ShellIntegration()))
	}
	return s, nil
}

func (m *Manager) Get(id ID) (*Session, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	s, ok := m.sessions[id]
	return s, ok
}

// List returns session snapshots ordered by creation time.
func (m *Manager) List() []Info {
	m.mu.Lock()
	sessions := make([]*Session, 0, len(m.sessions))
	for _, s := range m.sessions {
		sessions = append(sessions, s)
	}
	m.mu.Unlock()

	infos := make([]Info, 0, len(sessions))
	for _, s := range sessions {
		infos = append(infos, s.Info())
	}
	sort.Slice(infos, func(i, j int) bool { return infos[i].CreatedAt.Before(infos[j].CreatedAt) })
	return infos
}

// Close terminates a session and removes it from the registry.
func (m *Manager) Close(id ID) error {
	m.mu.Lock()
	s, ok := m.sessions[id]
	delete(m.sessions, id)
	m.mu.Unlock()
	if !ok {
		return fmt.Errorf("session %s not found", id)
	}
	err := s.Close()
	return err
}

// ReapIdle closes sessions that have had no attached client for longer
// than ttl and returns how many were closed. Called by the janitor loop.
func (m *Manager) ReapIdle(ttl time.Duration) int {
	m.mu.Lock()
	var idle []*Session
	for _, s := range m.sessions {
		if since, ok := s.Terminal().IdleSince(); ok && time.Since(since) >= ttl {
			idle = append(idle, s)
		}
	}
	m.mu.Unlock()

	for _, s := range idle {
		m.Close(s.ID())
	}
	return len(idle)
}

// StartJanitor reaps idle detached sessions every interval until ctx ends.
// Call once from main; tests call ReapIdle directly.
func (m *Manager) StartJanitor(ctx context.Context, interval, ttl time.Duration) {
	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				m.ReapIdle(ttl)
			case <-ctx.Done():
				return
			}
		}
	}()
}

// CloseAll shuts down every session (server shutdown).
func (m *Manager) CloseAll() {
	m.mu.Lock()
	sessions := make([]*Session, 0, len(m.sessions))
	for _, s := range m.sessions {
		sessions = append(sessions, s)
	}
	m.sessions = map[ID]*Session{}
	m.mu.Unlock()
	for _, s := range sessions {
		s.Close()
	}
}
