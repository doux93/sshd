package session

import (
	"sync"
	"time"
)

// SizeMode decides whether an attachment takes part in PTY size negotiation.
type SizeMode string

const (
	// SizeNegotiate: this client's viewport constrains the PTY. The PTY is
	// sized to the smallest viewport among negotiating clients so nothing
	// wraps off-screen for anyone. Desktop browsers use this.
	SizeNegotiate SizeMode = "negotiate"
	// SizePassive: this client renders whatever size the PTY already is and
	// scales or scrolls locally. Phones use this: attaching from a phone to
	// watch a session must not shrink the terminal on the desktop that owns
	// it.
	SizePassive SizeMode = "passive"
)

// subscriber receives terminal output. Data is delivered through a buffered
// channel; a subscriber that falls too far behind is dropped (the transport
// is expected to reattach, which replays the ring buffer).
type subscriber struct {
	data   chan []byte
	closed chan struct{} // closed exactly once when the terminal ends
	once   sync.Once

	// Viewport last reported by this client, guarded by Terminal.mu.
	// Zero for passive clients and for clients that never reported one.
	mode SizeMode
	cols int
	rows int

	// size carries the authoritative PTY geometry to the client, which must
	// render at it regardless of its own viewport. Capacity 1 and coalescing:
	// only the latest size matters.
	size chan [2]int
}

// pushSize delivers the current geometry, replacing any size this subscriber
// has not read yet. Caller must hold Terminal.mu.
func (s *subscriber) pushSize(cols, rows int) {
	select {
	case <-s.size:
	default:
	}
	select {
	case s.size <- [2]int{cols, rows}:
	default:
	}
}

func (s *subscriber) close() {
	s.once.Do(func() { close(s.closed) })
}

// Terminal wraps a Backend terminal channel with a replay ring buffer and
// fan-out to any number of attached clients. Input from all clients is
// merged; output is broadcast.
type Terminal struct {
	writeMu   sync.Mutex
	resizeMu  sync.Mutex
	alt       alternateScanner
	mu        sync.Mutex
	io        TerminalIO
	ring      *ring
	subs      map[*subscriber]struct{}
	cols      int
	rows      int
	done      bool
	onExit    func()
	lastEmpty time.Time // when the subscriber count last dropped to zero

	scanner osc7Scanner // extracts cwd from the output stream
	cwd     string
	onCwd   func(string) // notified when the reported cwd changes
}

const subscriberBuffer = 256

func newTerminal(tio TerminalIO, cols, rows, ringCapacity int, onExit func()) *Terminal {
	t := makeTerminal(tio, cols, rows, ringCapacity, onExit)
	go t.readPump()
	return t
}

func makeTerminal(tio TerminalIO, cols, rows, ringCapacity int, onExit func()) *Terminal {
	t := &Terminal{
		io:        tio,
		ring:      newRing(ringCapacity),
		subs:      make(map[*subscriber]struct{}),
		cols:      cols,
		rows:      rows,
		onExit:    onExit,
		lastEmpty: time.Now(),
	}
	return t
}

// readPump copies backend output into the ring buffer and to subscribers
// until the backend stream ends.
func (t *Terminal) readPump() {
	buf := make([]byte, 32*1024)
	for {
		n, err := t.io.Read(buf)
		if n > 0 {
			chunk := make([]byte, n)
			copy(chunk, buf[:n])
			t.broadcast(chunk)
		}
		if err != nil {
			t.shutdown()
			return
		}
	}
}

func (t *Terminal) broadcast(chunk []byte) {
	t.mu.Lock()
	t.ring.Write(chunk)
	t.alt.Feed(chunk)
	// Watch the same bytes for OSC 7 so the file panel can follow `cd`.
	var cwdChanged string
	if dir := t.scanner.Feed(chunk); dir != "" && dir != t.cwd {
		t.cwd = dir
		cwdChanged = dir
	}
	dropped := false
	for sub := range t.subs {
		select {
		case sub.data <- chunk:
		default:
			// Subscriber too slow: drop it rather than block or lose
			// interleaved output. It can reattach and replay the ring.
			delete(t.subs, sub)
			if len(t.subs) == 0 {
				t.lastEmpty = time.Now()
			}
			sub.close()
			dropped = true
		}
	}
	// Losing a client can relax the size constraint (§4.1 of the design).
	resize := false
	if dropped {
		_, _, resize = t.renegotiateLocked()
	}
	onCwd := t.onCwd
	t.mu.Unlock()

	if resize {
		t.applySize()
	}
	if cwdChanged != "" && onCwd != nil {
		onCwd(cwdChanged)
	}
}

// renegotiateLocked recomputes the PTY size implied by the attached clients:
// the smallest viewport among negotiating attachments. Passive clients are
// ignored, and when nothing constrains the size the PTY keeps its current
// dimensions rather than snapping back to a default.
//
// Caller must hold t.mu. Returns changed=true when the caller should push the
// new size to the backend — do that after unlocking, since it is a network
// round-trip.
func (t *Terminal) renegotiateLocked() (cols, rows int, changed bool) {
	found := false
	for sub := range t.subs {
		if sub.mode != SizeNegotiate || sub.cols <= 0 || sub.rows <= 0 {
			continue
		}
		if !found || sub.cols < cols {
			cols = sub.cols
		}
		if !found || sub.rows < rows {
			rows = sub.rows
		}
		found = true
	}
	if !found || (cols == t.cols && rows == t.rows) {
		return 0, 0, false
	}
	t.cols, t.rows = cols, rows
	// Every client renders at the PTY size, not at its own viewport.
	for sub := range t.subs {
		sub.pushSize(cols, rows)
	}
	return cols, rows, true
}

