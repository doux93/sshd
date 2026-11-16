package server

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"

	"sshx/internal/auth"
	"sshx/internal/session"
)

// stubBackend is an in-memory session.Backend whose terminal echoes input
// prefixed with "echo:".
type stubBackend struct {
	tio *stubTerminalIO
}

func (b *stubBackend) Kind() string   { return "stub" }
func (b *stubBackend) Target() string { return "stub@test" }
func (b *stubBackend) Close() error   { return nil }
func (b *stubBackend) OpenTerminal(cols, rows int) (session.TerminalIO, error) {
	return b.tio, nil
}
func (b *stubBackend) Exec(ctx context.Context, cmd string) ([]byte, []byte, int, error) {
	return []byte("stub:" + cmd), nil, 0, nil
}
func (b *stubBackend) ExecStream(ctx context.Context, cmd string) (io.ReadCloser, error) {
	return io.NopCloser(strings.NewReader("stub-stream:" + cmd)), nil
}

type stubTerminalIO struct {
	out *io.PipeReader
	in  *io.PipeWriter
}

func newStubBackend() *stubBackend {
	pr, pw := io.Pipe()
	tio := &stubTerminalIO{out: pr, in: pw}
	return &stubBackend{tio: tio}
}

func (t *stubTerminalIO) Read(p []byte) (int, error) { return t.out.Read(p) }
func (t *stubTerminalIO) Write(p []byte) (int, error) {
	// Echo input back as output, tagged so tests can tell it apart.
	go t.in.Write(append([]byte("echo:"), p...))
	return len(p), nil
}
func (t *stubTerminalIO) Resize(cols, rows int) error { return nil }
func (t *stubTerminalIO) Close() error                { return t.out.Close() }

func TestWebSocketAttachInputOutput(t *testing.T) {
	mgr := session.NewManager()
	tokenAuth := auth.NewToken()
	srv := New(Deps{Sessions: mgr, Authn: tokenAuth, Log: slog.New(slog.NewTextHandler(io.Discard, nil))})

	backend := newStubBackend()
	sess, err := mgr.Create(backend, session.Options{})
	if err != nil {
		t.Fatal(err)
	}

	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	wsURL := strings.Replace(ts.URL, "http://", "ws://", 1) +
		"/ws?token=" + tokenAuth.Secret()
	conn, _, err := websocket.Dial(ctx, wsURL, nil)
	if err != nil {
		t.Fatalf("ws dial: %v", err)
	}
	defer conn.Close(websocket.StatusNormalClosure, "")
	conn.SetReadLimit(1 << 20)

	// Attach on channel 7.
	attach, _ := json.Marshal(map[string]any{
		"type": "attach", "sessionId": string(sess.ID()), "channelId": 7,
		"cols": 80, "rows": 24,
	})
	if err := conn.Write(ctx, websocket.MessageText, attach); err != nil {
		t.Fatal(err)
	}

	// Expect the "attached" ack.
	readControl := func() map[string]any {
		for {
			typ, data, err := conn.Read(ctx)
			if err != nil {
				t.Fatalf("ws read: %v", err)
			}
			if typ == websocket.MessageText {
				var m map[string]any
				json.Unmarshal(data, &m)
				return m
			}
		}
	}
	if m := readControl(); m["type"] != "attached" {
		t.Fatalf("expected attached ack, got %v", m)
	}

	// Send input on channel 7 and expect the echoed output frame back.
	input := append([]byte{7}, []byte("ping")...)
	if err := conn.Write(ctx, websocket.MessageBinary, input); err != nil {
		t.Fatal(err)
	}
	var got []byte
	want := []byte("echo:ping")
	for len(got) < len(want) {
		typ, data, err := conn.Read(ctx)
		if err != nil {
			t.Fatalf("ws read: %v (got %q so far)", err, got)
		}
		if typ != websocket.MessageBinary {
			continue
		}
		if len(data) < 1 || data[0] != 7 {
			t.Fatalf("unexpected frame: %v", data)
		}
		got = append(got, data[1:]...)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("output = %q, want %q", got, want)
	}
}

