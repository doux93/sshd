package session

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"io"
	"sync"
	"time"
)

// ID uniquely identifies a live session within this process.
type ID string

func newID() ID {
	b := make([]byte, 8)
	rand.Read(b)
	return ID(hex.EncodeToString(b))
}

// State is the session lifecycle state.
type State string

const (
	StateActive State = "active" // terminal open, backend healthy
	StateClosed State = "closed" // backend gone or explicitly closed
)

// Info is the read-only snapshot exposed to transports and APIs.
type Info struct {
	ID        ID        `json:"id"`
	Label     string    `json:"label"`
	Kind      string    `json:"kind"`
	Target    string    `json:"target"`
	State     State     `json:"state"`
	Attached  int       `json:"attached"` // number of live client attachments
	Cwd       string    `json:"cwd"`      // last directory reported via OSC 7
	HasFiles  bool      `json:"hasFiles"` // backend supports SFTP
	CreatedAt time.Time `json:"createdAt"`
}

// Session owns one Backend connection and its terminal channel. It outlives
// any transport attachment: closing a WebSocket does not close the session.
type Session struct {
	backendOnce sync.Once
	closeOnce   sync.Once
	closeErr    error
	mu          sync.Mutex
	id          ID
	label       string
	backend     Backend
	term        *Terminal
	state       State
	createdAt   time.Time
}

func (s *Session) ID() ID              { return s.id }
func (s *Session) Terminal() *Terminal { return s.term }

// Files returns the session's file client if its backend supports file
// transfer. ErrNoFileSupport otherwise (e.g. a serial console).
func (s *Session) Files() (FileClient, error) {
	fb, ok := s.backend.(FileBackend)
	if !ok {
		return nil, ErrNoFileSupport
	}
	return fb.OpenFiles()
}

// ErrNoFileSupport is returned by Files for backends without file transfer.
var ErrNoFileSupport = errors.New("this connection type does not support file transfer")

// Exec runs a command on a separate channel without affecting the interactive
// terminal. See Backend.Exec for semantics.
func (s *Session) Exec(ctx context.Context, cmd string) (stdout, stderr []byte, exitCode int, err error) {
	return s.backend.Exec(ctx, cmd)
}

// ExecStream starts a long-running command and returns a reader for its live
// stdout. The caller closes the reader to terminate the process.
func (s *Session) ExecStream(ctx context.Context, cmd string) (io.ReadCloser, error) {
	return s.backend.ExecStream(ctx, cmd)
}

func (s *Session) Info() Info {
	s.mu.Lock()
	defer s.mu.Unlock()
	attached, cwd := 0, ""
	if s.term != nil {
		attached = s.term.AttachedCount()
		cwd = s.term.Cwd()
	}
	_, hasFiles := s.backend.(FileBackend)
	return Info{
		ID:        s.id,
		Label:     s.label,
		Kind:      s.backend.Kind(),
		Target:    s.backend.Target(),
		State:     s.state,
		Attached:  attached,
		Cwd:       cwd,
		HasFiles:  hasFiles,
		CreatedAt: s.createdAt,
	}
}

func (s *Session) setState(st State) {
	s.mu.Lock()
	s.state = st
	s.mu.Unlock()
}

// Close tears down the terminal and backend.
func (s *Session) Close() error {
	s.closeOnce.Do(func() {
		s.setState(StateClosed)
		// Close the backend first to unblock any pending terminal resize/write.
		s.closeBackend()
		if s.term != nil {
			s.term.close()
		}
	})
	return s.closeErr
}

func (s *Session) closeBackend() { s.backendOnce.Do(func() { s.closeErr = s.backend.Close() }) }
