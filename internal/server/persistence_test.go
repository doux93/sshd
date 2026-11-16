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

// wsTestClient wraps the raw websocket with the helpers these tests need.
type wsTestClient struct {
	t    *testing.T
	ctx  context.Context
	conn *websocket.Conn
}

func dialWS(t *testing.T, ctx context.Context, tsURL, token string) *wsTestClient {
	t.Helper()
	url := strings.Replace(tsURL, "http://", "ws://", 1) + "/ws?token=" + token
	conn, _, err := websocket.Dial(ctx, url, nil)
	if err != nil {
		t.Fatalf("ws dial: %v", err)
	}
	conn.SetReadLimit(1 << 20)
	return &wsTestClient{t: t, ctx: ctx, conn: conn}
}

func (c *wsTestClient) attach(sessionID string, channelID int) {
	c.t.Helper()
	msg, _ := json.Marshal(map[string]any{
		"type": "attach", "sessionId": sessionID, "channelId": channelID,
	})
	if err := c.conn.Write(c.ctx, websocket.MessageText, msg); err != nil {
		c.t.Fatalf("attach write: %v", err)
	}
}

func (c *wsTestClient) sendInput(channelID int, data string) {
	c.t.Helper()
	frame := append([]byte{byte(channelID)}, data...)
	if err := c.conn.Write(c.ctx, websocket.MessageBinary, frame); err != nil {
		c.t.Fatalf("input write: %v", err)
	}
}

// readOutput accumulates binary frames on channelID until want is covered.
func (c *wsTestClient) readOutput(channelID int, want []byte) []byte {
	c.t.Helper()
	var got []byte
	for len(got) < len(want) {
		typ, data, err := c.conn.Read(c.ctx)
		if err != nil {
			c.t.Fatalf("ws read: %v (got %q, want %q)", err, got, want)
		}
		if typ == websocket.MessageBinary && len(data) > 1 && data[0] == byte(channelID) {
			got = append(got, data[1:]...)
		}
	}
	return got
}

func (c *wsTestClient) close() { c.conn.Close(websocket.StatusNormalClosure, "") }

func newTestServer(t *testing.T) (*httptest.Server, *session.Manager, string) {
	t.Helper()
	mgr := session.NewManager()
	tokenAuth := auth.NewToken()
	srv := New(Deps{
		Sessions: mgr,
		Authn:    tokenAuth,
		Log:      slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)
	return ts, mgr, tokenAuth.Secret()
}

// TestSessionSurvivesDisconnectAndReattaches is the M3 acceptance test:
// a client disconnects entirely (browser closed), the session keeps
// running, and a fresh connection reattaches and sees the full history.
func TestSessionSurvivesDisconnectAndReattaches(t *testing.T) {
	ts, mgr, token := newTestServer(t)
	backend := newStubBackend()
	sess, err := mgr.Create(backend, session.Options{})
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	// First client: attach and produce some scrollback.
	c1 := dialWS(t, ctx, ts.URL, token)
	c1.attach(string(sess.ID()), 3)
	c1.sendInput(3, "first")
	if got := c1.readOutput(3, []byte("echo:first")); !bytes.Equal(got, []byte("echo:first")) {
		t.Fatalf("first output = %q", got)
	}

	// Browser closes.
	c1.close()

	// Session must still be alive and detached.
	deadline := time.Now().Add(2 * time.Second)
	for {
		info := sess.Info()
		if info.Attached == 0 {
			if info.State != session.StateActive {
				t.Fatalf("session state after disconnect = %q, want active", info.State)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("session still reports attached clients after disconnect")
		}
		time.Sleep(10 * time.Millisecond)
	}

	// Output produced while nobody is watching must be buffered.
	backend.tio.in.Write([]byte("|detached-output"))
	time.Sleep(50 * time.Millisecond)

	// New client (page reload) reattaches and replays the whole history.
	c2 := dialWS(t, ctx, ts.URL, token)
	defer c2.close()
	c2.attach(string(sess.ID()), 9)
	want := []byte("echo:first|detached-output")
	if got := c2.readOutput(9, want); !bytes.Equal(got, want) {
		t.Fatalf("replay = %q, want %q", got, want)
	}

	// The reattached client can still drive the session.
	c2.sendInput(9, "second")
	if got := c2.readOutput(9, []byte("echo:second")); !bytes.Contains(got, []byte("echo:second")) {
		t.Fatalf("post-reattach output = %q", got)
	}
}

// TestMultipleClientsShareOneSession covers the phone+PC scenario: two
// live connections see the same output and both can type.
func TestMultipleClientsShareOneSession(t *testing.T) {
	ts, mgr, token := newTestServer(t)
	backend := newStubBackend()
	sess, err := mgr.Create(backend, session.Options{})
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	c1 := dialWS(t, ctx, ts.URL, token)
	defer c1.close()
	c1.attach(string(sess.ID()), 1)
	c2 := dialWS(t, ctx, ts.URL, token)
	defer c2.close()
	c2.attach(string(sess.ID()), 2)

	// Wait until the server sees both attachments.
	deadline := time.Now().Add(2 * time.Second)
	for sess.Info().Attached < 2 {
		if time.Now().After(deadline) {
			t.Fatalf("only %d clients attached", sess.Info().Attached)
		}
		time.Sleep(10 * time.Millisecond)
	}

	// Input from client 1 is broadcast to both.
	c1.sendInput(1, "shared")
	want := []byte("echo:shared")
	if got := c1.readOutput(1, want); !bytes.Contains(got, want) {
		t.Fatalf("client1 output = %q", got)
	}
	if got := c2.readOutput(2, want); !bytes.Contains(got, want) {
		t.Fatalf("client2 output = %q", got)
	}

	// And client 2 can type into the same terminal.
	c2.sendInput(2, "from2")
	if got := c1.readOutput(1, []byte("echo:from2")); !bytes.Contains(got, []byte("echo:from2")) {
		t.Fatalf("client1 did not see client2's input: %q", got)
	}
}