// Cwd returns the last directory reported via OSC 7, or "" if the remote
// shell has no shell-integration hook installed.
func (t *Terminal) Cwd() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.cwd
}

// OnCwdChange registers the (single) listener notified when the terminal
// reports a new working directory.
func (t *Terminal) OnCwdChange(f func(string)) {
	t.mu.Lock()
	t.onCwd = f
	t.mu.Unlock()
}

func (t *Terminal) shutdown() {
	t.mu.Lock()
	if t.done {
		t.mu.Unlock()
		return
	}
	t.done = true
	subs := make([]*subscriber, 0, len(t.subs))
	for sub := range t.subs {
		subs = append(subs, sub)
	}
	t.subs = map[*subscriber]struct{}{}
	t.mu.Unlock()

	for _, sub := range subs {
		sub.close()
	}
	if t.onExit != nil {
		t.onExit()
	}
}

// Attachment is one client's live subscription to a terminal.
type Attachment struct {
	// Replay is the ring-buffer snapshot taken atomically with the
	// subscription, so no output is lost or duplicated between it and Out.
	Replay []byte
	// Out delivers live terminal output.
	Out <-chan []byte
	// Closed is closed when the terminal ends.
	Closed <-chan struct{}
	// Sizes delivers the PTY geometry the client must render at: once on
	// attach, then whenever negotiation changes it.
	Sizes <-chan [2]int

	term *Terminal
	sub  *subscriber
}

// Attach subscribes a client to terminal output. mode decides whether this
// client's viewport constrains the PTY size; cols/rows may be zero when the
// client has not measured itself yet.
func (t *Terminal) Attach(mode SizeMode, cols, rows int) *Attachment {
	if mode != SizeNegotiate && mode != SizePassive {
		mode = SizeNegotiate
	}
	sub := &subscriber{
		data:   make(chan []byte, subscriberBuffer),
		closed: make(chan struct{}),
		mode:   mode,
		size:   make(chan [2]int, 1),
	}
	if mode == SizeNegotiate && cols > 0 && rows > 0 && cols <= 1000 && rows <= 1000 {
		sub.cols, sub.rows = cols, rows
	}

	t.mu.Lock()
	att := &Attachment{
		Replay: t.ring.Snapshot(),
		Out:    sub.data,
		Closed: sub.closed,
		Sizes:  sub.size,
		term:   t,
		sub:    sub,
	}
	if t.done {
		t.mu.Unlock()
		sub.close()
		return att
	}
	t.subs[sub] = struct{}{}
	_, _, resize := t.renegotiateLocked()
	// Tell the newcomer the geometry even when negotiation changed nothing:
	// a passive client has no other way to learn it.
	sub.pushSize(t.cols, t.rows)
	t.mu.Unlock()

	if resize {
		t.applySize()
	}
	return att
}

// Resize reports a new viewport for this client. Passive clients are ignored
// (they scale locally); for negotiating clients the PTY is resized to the
// smallest viewport currently attached.
func (a *Attachment) Resize(cols, rows int) error {
	if a.sub.mode != SizeNegotiate || cols <= 0 || rows <= 0 || cols > 1000 || rows > 1000 {
		return nil
	}
	t := a.term
	t.mu.Lock()
	a.sub.cols, a.sub.rows = cols, rows
	_, _, changed := t.renegotiateLocked()
	t.mu.Unlock()
	if !changed {
		return nil
	}
	return t.applySize()
}

// Detach unsubscribes the client. The session and its terminal keep running.
// Safe to call more than once.
func (a *Attachment) Detach() {
	t := a.term
	t.mu.Lock()
	_, present := t.subs[a.sub]
	delete(t.subs, a.sub)
	if present && len(t.subs) == 0 {
		t.lastEmpty = time.Now()
	}
	// This client may have been the one holding the PTY small.
	_, _, resize := t.renegotiateLocked()
	t.mu.Unlock()

	if resize {
		t.applySize()
	}
	a.sub.close()
}

// AttachedCount reports how many clients are currently attached.
func (t *Terminal) AttachedCount() int {
	t.mu.Lock()
	defer t.mu.Unlock()
	return len(t.subs)
}

// IdleSince returns when the terminal lost its last client. ok=false while
// clients are attached.
func (t *Terminal) IdleSince() (time.Time, bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if len(t.subs) > 0 {
		return time.Time{}, false
	}
	return t.lastEmpty, true
}

// Write sends client input (keystrokes) to the backend terminal.
func (t *Terminal) Write(p []byte) (int, error) {
	t.writeMu.Lock()
	defer t.writeMu.Unlock()
	return t.io.Write(p)
}

// Size returns the current PTY dimensions.
func (t *Terminal) Size() (cols, rows int) {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.cols, t.rows
}

func (t *Terminal) close() error {
	err := t.io.Close()
	t.shutdown()
	return err
}

func (t *Terminal) applySize() error {
	t.resizeMu.Lock()
	defer t.resizeMu.Unlock()
	t.mu.Lock()
	cols, rows, done := t.cols, t.rows, t.done
	t.mu.Unlock()
	if done {
		return nil
	}
	return t.io.Resize(cols, rows)
}
func (t *Terminal) IsClosed() bool { t.mu.Lock(); defer t.mu.Unlock(); return t.done }

// Snapshot reads recent raw output without creating an attachment or resetting idle TTL.
func (t *Terminal) Snapshot() ([]byte, bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.ring.Snapshot(), t.alt.active
}
