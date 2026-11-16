package session

import (
	"context"
	"io"
	"strings"
	"testing"
	"time"
)

// stubBackend is an in-memory Backend whose terminal echoes nothing; tests
// drive output by writing to tio.in directly.
type stubBackend struct {
	tio    *pipeIO
	closed chan struct{}
}

func newStubBackend() *stubBackend {
	return &stubBackend{tio: newPipeIO(), closed: make(chan struct{})}
}

func (b *stubBackend) Kind() string   { return "stub" }
func (b *stubBackend) Target() string { return "stub@test" }
func (b *stubBackend) OpenTerminal(cols, rows int) (TerminalIO, error) {
	return b.tio, nil
}
func (b *stubBackend) Close() error {
	select {
	case <-b.closed:
	default:
		close(b.closed)
	}
	return nil
}
func (b *stubBackend) Exec(ctx context.Context, cmd string) ([]byte, []byte, int, error) {
	return []byte("stub:" + cmd), nil, 0, nil
}
func (b *stubBackend) ExecStream(ctx context.Context, cmd string) (io.ReadCloser, error) {
	return io.NopCloser(strings.NewReader("stub-stream:" + cmd)), nil
}

func TestSessionSurvivesDetach(t *testing.T) {
	mgr := NewManager()
	backend := newStubBackend()
	sess, err := mgr.Create(backend, Options{Label: "s1"})
	if err != nil {
		t.Fatal(err)
	}

	att := sess.Terminal().Attach(SizeNegotiate, 80, 24)
	if got := sess.Info().Attached; got != 1 {
		t.Fatalf("attached = %d, want 1", got)
	}

	// Client goes away — the session must stay alive (tmux semantics).
	att.Detach()
	if got := sess.Info().Attached; got != 0 {
		t.Fatalf("attached after detach = %d, want 0", got)
	}
	if got := sess.Info().State; got != StateActive {
		t.Fatalf("state after detach = %q, want active", got)
	}
	select {
	case <-backend.closed:
		t.Fatal("backend must not close on detach")
	default:
	}

	// Output produced while detached lands in the ring and replays on reattach.
	backend.tio.in.Write([]byte("while-away"))
	waitFor(t, func() bool {
		a := sess.Terminal().Attach(SizeNegotiate, 80, 24)
		a.Detach()
		return string(a.Replay) == "while-away"
	})
}

func TestManagerReapIdle(t *testing.T) {
	mgr := NewManager()
	idle, err := mgr.Create(newStubBackend(), Options{Label: "idle"})
	if err != nil {
		t.Fatal(err)
	}
	busy, err := mgr.Create(newStubBackend(), Options{Label: "busy"})
	if err != nil {
		t.Fatal(err)
	}
	busyAtt := busy.Terminal().Attach(SizeNegotiate, 80, 24)
	defer busyAtt.Detach()

	// Nothing is old enough yet.
	if n := mgr.ReapIdle(time.Hour); n != 0 {
		t.Fatalf("reaped %d sessions too early", n)
	}

	// A zero TTL reaps every session without an attached client.
	if n := mgr.ReapIdle(0); n != 1 {
		t.Fatalf("reaped %d, want 1 (only the detached session)", n)
	}
	if _, ok := mgr.Get(idle.ID()); ok {
		t.Fatal("idle session should be gone")
	}
	if _, ok := mgr.Get(busy.ID()); !ok {
		t.Fatal("attached session must survive reaping")
	}
}

func TestManagerEventsOnCreateAndClose(t *testing.T) {
	mgr := NewManager()
	events := make(chan Event, 8)
	mgr.OnEvent(func(ev Event) { events <- ev })

	sess, err := mgr.Create(newStubBackend(), Options{})
	if err != nil {
		t.Fatal(err)
	}
	if ev := recvEvent(t, events); ev.Type != "created" || ev.SessionID != sess.ID() {
		t.Fatalf("want created event, got %+v", ev)
	}

	mgr.Close(sess.ID())
	if ev := recvEvent(t, events); ev.Type != "closed" {
		t.Fatalf("want closed event, got %+v", ev)
	}
}

func TestBackendEOFClosesSession(t *testing.T) {
	mgr := NewManager()
	backend := newStubBackend()
	sess, err := mgr.Create(backend, Options{})
	if err != nil {
		t.Fatal(err)
	}

	backend.tio.in.CloseWithError(io.EOF) // remote shell exits
	waitFor(t, func() bool { return sess.Info().State == StateClosed })
}

func recvEvent(t *testing.T, ch <-chan Event) Event {
	t.Helper()
	select {
	case ev := <-ch:
		return ev
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for session event")
		return Event{}
	}
}
