package server

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"

	"sshx/internal/auth"
)

// loginRequest is the POST /api/login body.
type loginRequest struct {
	Password string `json:"password"`
}

// handleLogin verifies the master password and, on success, sets the
// session cookie. Status codes:
//
//	204 + Set-Cookie on success
//	400 on malformed body
//	401 on wrong password
//	409 if the server hasn't been bootstrapped yet (UI should redirect
//	    to /setup)
//
// The frontend interceptor keys off the body's "error" field; we keep
// the body minimal so we don't leak whether the password is wrong vs
// whether setup is required (those are different UX flows).
func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	if s.passAuth == nil {
		writeError(w, http.StatusServiceUnavailable, errors.New("local mode: login disabled"))
		return
	}
	first, err := s.passAuth.FirstRun()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	if first {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusConflict)
		_, _ = io.WriteString(w, `{"error":"setup_required"}`)
		return
	}
	var req loginRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, errors.New("malformed json"))
		return
	}
	if req.Password == "" || len(req.Password) > 1024 {
		writeError(w, http.StatusBadRequest, errors.New("password required"))
		return
	}
	release, allowed := s.loginLimit.acquire(r)
	if !allowed {
		w.Header().Set("Retry-After", "60")
		http.Error(w, "too many login attempts", http.StatusTooManyRequests)
		return
	}
	defer release()
	id, err := s.passAuth.Attempt(req.Password, r.UserAgent())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	if id == "" {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = io.WriteString(w, `{"error":"invalid_password"}`)
		return
	}
	auth.SetSessionCookie(w, id, time.Now().Add(auth.SessionLifetime()), s.passAuth.SecureCookies())
	s.refreshVaultKey()
	w.WriteHeader(http.StatusNoContent)
}

// handleLogout drops the session cookie and removes the row.
func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request) {
	if s.passAuth == nil {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	sid := auth.SessionFromContext(r.Context())
	if sid != "" {
		if err := s.passAuth.Logout(sid); err != nil {
			writeError(w, http.StatusInternalServerError, err)
			return
		}
		s.revokeSockets(sid)
	}
	auth.ClearSessionCookie(w, s.passAuth.SecureCookies())
	w.WriteHeader(http.StatusNoContent)
}

// setupRequest is the POST /api/setup body.
type setupRequest struct {
	Password string `json:"password"`
}

// handleSetup seals the master password into auth_config. Available
// only when auth_config is empty.
func (s *Server) handleSetup(w http.ResponseWriter, r *http.Request) {
	if s.passAuth == nil {
		writeError(w, http.StatusServiceUnavailable, errors.New("local mode: setup disabled"))
		return
	}
	first, err := s.passAuth.FirstRun()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	if !first {
		writeError(w, http.StatusConflict, errors.New("auth already configured"))
		return
	}
	var req setupRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, errors.New("malformed json"))
		return
	}
	if !acceptablePassword(req.Password) {
		writeError(w, http.StatusBadRequest, errors.New("password must be 8-1024 characters"))
		return
	}
	if err := s.passAuth.Bootstrap(req.Password); err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// handleAuthStatus reports whether auth_config is set, so the SPA can
// decide between /login and /setup on first load.
func (s *Server) handleAuthStatus(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if s.passAuth == nil {
		writeJSON(w, http.StatusOK, map[string]any{
			"mode":       "local",
			"configured": true,
		})
		return
	}
	first, err := s.passAuth.FirstRun()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"mode":          "remote",
		"configured":    !first,
		"authenticated": s.passAuth.Authenticated(r),
		"locked":        s.passAuth.Locked(),
	})
}

// acceptablePassword enforces the minimum length. Entropy meters,
// breach checks, etc. are out of scope.
func acceptablePassword(p string) bool {
	p = strings.TrimSpace(p)
	return len(p) >= 8 && len(p) <= 1024
}

// refreshVaultKey pulls the (possibly just-derived) wrap key from the
// authenticator and wires it into the encrypted vault. Called on
// successful login. No-op in local mode.
func (s *Server) refreshVaultKey() {
	if s.passAuth == nil || s.encVault == nil {
		return
	}
	s.encVault.SetKey(s.passAuth.VaultKey())
}
