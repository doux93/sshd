package session

import (
	"bytes"
	"testing"
	"time"
)

func TestSnapshotDoesNotResetIdleTTL(t *testing.T) {
	p := newPipeIO()
	defer p.Close()
	term := newTerminal(p, 80, 24, 64, nil)
	before, _ := term.IdleSince()
	term.Snapshot()
	after, _ := term.IdleSince()
	if before != after {
		t.Fatal("snapshot reset idle time")
	}
	att := term.Attach(SizePassive, 0, 0)
	att.Detach()
	before, _ = term.IdleSince()
	time.Sleep(time.Millisecond)
	att.Detach()
	after, _ = term.IdleSince()
	if before != after {
		t.Fatal("duplicate detach reset idle time")
	}
}
func TestAlternateScreenAcrossChunks(t *testing.T) {
	var s alternateScanner
	for _, chunk := range []string{"\x1b[?10", "49h", "screen"} {
		s.Feed([]byte(chunk))
	}
	if !s.active {
		t.Fatal("alternate screen not detected")
	}
	s.Feed([]byte("\x1b[?1049l"))
	if s.active {
		t.Fatal("alternate screen not cleared")
	}
}
func TestSlowSubscriberDoesNotCloseTerminal(t *testing.T) {
	p := newPipeIO()
	defer p.Close()
	term := newTerminal(p, 80, 24, 64, nil)
	att := term.Attach(SizePassive, 0, 0)
	for i := 0; i < subscriberBuffer+1; i++ {
		term.broadcast([]byte("x"))
	}
	select {
	case <-att.Closed:
	case <-time.After(time.Second):
		t.Fatal("slow subscriber was not evicted")
	}
	if term.IsClosed() {
		t.Fatal("terminal incorrectly closed")
	}
	replay := term.Attach(SizePassive, 0, 0)
	defer replay.Detach()
	if !bytes.Contains(replay.Replay, []byte("x")) {
		t.Fatal("replay lost")
	}
}
