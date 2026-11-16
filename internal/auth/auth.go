// Package auth defines the transport authentication boundary.
//
// Two implementations are planned per the design doc:
//   - Token (desktop/local mode): loopback listener + startup token.
//   - remote mode (M6): username + master password login with sessions.
//
// The server wires exactly one Authenticator; everything behind it is
// mode-agnostic.
package auth

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"net/http"
)

// Authenticator guards the API and WebSocket endpoints.
type Authenticator interface {
	// Middleware rejects unauthenticated requests with 401.
	Middleware(next http.Handler) http.Handler
}

// Token authenticates requests bearing a fixed secret generated at startup.
// The launcher prints/opens a URL carrying the token; the frontend then
// sends it on every API/WS request. Static assets are not guarded (the UI
// shell itself is not sensitive; all data sits behind the API).
type Token struct {
	secret string
}

func NewToken() *Token {
	b := make([]byte, 24)
	rand.Read(b)
	return &Token{secret: hex.EncodeToString(b)}
}

// Secret returns the token for embedding into the startup URL.
func (t *Token) Secret() string { return t.secret }

func (t *Token) valid(candidate string) bool {
	return subtle.ConstantTimeCompare([]byte(candidate), []byte(t.secret)) == 1
}

func (t *Token) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		candidate := r.Header.Get("X-Auth-Token")
		if candidate == "" {
			// WebSocket upgrades and downloads cannot set custom headers.
			candidate = r.URL.Query().Get("token")
		}
		if !t.valid(candidate) {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		next.ServeHTTP(w, r)
	})
}
