package session

import (
	"bytes"
	"io"
	"sync"
	"testing"
	"time"
)

// pipeIO is an in-memory TerminalIO for tests.
type pipeIO struct {
	out *io.PipeReader // terminal output read by Terminal.readPump
	in  *io.PipeWriter // test writes output here
	got bytes.Buffer   // input written by clients

	mu   sync.Mutex
	cols int // last size pushed via Resize
	rows int
	n    int // how many times Resize was called
}

func newPipeIO() *pipeIO {
	pr, pw := io.Pipe()
	return &pipeIO{out: pr, in: pw}
}

func (p *pipeIO) Read(b []byte) (int, error)  { return p.out.Read(b) }
func (p *pipeIO) Write(b []byte) (int, error) { return p.got.Write(b) }
func (p *pipeIO) Close() error                { return p.out.Close() }

func (p *pipeIO) Resize(cols, rows int) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.cols, p.rows, p.n = cols, rows, p.n+1
	return nil
}

// size reports the last size pushed to the backend and the call count.
func (p *pipeIO) size() (cols, rows, calls int) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.cols, p.rows, p.n
}

func collect(t *testing.T, out <-chan []byte, closed <-chan struct{}, want []byte, timeout time.Duration) []byte {
	t.Helper()
	var buf bytes.Buffer
	deadline := time.After(timeout)
	for buf.Len() < len(want) {
		select {
		case data, ok := <-out:
			if !ok {
				return buf.Bytes()
			}
			buf.Write(data)
		case <-closed:
			// Drain buffered data after close.
			for {
				select {
				case data, ok := <-out:
					if ok {
						buf.Write(data)
						continue
					}
				default:
				}
				return buf.Bytes()
			}
		case <-deadline:
			t.Fatalf("timeout; got %q so far", buf.Bytes())
		}
	}
	return buf.Bytes()
}

func TestTerminalReplayThenLive(t *testing.T) {
	pio := newPipeIO()
	term := newTerminal(pio, 80, 24, 64, nil)

	pio.in.Write([]byte("early-output"))
	// Wait until the read pump has buffered it.
	waitFor(t, func() bool {
		a := term.Attach(SizeNegotiate, 80, 24)
		a.Detach()
		return bytes.Contains(a.Replay, []byte("early-output"))
	})

	a := term.Attach(SizeNegotiate, 80, 24)
	defer a.Detach()
	out, closed := a.Out, a.Closed
	if !bytes.Equal(a.Replay, []byte("early-output")) {
		t.Fatalf("replay = %q", a.Replay)
	}

	pio.in.Write([]byte("live"))
	if got := collect(t, out, closed, []byte("live"), time.Second); !bytes.Equal(got, []byte("live")) {
		t.Fatalf("live = %q", got)
	}
}

func TestTerminalMultiSubscriberBroadcast(t *testing.T) {
	pio := newPipeIO()
	term := newTerminal(pio, 80, 24, 64, nil)

	a1 := term.Attach(SizeNegotiate, 80, 24)
	defer a1.Detach()
	a2 := term.Attach(SizeNegotiate, 80, 24)
	defer a2.Detach()
	out1, closed1 := a1.Out, a1.Closed
	out2, closed2 := a2.Out, a2.Closed

	pio.in.Write([]byte("hello"))
	if got := collect(t, out1, closed1, []byte("hello"), time.Second); !bytes.Equal(got, []byte("hello")) {
		t.Fatalf("sub1 = %q", got)
	}
	if got := collect(t, out2, closed2, []byte("hello"), time.Second); !bytes.Equal(got, []byte("hello")) {
		t.Fatalf("sub2 = %q", got)
	}
}

func TestTerminalCloseNotifiesSubscribers(t *testing.T) {
	pio := newPipeIO()
	exited := make(chan struct{})
	term := newTerminal(pio, 80, 24, 64, func() { close(exited) })

	a := term.Attach(SizeNegotiate, 80, 24)
	defer a.Detach()
	closed := a.Closed

	pio.in.Close() // simulate remote EOF
	select {
	case <-closed:
	case <-time.After(time.Second):
		t.Fatal("subscriber not notified of close")
	}
	select {
	case <-exited:
	case <-time.After(time.Second):
		t.Fatal("onExit not called")
	}
}

func TestTerminalInputMerged(t *testing.T) {
	pio := newPipeIO()
	term := newTerminal(pio, 80, 24, 64, nil)
	term.Write([]byte("ab"))
	term.Write([]byte("cd"))
	if got := pio.got.Bytes(); !bytes.Equal(got, []byte("abcd")) {
		t.Fatalf("input = %q", got)
	}
}

// A phone attaching to watch a session must not resize the PTY out from
// under the desktop that owns it — the bug this size negotiation fixes.
func TestTerminalPassiveAttachLeavesSizeAlone(t *testing.T) {
	pio := newPipeIO()
	term := newTerminal(pio, 80, 24, 64, nil)

	desktop := term.Attach(SizeNegotiate, 120, 40)
	defer desktop.Detach()
	if cols, rows, _ := pio.size(); cols != 120 || rows != 40 {
		t.Fatalf("after desktop attach = %dx%d, want 120x40", cols, rows)
	}

	phone := term.Attach(SizePassive, 40, 20)
	defer phone.Detach()
	if cols, rows, _ := pio.size(); cols != 120 || rows != 40 {
		t.Fatalf("passive attach resized PTY to %dx%d, want 120x40", cols, rows)
	}

	// A passive client reporting a new viewport is ignored too.
	phone.Resize(30, 15)
	if cols, rows, _ := pio.size(); cols != 120 || rows != 40 {
		t.Fatalf("passive resize changed PTY to %dx%d, want 120x40", cols, rows)
	}
	if got, _ := term.Size(); got != 120 {
		t.Fatalf("term.Size cols = %d, want 120", got)
	}
}

func TestTerminalNegotiatesSmallestViewport(t *testing.T) {
	pio := newPipeIO()
	term := newTerminal(pio, 80, 24, 64, nil)

	wide := term.Attach(SizeNegotiate, 120, 40)
	defer wide.Detach()
	// Narrower columns but taller rows: each dimension is minimised
	// independently, so nothing wraps off-screen for either client.
	narrow := term.Attach(SizeNegotiate, 100, 50)
	if cols, rows, _ := pio.size(); cols != 100 || rows != 40 {
		t.Fatalf("negotiated %dx%d, want 100x40", cols, rows)
	}

	// The constraining client leaves: the PTY grows back.
	narrow.Detach()
	if cols, rows, _ := pio.size(); cols != 120 || rows != 40 {
		t.Fatalf("after detach = %dx%d, want 120x40", cols, rows)
	}
}

// With every client gone the PTY keeps its last size rather than snapping
// back to a default; a reattaching client replays into the same geometry.
func TestTerminalSizeSurvivesLastDetach(t *testing.T) {
	pio := newPipeIO()
	term := newTerminal(pio, 80, 24, 64, nil)

	a := term.Attach(SizeNegotiate, 120, 40)
	a.Detach()
	_, _, before := pio.size()

	if cols, rows := term.Size(); cols != 120 || rows != 40 {
		t.Fatalf("size after detach = %dx%d, want 120x40", cols, rows)
	}
	if _, _, after := pio.size(); after != before {
		t.Fatalf("detach pushed a redundant resize (%d → %d)", before, after)
	}
}

func waitFor(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("condition not met within 1s")
}
