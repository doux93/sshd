package server

import (
	"context"
	"github.com/coder/websocket"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sshx/internal/auth"
	"sshx/internal/session"
	"sshx/internal/store"
	"strings"
	"testing"
	"time"
)

func reviewPasswordServer(t *testing.T) (*Server, string) {
	st, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	sid := "review-auth-session"
	now := time.Now()
	if err := st.CreateSession(&store.Session{ID: sid, CreatedAt: now, ExpiresAt: now.Add(time.Hour), LastSeenAt: now}); err != nil {
		t.Fatal(err)
	}
	p := auth.NewPassword(st, false, nil)
	return New(Deps{Sessions: session.NewManager(), Authn: p, PassAuth: p, Store: st}), sid
}
func TestReview_HTTPLogoutMustRevokeSession(t *testing.T) {
	s, sid := reviewPasswordServer(t)
	h := s.Handler()
	r := httptest.NewRequest("POST", "/api/auth/logout", nil)
	r.AddCookie(&http.Cookie{Name: auth.SessionCookieName(), Value: sid})
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 204 {
		t.Fatalf("logout status=%d", w.Code)
	}
	r = httptest.NewRequest("GET", "/api/sessions", nil)
	r.Header.Set("X-Auth-Token", sid)
	w = httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 401 {
		t.Errorf("credential still accepted after logout: status=%d", w.Code)
	}
}
func TestReview_WSRejectsUntrustedOrigin(t *testing.T) {
	s, sid := reviewPasswordServer(t)
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	headers := http.Header{"Origin": []string{"https://untrusted.example"}, "Cookie": []string{auth.SessionCookieName() + "=" + sid}}
	conn, _, err := websocket.Dial(ctx, strings.Replace(ts.URL, "http://", "ws://", 1)+"/ws", &websocket.DialOptions{HTTPHeader: headers})
	if err == nil {
		conn.CloseNow()
		t.Error("server accepted untrusted Origin with session cookie")
	}
}

func TestLogoutClosesEstablishedWebSocket(t *testing.T) {
	s, sid := reviewPasswordServer(t)
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	headers := http.Header{"Cookie": []string{auth.SessionCookieName() + "=" + sid}}
	conn, _, err := websocket.Dial(ctx, strings.Replace(ts.URL, "http://", "ws://", 1)+"/ws", &websocket.DialOptions{HTTPHeader: headers})
	if err != nil {
		t.Fatal(err)
	}
	defer conn.CloseNow()
	req, _ := http.NewRequest("POST", ts.URL+"/api/auth/logout", nil)
	req.Header = headers
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 204 {
		t.Fatal(resp.StatusCode)
	}
	if _, _, err = conn.Read(ctx); err == nil {
		t.Fatal("socket remained usable after logout")
	}
	if ctx.Err() != nil {
		t.Fatal("logout did not close websocket promptly")
	}
}
func TestMutationRejectsForeignOrigin(t *testing.T) {
	s, sid := reviewPasswordServer(t)
	r := httptest.NewRequest("POST", "http://console.example/api/auth/logout", nil)
	r.Header.Set("Origin", "https://other.example")
	r.Header.Set("X-Auth-Token", sid)
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, r)
	if w.Code != 403 {
		t.Fatal(w.Code)
	}
}
func TestLoginConcurrencyLimit(t *testing.T) {
	var limit loginLimiter
	r := httptest.NewRequest("POST", "/api/auth/login", nil)
	a, ok := limit.acquire(r)
	if !ok {
		t.Fatal("first rejected")
	}
	defer a()
	b, ok := limit.acquire(r)
	if !ok {
		t.Fatal("second rejected")
	}
	defer b()
	if _, ok := limit.acquire(r); ok {
		t.Fatal("unbounded password hashing concurrency")
	}
}

func TestReusingChannelDoesNotDetachReplacement(t *testing.T) {
	ts, mgr, token := newTestServer(t)
	defer mgr.CloseAll()
	backend := newStubBackend()
	sess, err := mgr.Create(backend, session.Options{})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	c := dialWS(t, ctx, ts.URL, token)
	defer c.close()
	c.attach(string(sess.ID()), 1)
	c.sendInput(1, "before")
	c.readOutput(1, []byte("echo:before"))
	c.attach(string(sess.ID()), 1)
	c.readOutput(1, []byte("echo:before"))
	c.sendInput(1, "after")
	if got := c.readOutput(1, []byte("echo:after")); string(got) != "echo:after" {
		t.Fatal(string(got))
	}
}
