package session

import (
	"testing"
	"time"
)

func timeoutAfterSecond() <-chan time.Time { return time.After(time.Second) }

func TestOSC7BasicBEL(t *testing.T) {
	var s osc7Scanner
	got := s.Feed([]byte("prompt\x1b]7;file://myhost/var/log\x07$ "))
	if got != "/var/log" {
		t.Fatalf("got %q, want /var/log", got)
	}
}

func TestOSC7StTerminator(t *testing.T) {
	var s osc7Scanner
	// ST terminator (ESC backslash) instead of BEL.
	got := s.Feed([]byte("\x1b]7;file://h/home/me\x1b\\"))
	if got != "/home/me" {
		t.Fatalf("got %q, want /home/me", got)
	}
}

func TestOSC7SplitAcrossReads(t *testing.T) {
	var s osc7Scanner
	if got := s.Feed([]byte("noise\x1b]7;file://h/usr")); got != "" {
		t.Fatalf("incomplete sequence returned %q", got)
	}
	if got := s.Feed([]byte("/local/bin\x07more")); got != "/usr/local/bin" {
		t.Fatalf("got %q, want /usr/local/bin", got)
	}
}

func TestOSC7PrefixSplitAcrossReads(t *testing.T) {
	var s osc7Scanner
	// The prefix itself straddles the chunk boundary.
	if got := s.Feed([]byte("output\x1b]")); got != "" {
		t.Fatalf("partial prefix returned %q", got)
	}
	if got := s.Feed([]byte("7;file://h/tmp\x07")); got != "/tmp" {
		t.Fatalf("got %q, want /tmp", got)
	}
}

func TestOSC7LastWins(t *testing.T) {
	var s osc7Scanner
	got := s.Feed([]byte("\x1b]7;file://h/first\x07mid\x1b]7;file://h/second\x07"))
	if got != "/second" {
		t.Fatalf("got %q, want /second (last reported dir)", got)
	}
}

func TestOSC7PercentDecoding(t *testing.T) {
	var s osc7Scanner
	got := s.Feed([]byte("\x1b]7;file://h/data/my%20dir\x07"))
	if got != "/data/my dir" {
		t.Fatalf("got %q, want %q", got, "/data/my dir")
	}
}

func TestOSC7IgnoresOtherSequences(t *testing.T) {
	var s osc7Scanner
	// Window title (OSC 0) and colors must not be mistaken for cwd.
	got := s.Feed([]byte("\x1b]0;my title\x07\x1b[31mred\x1b[0m"))
	if got != "" {
		t.Fatalf("unrelated sequences returned %q", got)
	}
}

func TestOSC7BarePathAccepted(t *testing.T) {
	var s osc7Scanner
	// Some shells emit the path without the file:// scheme.
	if got := s.Feed([]byte("\x1b]7;/srv/app\x07")); got != "/srv/app" {
		t.Fatalf("got %q, want /srv/app", got)
	}
	// A relative path is not trustworthy and must be ignored.
	var s2 osc7Scanner
	if got := s2.Feed([]byte("\x1b]7;relative/path\x07")); got != "" {
		t.Fatalf("relative path returned %q", got)
	}
}

func TestOSC7NoUnboundedBuffering(t *testing.T) {
	var s osc7Scanner
	// A never-terminated sequence must not grow the buffer without bound.
	s.Feed([]byte("\x1b]7;file://h/"))
	big := make([]byte, osc7MaxLen*2)
	for i := range big {
		big[i] = 'a'
	}
	s.Feed(big)
	if len(s.buf) > osc7MaxLen {
		t.Fatalf("buffer grew to %d bytes, cap is %d", len(s.buf), osc7MaxLen)
	}
}

func TestTerminalReportsCwd(t *testing.T) {
	pio := newPipeIO()
	term := newTerminal(pio, 80, 24, 1024, nil)

	changes := make(chan string, 4)
	term.OnCwdChange(func(dir string) { changes <- dir })

	pio.in.Write([]byte("\x1b]7;file://host/opt/app\x07$ "))
	select {
	case dir := <-changes:
		if dir != "/opt/app" {
			t.Fatalf("notified %q, want /opt/app", dir)
		}
	case <-timeoutAfterSecond():
		t.Fatal("no cwd notification")
	}
	if got := term.Cwd(); got != "/opt/app" {
		t.Fatalf("Cwd() = %q", got)
	}
}
