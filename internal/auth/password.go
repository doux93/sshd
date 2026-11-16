// Password authenticator: master-password login in M8 long-running mode.
//
// Key derivation (argon2id, per docs/DESIGN.md + plan §1):
//   - time = 1, memory = 64 MiB, threads = 2, keyLen = 32, saltLen = 16.
//   - Two domain-separated keys from one master password:
//   - verifier (domain "sshx-auth-v1"): stored in DB, compared on login
//   - wrap key (domain "sshx-vault-v1"): held in memory after login,
//     never persisted
//
// Sessions are opaque server-side rows in the sessions table (revocable,
// auditable). Cookies carry only the id. Same id can be presented via
// `X-Auth-Token` header or `?token=` query string for CLI / WS clients
// that can't set cookies.
package auth

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"errors"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"golang.org/x/crypto/argon2"

	"sshx/internal/store"
)

// argon2id parameters (must match the values saved in auth_config).
const (
	argonTime    = 1
	argonMemory  = 64 * 1024 // KiB
	argonThreads = 2
	keyLen       = 32
	saltLen      = 16

	domainVerifier = "sshx-auth-v1"
	domainVaultKey = "sshx-vault-v1"

	cookieName    = "sshx_session"
	tokenHeader   = "X-Auth-Token"
	queryTokenKey = "token"

	// Session lifetime: 30 days, sliding-renewed at half-life.
	sessionLifetime = 30 * 24 * time.Hour

	// Cookie Max-Age in seconds.
	cookieMaxAge = int(sessionLifetime / time.Second)
)

// SessionCookieName is exposed so the HTTP layer can clear the cookie
// at logout time without hard-coding the name in two places.
func SessionCookieName() string { return cookieName }

// SessionLifetime returns the configured session TTL.
func SessionLifetime() time.Duration { return sessionLifetime }

// Logger is the minimum logging surface this package needs.
type Logger interface {
	Info(msg string, args ...any)
	Warn(msg string, args ...any)
	Error(msg string, args ...any)
}

// Password implements auth.Authenticator against a master-password DB.
type Password struct {
	st  *store.Store
	log Logger

	mu      sync.Mutex
	wrapKey []byte // 32 bytes after first successful login; nil = locked

	// secure indicates the connection is HTTPS — adds the Secure flag
	// to the session cookie. Driven by server config, not auto-detected.
	secureCookies bool
}

// NewPassword constructs a Password authenticator. The vault is locked
// until the first successful login derives the wrap key.
func NewPassword(st *store.Store, secureCookies bool, log Logger) *Password {
	return &Password{st: st, secureCookies: secureCookies, log: log}
}

// VaultKey returns the in-process wrap key (32 bytes) or nil if the
// vault is locked. The connect layer / vault.Encrypted use this to
// unseal secret_blobs.
func (p *Password) VaultKey() []byte {
	p.mu.Lock()
	defer p.mu.Unlock()
	if len(p.wrapKey) != 32 {
		return nil
	}
	return append([]byte(nil), p.wrapKey...)
}

// Locked reports whether the vault has an unsealed wrap key.
func (p *Password) Locked() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return len(p.wrapKey) != 32
}

// FirstRun reports whether auth_config has been bootstrapped yet.
func (p *Password) FirstRun() (bool, error) {
	_, err := p.st.LoadAuthConfig()
	if errors.Is(err, store.ErrAuthNotConfigured) {
		return true, nil
	}
	if err != nil {
		return false, err
	}
	return false, nil
}

// Bootstrap seals a master password into auth_config. One-shot — fails
// if a row already exists. A caller using a bootstrap password file should
// remove that file only after this method succeeds.
func (p *Password) Bootstrap(password string) error {
	if len(strings.TrimSpace(password)) < 8 || len(password) > 1024 {
		return errors.New("password must be 8-1024 bytes")
	}
	if _, err := p.st.LoadAuthConfig(); err == nil {
		return errors.New("auth: already configured")
	} else if !errors.Is(err, store.ErrAuthNotConfigured) {
		return err
	}
	cfg, err := seal(password)
	if err != nil {
		return err
	}
	return p.st.SaveAuthConfig(cfg)
}

// seal derives verifier + vault salts and computes the verifier.
func seal(password string) (*store.AuthConfig, error) {
	verifierSalt := make([]byte, saltLen)
	vaultSalt := make([]byte, saltLen)
	if _, err := io.ReadFull(rand.Reader, verifierSalt); err != nil {
		return nil, err
	}
	if _, err := io.ReadFull(rand.Reader, vaultSalt); err != nil {
		return nil, err
	}
	verifier := deriveKey(password, verifierSalt, domainVerifier)
	return &store.AuthConfig{
		Verifier:       verifier,
		VerifierSalt:   verifierSalt,
		VaultSalt:      vaultSalt,
		ArgonTime:      argonTime,
		ArgonMemoryKiB: argonMemory,
		ArgonThreads:   argonThreads,
	}, nil
}

// deriveKey runs argon2id with the documented parameters. Domain
// separation is achieved by prefixing the password with a fixed string
// before passing it to argon2id; this prevents the verifier and wrap
// key from being equal even with identical salts.
func deriveKey(password string, salt []byte, domain string) []byte {
	return argon2.IDKey(
		[]byte(domain+"\x00"+password),
		salt,
		argonTime,
		argonMemory,
		argonThreads,
		keyLen,
	)
}

