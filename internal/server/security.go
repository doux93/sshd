package server

import (
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/coder/websocket"
)

// Browsers must use the same origin for mutations. Non-browser clients may
// omit Origin but still require the normal token/session authentication.
func sameOrigin(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead && r.Method != http.MethodOptions {
			if origin := r.Header.Get("Origin"); origin != "" {
				u, err := url.Parse(origin)
				if err != nil || (u.Scheme != "http" && u.Scheme != "https") || !strings.EqualFold(u.Host, r.Host) || u.User != nil {
					http.Error(w, "untrusted origin", http.StatusForbidden)
					return
				}
			}
		}
		if strings.HasPrefix(r.URL.Path, "/api/") && r.URL.Path != "/api/files/upload" {
			limit := int64(1 << 20)
			if strings.HasPrefix(r.URL.Path, "/api/auth/") {
				limit = 8192
			}
			r.Body = http.MaxBytesReader(w, r.Body, limit)
		}
		next.ServeHTTP(w, r)
	})
}

type loginWindow struct {
	since    time.Time
	attempts int
}
type loginLimiter struct {
	mu      sync.Mutex
	windows map[string]loginWindow
	active  int
}

// Bound both CPU/memory spent in Argon2 and the size of the rate-limit map.
// Forwarded IP headers are deliberately ignored unless a trusted-proxy model exists.
func (l *loginLimiter) acquire(r *http.Request) (func(), bool) {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	now := time.Now()
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.windows == nil {
		l.windows = make(map[string]loginWindow)
	}
	for k, v := range l.windows {
		if now.Sub(v.since) >= time.Minute {
			delete(l.windows, k)
		}
	}
	v, exists := l.windows[host]
	if l.active >= 2 || v.attempts >= 10 || (!exists && len(l.windows) >= 1024) {
		return nil, false
	}
	if !exists {
		v.since = now
	}
	v.attempts++
	l.windows[host] = v
	l.active++
	return func() { l.mu.Lock(); l.active--; l.mu.Unlock() }, true
}

func (s *Server) trackSocket(sid string, conn *websocket.Conn) func() {
	s.socketMu.Lock()
	if s.sockets == nil {
		s.sockets = make(map[string]map[*websocket.Conn]struct{})
	}
	if s.sockets[sid] == nil {
		s.sockets[sid] = make(map[*websocket.Conn]struct{})
	}
	s.sockets[sid][conn] = struct{}{}
	s.socketMu.Unlock()
	return func() {
		s.socketMu.Lock()
		defer s.socketMu.Unlock()
		delete(s.sockets[sid], conn)
		if len(s.sockets[sid]) == 0 {
			delete(s.sockets, sid)
		}
	}
}
func (s *Server) revokeSockets(sid string) {
	s.socketMu.Lock()
	conns := make([]*websocket.Conn, 0, len(s.sockets[sid]))
	for conn := range s.sockets[sid] {
		conns = append(conns, conn)
	}
	delete(s.sockets, sid)
	s.socketMu.Unlock()
	for _, conn := range conns {
		conn.CloseNow()
	}
}