// A phone attaching in passive mode must not resize the terminal a desktop
// client is using. Regression test for attach unconditionally resizing.
func TestWebSocketPassiveAttachDoesNotResize(t *testing.T) {
	mgr := session.NewManager()
	tokenAuth := auth.NewToken()
	srv := New(Deps{Sessions: mgr, Authn: tokenAuth, Log: slog.New(slog.NewTextHandler(io.Discard, nil))})

	sess, err := mgr.Create(newStubBackend(), session.Options{})
	if err != nil {
		t.Fatal(err)
	}

	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	wsURL := strings.Replace(ts.URL, "http://", "ws://", 1) + "/ws?token=" + tokenAuth.Secret()

	// attachClient dials, attaches, and returns once the server acks — so
	// any resize the attach caused has already been applied.
	attachClient := func(sizeMode string, cols, rows int) *websocket.Conn {
		t.Helper()
		conn, _, err := websocket.Dial(ctx, wsURL, nil)
		if err != nil {
			t.Fatalf("ws dial: %v", err)
		}
		msg, _ := json.Marshal(map[string]any{
			"type": "attach", "sessionId": string(sess.ID()), "channelId": 1,
			"cols": cols, "rows": rows, "sizeMode": sizeMode,
		})
		if err := conn.Write(ctx, websocket.MessageText, msg); err != nil {
			t.Fatal(err)
		}
		for {
			typ, data, err := conn.Read(ctx)
			if err != nil {
				t.Fatalf("ws read: %v", err)
			}
			if typ != websocket.MessageText {
				continue
			}
			var m map[string]any
			json.Unmarshal(data, &m)
			if m["type"] == "attached" {
				return conn
			}
		}
	}

	desktop := attachClient("negotiate", 120, 40)
	defer desktop.Close(websocket.StatusNormalClosure, "")
	if cols, rows := sess.Terminal().Size(); cols != 120 || rows != 40 {
		t.Fatalf("after desktop attach = %dx%d, want 120x40", cols, rows)
	}

	phone := attachClient("passive", 40, 20)
	defer phone.Close(websocket.StatusNormalClosure, "")
	if cols, rows := sess.Terminal().Size(); cols != 120 || rows != 40 {
		t.Fatalf("passive attach resized to %dx%d, want 120x40", cols, rows)
	}

	// A passive client's later resize frames are ignored as well.
	resize, _ := json.Marshal(map[string]any{
		"type": "resize", "channelId": 1, "cols": 30, "rows": 15,
	})
	if err := phone.Write(ctx, websocket.MessageText, resize); err != nil {
		t.Fatal(err)
	}
	// Round-trip a second attach to be sure the resize frame was processed.
	spare := attachClient("passive", 40, 20)
	defer spare.Close(websocket.StatusNormalClosure, "")
	if cols, rows := sess.Terminal().Size(); cols != 120 || rows != 40 {
		t.Fatalf("passive resize changed size to %dx%d, want 120x40", cols, rows)
	}
}

func TestWebSocketRejectsBadToken(t *testing.T) {
	mgr := session.NewManager()
	srv := New(Deps{Sessions: mgr, Authn: auth.NewToken(), Log: slog.New(slog.NewTextHandler(io.Discard, nil))})
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	wsURL := strings.Replace(ts.URL, "http://", "ws://", 1) + "/ws?token=wrong"
	if _, _, err := websocket.Dial(ctx, wsURL, nil); err == nil {
		t.Fatal("expected dial to fail with bad token")
	}
}

func TestReattachReplaysRingBuffer(t *testing.T) {
	mgr := session.NewManager()
	defer mgr.CloseAll()
	tokenAuth := auth.NewToken()
	srv := New(Deps{Sessions: mgr, Authn: tokenAuth, Log: slog.New(slog.NewTextHandler(io.Discard, nil))})

	backend := newStubBackend()
	sess, err := mgr.Create(backend, session.Options{})
	if err != nil {
		t.Fatal(err)
	}

	// Produce output before anyone attaches.
	backend.tio.in.Write([]byte("history\x1b[>c-line\x1b[c\x1b[31mred\x1b[0m"))
	time.Sleep(50 * time.Millisecond) // let the read pump buffer it

	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	wsURL := strings.Replace(ts.URL, "http://", "ws://", 1) +
		"/ws?token=" + tokenAuth.Secret()
	conn, _, err := websocket.Dial(ctx, wsURL, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close(websocket.StatusNormalClosure, "")
	conn.SetReadLimit(1 << 20)

	attach, _ := json.Marshal(map[string]any{
		"type": "attach", "sessionId": string(sess.ID()), "channelId": 1,
	})
	conn.Write(ctx, websocket.MessageText, attach)

	var got []byte
	want := []byte("history-line\x1b[31mred\x1b[0m")
	for len(got) < len(want) {
		typ, data, err := conn.Read(ctx)
		if err != nil {
			t.Fatalf("ws read: %v (got %q)", err, got)
		}
		if typ == websocket.MessageBinary && len(data) > 1 && data[0] == 1 {
			got = append(got, data[1:]...)
		}
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("replay = %q, want %q", got, want)
	}

	// The same query in NEW output still reaches the browser for a fresh reply.
	live := []byte("live\x1b[>c")
	backend.tio.in.Write(live)
	got = nil
	for len(got) < len(live) {
		typ, data, err := conn.Read(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if typ == websocket.MessageBinary && len(data) > 1 && data[0] == 1 {
			got = append(got, data[1:]...)
		}
	}
	if !bytes.Equal(got, live) {
		t.Fatalf("live query was filtered: %q", got)
	}
}
