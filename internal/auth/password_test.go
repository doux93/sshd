package auth

import (
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"sshx/internal/store"
)

func newTestPassword(t *testing.T) *Password {
	t.Helper()
	dir := t.TempDir()
	st, err := store.Open(filepath.Join(dir, "a.db"))
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { st.Close() })
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	return NewPassword(st, false, log)
}

func TestPassword_BootstrapIsOneShot(t *testing.T) {
	p := newTestPassword(t)
	if err := p.Bootstrap("hunter2-review"); err != nil {
		t.Fatalf("Bootstrap: %v", err)
	}
	if err := p.Bootstrap("hunter2-review"); err == nil {
		t.Fatal("second Bootstrap should fail")
	}
}

func TestPassword_FirstRunDetection(t *testing.T) {
	p := newTestPassword(t)
	first, err := p.FirstRun()
	if err != nil || !first {
		t.Fatalf("FirstRun before bootstrap: %v %v", first, err)
	}
	if err := p.Bootstrap("hunter2-review"); err != nil {
		t.Fatalf("Bootstrap: %v", err)
	}
	first, err = p.FirstRun()
	if err != nil || first {
		t.Fatalf("FirstRun after bootstrap: %v %v", first, err)
	}
}

func TestPassword_AttemptAndUnseal(t *testing.T) {
	p := newTestPassword(t)
	if err := p.Bootstrap("hunter2-review"); err != nil {
		t.Fatalf("Bootstrap: %v", err)
	}
	if !p.Locked() {
		t.Fatal("vault should be locked before login")
	}
	id, err := p.Attempt("hunter2-review", "test-ua")
	if err != nil {
		t.Fatalf("Attempt: %v", err)
	}
	if id == "" {
		t.Fatal("Attempt returned empty session id")
	}
	if p.Locked() {
		t.Fatal("vault should be unlocked after login")
	}
	if k := p.VaultKey(); len(k) != 32 {
		t.Fatalf("VaultKey length = %d, want 32", len(k))
	}
}

func TestPassword_AttemptWrongPassword(t *testing.T) {
	p := newTestPassword(t)
	if err := p.Bootstrap("hunter2-review"); err != nil {
		t.Fatalf("Bootstrap: %v", err)
	}
	id, err := p.Attempt("WRONG", "test-ua")
	if err != nil {
		t.Fatalf("Attempt wrong: %v", err)
	}
	if id != "" {
		t.Fatalf("Attempt wrong should return empty id, got %q", id)
	}
	if !p.Locked() {
		t.Fatal("vault should remain locked after wrong password")
	}
}

func TestPassword_MiddlewareRejectsMissing(t *testing.T) {
	p := newTestPassword(t)
	if err := p.Bootstrap("hunter2-review"); err != nil {
		t.Fatalf("Bootstrap: %v", err)
	}
	h := p.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
	}))
	req := httptest.NewRequest("GET", "/x", nil)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != 401 {
		t.Fatalf("missing cookie: status = %d", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), "login_required") {
		t.Fatalf("missing cookie: body = %q", rr.Body.String())
	}
}

func TestPassword_MiddlewareAcceptsCookie(t *testing.T) {
	p := newTestPassword(t)
	if err := p.Bootstrap("hunter2-review"); err != nil {
		t.Fatalf("Bootstrap: %v", err)
	}
	id, err := p.Attempt("hunter2-review", "test-ua")
	if err != nil {
		t.Fatalf("Attempt: %v", err)
	}
	called := false
	h := p.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		if SessionFromContext(r.Context()) != id {
			t.Fatalf("session id in ctx = %q, want %q", SessionFromContext(r.Context()), id)
		}
		w.WriteHeader(200)
	}))
	req := httptest.NewRequest("GET", "/x", nil)
	req.AddCookie(&http.Cookie{Name: SessionCookieName(), Value: id})
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if !called || rr.Code != 200 {
		t.Fatalf("called=%v code=%d", called, rr.Code)
	}
}

func TestPassword_Logout(t *testing.T) {
	p := newTestPassword(t)
	if err := p.Bootstrap("hunter2-review"); err != nil {
		t.Fatalf("Bootstrap: %v", err)
	}
	id, err := p.Attempt("hunter2-review", "test-ua")
	if err != nil {
		t.Fatalf("Attempt: %v", err)
	}
	if err := p.Logout(id); err != nil {
		t.Fatalf("Logout: %v", err)
	}
	// Middleware should now reject.
	h := p.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("handler called after logout")
	}))
	req := httptest.NewRequest("GET", "/x", nil)
	req.AddCookie(&http.Cookie{Name: SessionCookieName(), Value: id})
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != 401 {
		t.Fatalf("after logout: status = %d", rr.Code)
	}
}

func TestPassword_TouchSessionSliding(t *testing.T) {
	p := newTestPassword(t)
	if err := p.Bootstrap("hunter2-review"); err != nil {
		t.Fatalf("Bootstrap: %v", err)
	}
	id, err := p.Attempt("hunter2-review", "test-ua")
	if err != nil || id == "" {
		t.Fatalf("Attempt: %v id=%q", err, id)
	}
	// Force last_seen_at back so the sliding-renewal branch fires.
	_, err = p.st.LoadAuthConfig() // sanity
	if err != nil {
		t.Fatal(err)
	}
	// Manually push last_seen to 2 days ago and expire_at to 10 days.
	if _, err := p.st.DB().Exec(
		`UPDATE sessions SET last_seen_at = ?, expires_at = ? WHERE id = ?`,
		time.Now().Add(-2*24*time.Hour).UnixMilli(),
		time.Now().Add(10*24*time.Hour).UnixMilli(),
		id,
	); err != nil {
		t.Fatal(err)
	}
	if _, err := p.st.TouchSession(id); err != nil {
		t.Fatalf("TouchSession: %v", err)
	}
	sess, err := p.st.GetSession(id)
	if err != nil {
		t.Fatalf("GetSession: %v", err)
	}
	// Expires_at should have been pushed back to ~30 days.
	delta := time.Until(sess.ExpiresAt)
	if delta < 25*24*time.Hour {
		t.Fatalf("expires_at not bumped: %v remaining", delta)
	}
}

func TestPassword_ArgonDomainSeparation(t *testing.T) {
	// Verifier and wrap key must not collide even with identical salts.
	salt := make([]byte, saltLen)
	for i := range salt {
		salt[i] = 0xAA
	}
	v1 := deriveKey("hunter2-review", salt, domainVerifier)
	v2 := deriveKey("hunter2-review", salt, domainVaultKey)
	if string(v1) == string(v2) {
		t.Fatal("verifier and vault key collide")
	}
}

func TestPassword_AttemptBeforeBootstrap(t *testing.T) {
	p := newTestPassword(t)
	_, err := p.Attempt("anything", "test-ua")
	if !errors.Is(err, store.ErrAuthNotConfigured) {
		t.Fatalf("Attempt before bootstrap: err = %v, want ErrAuthNotConfigured", err)
	}
}