// deriveKeyWithParams mirrors deriveKey but accepts the persisted
// argon2 parameters so cost changes can be picked up over time.
func deriveKeyWithParams(password string, salt []byte, domain string,
	t, mem uint32, threads uint8) []byte {
	return argon2.IDKey(
		[]byte(domain+"\x00"+password),
		salt,
		t, mem, threads, keyLen,
	)
}

// Attempt verifies the master password. On success it returns a fresh
// session id and installs the wrap key in process memory. On wrong
// password it returns ("", nil) — caller distinguishes by checking
// whether the returned id is empty.
func (p *Password) Attempt(password, userAgent string) (sessionID string, err error) {
	cfg, err := p.st.LoadAuthConfig()
	if err != nil {
		return "", err
	}
	candidate := deriveKeyWithParams(password, cfg.VerifierSalt, domainVerifier,
		cfg.ArgonTime, cfg.ArgonMemoryKiB, cfg.ArgonThreads)
	if subtle.ConstantTimeCompare(candidate, cfg.Verifier) != 1 {
		return "", nil
	}
	// Password matches — derive wrap key and install it.
	wrap := deriveKeyWithParams(password, cfg.VaultSalt, domainVaultKey,
		cfg.ArgonTime, cfg.ArgonMemoryKiB, cfg.ArgonThreads)
	p.mu.Lock()
	p.wrapKey = append([]byte(nil), wrap...)
	p.mu.Unlock()

	id, err := store.NewSessionID()
	if err != nil {
		return "", err
	}
	now := time.Now()
	if err := p.st.CreateSession(&store.Session{
		ID:         id,
		CreatedAt:  now,
		ExpiresAt:  now.Add(sessionLifetime),
		LastSeenAt: now,
		UserAgent:  userAgent,
	}); err != nil {
		return "", err
	}
	if p.log != nil {
		p.log.Info("auth login", "session_id_prefix", id[:8], "ua", userAgent)
	}
	return id, nil
}

// Logout deletes the session row. Idempotent.
func (p *Password) Logout(sessionID string) error {
	return p.st.DeleteSession(sessionID)
}

// SetSecureCookies toggles the Secure flag on outgoing cookies. Called
// from main once TLS is configured.
func (p *Password) SetSecureCookies(b bool) { p.secureCookies = b }

// SecureCookies reports the current Secure-flag policy.
func (p *Password) SecureCookies() bool { return p.secureCookies }

// Middleware checks for a valid session via cookie, header, or query
// param. On success it attaches the session id to the request context.
func (p *Password) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sid, from := extractSessionID(r)
		if sid == "" {
			writeLoginRequired(w)
			return
		}
		sess, err := p.st.TouchSession(sid)
		if err != nil {
			writeLoginRequired(w)
			return
		}
		// Renew the cookie if the server pushed the expiry forward.
		if from == fromCookie {
			SetSessionCookie(w, sess.ID, sess.ExpiresAt, p.secureCookies)
		}
		next.ServeHTTP(w, r.WithContext(WithSession(r.Context(), sess.ID)))
	})
}

// writeLoginRequired emits the canonical 401 with the sentinel error
// the frontend 401 interceptor keys off.
func writeLoginRequired(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusUnauthorized)
	_, _ = io.WriteString(w, `{"error":"login_required"}`)
}

type ctxKey struct{ name string }

var sessionCtxKey = &ctxKey{"sshx-session-id"}

// WithSession returns a context carrying the session id.
func WithSession(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, sessionCtxKey, id)
}

// SessionFromContext returns the session id stored by Middleware, or "".
func SessionFromContext(ctx context.Context) string {
	if v, ok := ctx.Value(sessionCtxKey).(string); ok {
		return v
	}
	return ""
}

// SetSessionCookie writes the standard session cookie.
func SetSessionCookie(w http.ResponseWriter, id string, exp time.Time, secure bool) {
	cookie := &http.Cookie{
		Name:     cookieName,
		Value:    id,
		Path:     "/",
		Expires:  exp,
		MaxAge:   cookieMaxAge,
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Secure:   secure,
	}
	http.SetCookie(w, cookie)
}

// ClearSessionCookie instructs the browser to drop the session cookie.
func ClearSessionCookie(w http.ResponseWriter, secure bool) {
	cookie := &http.Cookie{
		Name:     cookieName,
		Value:    "",
		Path:     "/",
		Expires:  time.Unix(0, 0),
		MaxAge:   -1,
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Secure:   secure,
	}
	http.SetCookie(w, cookie)
}

type source int

const (
	fromUnknown source = iota
	fromCookie
	fromHeader
	fromQuery
)

// extractSessionID reads the session id from cookie / header / query,
// in that priority order. Returns the id and where it came from so
// the middleware knows whether to refresh the cookie on renewal.
func extractSessionID(r *http.Request) (string, source) {
	if c, err := r.Cookie(cookieName); err == nil && c.Value != "" {
		return c.Value, fromCookie
	}
	if h := r.Header.Get(tokenHeader); h != "" {
		return h, fromHeader
	}
	if q := r.URL.Query().Get(queryTokenKey); q != "" {
		return q, fromQuery
	}
	return "", fromUnknown
}

// CookiePolicy returns the cookie attributes the server uses, for tests
// and for documentation.
func CookiePolicy(secure bool) string {
	parts := []string{"HttpOnly", "SameSite=Lax", "Path=/"}
	if secure {
		parts = append(parts, "Secure")
	}
	return strings.Join(parts, "; ")
}

// Authenticated checks a credential without extending its lifetime.
func (p *Password) Authenticated(r *http.Request) bool {
	sid, _ := extractSessionID(r)
	if sid == "" {
		return false
	}
	_, err := p.st.GetSession(sid)
	return err == nil
}
