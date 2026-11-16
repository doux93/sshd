package push

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"encoding/base64"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"sshx/internal/alert"
	"sshx/internal/store"
)

func TestVAPIDKeypair_Roundtrip(t *testing.T) {
	kp, err := GenerateVAPIDKey()
	if err != nil {
		t.Fatalf("GenerateVAPIDKey: %v", err)
	}
	if len(kp.PrivateScalar) != 32 {
		t.Fatalf("scalar len = %d, want 32", len(kp.PrivateScalar))
	}
	if len(kp.PublicUncompressed) != 65 || kp.PublicUncompressed[0] != 0x04 {
		t.Fatalf("public key malformed: len=%d first=%x", len(kp.PublicUncompressed), kp.PublicUncompressed[0])
	}
	priv, err := DecodeVAPIDPrivate(kp.PrivateScalar)
	if err != nil {
		t.Fatal(err)
	}
	if priv.X.Cmp(priv.PublicKey.X) != 0 {
		t.Fatal("decoded private does not match original public point")
	}
}

func TestVAPIDJWT_Structure(t *testing.T) {
	kp, err := GenerateVAPIDKey()
	if err != nil {
		t.Fatal(err)
	}
	priv, err := DecodeVAPIDPrivate(kp.PrivateScalar)
	if err != nil {
		t.Fatal(err)
	}
	tok, err := SignVAPIDJWT(priv, Subject("mailto:test@example.com"),
		"https://fcm.googleapis.com", time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	parts := strings.Split(tok, ".")
	if len(parts) != 3 {
		t.Fatalf("JWT has %d parts, want 3", len(parts))
	}
	hdr, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(hdr), `"alg":"ES256"`) {
		t.Fatalf("JWT header missing ES256: %s", hdr)
	}

	claims, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		t.Fatal(err)
	}
	// RFC 8292 §2 requires aud (string), sub (string), exp (number).
	// A regression here ships JWTs that real push services reject with
	// 401/403 — caught early so the M8-3 bug never recurs.
	for _, want := range []string{
		`"aud":"https://fcm.googleapis.com"`,
		`"sub":"mailto:test@example.com"`,
		`"exp":`,
	} {
		if !strings.Contains(string(claims), want) {
			t.Errorf("claims missing %s: %s", want, claims)
		}
	}
	// Guard against the original bug: aud was hardcoded to integer 0.
	if strings.Contains(string(claims), `"aud":0`) {
		t.Fatalf("claims contain literal aud=0: %s", claims)
	}
}

// TestVAPIDJWT_RejectsMissingClaims covers the input validation we added
// when rewriting the claims encoder — RFC 8292 requires both aud and sub.
func TestVAPIDJWT_RejectsMissingClaims(t *testing.T) {
	kp, err := GenerateVAPIDKey()
	if err != nil {
		t.Fatal(err)
	}
	priv, err := DecodeVAPIDPrivate(kp.PrivateScalar)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := SignVAPIDJWT(priv, Subject("mailto:t"), "", time.Hour); err == nil {
		t.Fatal("SignVAPIDJWT with empty aud should fail")
	}
	if _, err := SignVAPIDJWT(priv, Subject(""), "https://fcm.googleapis.com", time.Hour); err == nil {
		t.Fatal("SignVAPIDJWT with empty sub should fail")
	}
}

func TestEncryptPayload_Structure(t *testing.T) {
	// Fake browser keypair.
	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	pubBytes := elliptic.Marshal(elliptic.P256(), priv.X, priv.Y)
	p256dh := base64.RawURLEncoding.EncodeToString(pubBytes)
	authBytes := make([]byte, 16)
	io.ReadFull(rand.Reader, authBytes)
	auth := base64.RawURLEncoding.EncodeToString(authBytes)

	ct, err := EncryptPayload([]byte(`{"title":"hi"}`), p256dh, auth)
	if err != nil {
		t.Fatal(err)
	}
	// 16 salt + 4 rs + 1 idlen + 65 pub + 16 tag + at least 1 byte body
	if len(ct) < 16+4+1+65+16+1 {
		t.Fatalf("ciphertext too short: %d", len(ct))
	}
	if ct[20] != 65 {
		t.Fatalf("idlen = %d, want 65", ct[20])
	}
}

// mockSubscriptionStore implements SubscriptionStore for the notifier
// tests. It uses the real store.PushSubscription type so the notifier
// can call methods that exist on the production struct.
type mockSubscriptionStore struct {
	mu      sync.Mutex
	subs    []store.PushSubscription
	revoked []string
	touched []string
}

func (m *mockSubscriptionStore) ListActivePushSubscriptions() ([]store.PushSubscription, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]store.PushSubscription, 0, len(m.subs))
	for _, s := range m.subs {
		if !s.Revoked {
			out = append(out, s)
		}
	}
	return out, nil
}
func (m *mockSubscriptionStore) RevokePushSubscription(id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.revoked = append(m.revoked, id)
	for i := range m.subs {
		if m.subs[i].ID == id {
			m.subs[i].Revoked = true
		}
	}
	return nil
}
func (m *mockSubscriptionStore) TouchPushSubscription(id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.touched = append(m.touched, id)
	return nil
}

func TestWebPushNotifier_LazyKeyGeneration(t *testing.T) {
	st := &mockSubscriptionStore{}
	loadCalls := 0
	load := func() (*ecdsa.PrivateKey, []byte, error) {
		loadCalls++
		return nil, nil, nil // empty every time -> force generation
	}
	saved := false
	save := func(priv, pub []byte) error {
		saved = true
		if len(priv) != 32 || len(pub) != 65 {
			t.Errorf("save sizes: priv=%d pub=%d", len(priv), len(pub))
		}
		return nil
	}
	n := NewWebPushNotifier(st, load, save, nil)
	pk, err := n.PublicKey()
	if err != nil {
		t.Fatal(err)
	}
	if pk == "" {
		t.Fatal("PublicKey returned empty")
	}
	if !saved {
		t.Fatal("expected saveVAPID to be called")
	}
	if loadCalls == 0 {
		t.Fatal("expected loadVAPID to be called")
	}
	// Second call should hit the in-memory cache.
	loadCalls = 0
	_, _ = n.PublicKey()
	if loadCalls != 0 {
		t.Fatalf("second PublicKey should not call load; calls=%d", loadCalls)
	}
}

func TestWebPushNotifier_RevokesDeadSubscription(t *testing.T) {
	st := &mockSubscriptionStore{}
	// pre-seed an existing subscription that points at our test server.
	priv, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	browserPriv, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	pubBytes := elliptic.Marshal(elliptic.P256(), browserPriv.X, browserPriv.Y)
	p256dh := base64.RawURLEncoding.EncodeToString(pubBytes)
	authBytes := make([]byte, 16)
	io.ReadFull(rand.Reader, authBytes)
	auth := base64.RawURLEncoding.EncodeToString(authBytes)

	gone := false
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if gone {
			w.WriteHeader(http.StatusGone)
			return
		}
		w.WriteHeader(http.StatusCreated)
	}))
	defer ts.Close()

	st.subs = append(st.subs, store.PushSubscription{
		ID: "abc", Endpoint: ts.URL, P256DH: p256dh, Auth: auth,
	})

	n := NewWebPushNotifier(st,
		func() (*ecdsa.PrivateKey, []byte, error) {
			return priv, elliptic.Marshal(elliptic.P256(), priv.X, priv.Y), nil
		},
		nil, nil)

	err := n.Notify(nil, alert.Alert{NodeID: "n1", NodeLabel: "host-1", Level: alert.LevelWarning})
	if err != nil {
		t.Fatalf("Notify: %v", err)
	}

	// Now flip the service to 410 and notify again.
	gone = true
	st.mu.Lock()
	st.revoked = nil
	st.mu.Unlock()
	err = n.Notify(nil, alert.Alert{NodeID: "n1", Level: alert.LevelWarning})
	if err == nil {
		t.Fatal("expected error from dead subscription")
	}
	st.mu.Lock()
	defer st.mu.Unlock()
	if len(st.revoked) != 1 || st.revoked[0] != "abc" {
		t.Fatalf("expected abc to be revoked, got %v", st.revoked)
	}
}

func TestAlertNotificationOpensHostHistory(t *testing.T) {
	n := alertToNotification(alert.Alert{NodeID: "host&special", NodeLabel: "Production"})
	u, err := url.Parse(n.URL)
	if err != nil || u.Query().Get("node") != "host&special" || u.Query().Get("panel") != "alerts" || u.Query().Get("session") != "" {
		t.Fatalf("incorrect alert route: %s %v", n.URL, err)
	}
}
