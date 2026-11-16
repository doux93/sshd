// Package server is the HTTP/WebSocket transport layer. It wires the UI,
// REST API and terminal WebSocket onto a session.Manager behind an
// auth.Authenticator. It contains no SSH- or storage-specific logic.
package server

import (
	"encoding/json"
	"io/fs"
	"log/slog"
	"net/http"
	"sync"

	"github.com/coder/websocket"
	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"

	"sshx/internal/alert"
	"sshx/internal/auth"
	"sshx/internal/connect"
	"sshx/internal/llm"
	"sshx/internal/mcp"
	"sshx/internal/monitor"
	"sshx/internal/session"
	"sshx/internal/store"
	"sshx/internal/tool"
	"sshx/internal/vault"
)

type Server struct {
	loginLimit loginLimiter
	nodeMu     sync.Mutex
	socketMu   sync.Mutex
	sockets    map[string]map[*websocket.Conn]struct{}
	sessions   *session.Manager
	authn      auth.Authenticator
	store      *store.Store
	vault      vault.Vault
	connect    *connect.Service
	monitor    *monitor.Manager
	ui         fs.FS
	log        *slog.Logger

	// passAuth and encVault are non-nil only in remote (M8) mode.
	// nil fields degrade gracefully to local-mode behavior.
	passAuth *auth.Password
	encVault *vault.Encrypted

	// push is non-nil only when remote mode wired a WebPushNotifier.
	push *PushDeps

	// toolReg holds the AI/MCP tool registry wired from main. nil in
	// tests that don't exercise the tool layer.
	toolReg *tool.Registry
	// mcpServer exposes the tool layer over JSON-RPC at /api/mcp. nil
	// when no tool registry was wired.
	mcpServer *mcp.Server
	// llm is the thin provider client used by Ctrl+K command generation.
	llm *llm.Client

	evMu        sync.Mutex
	evNextID    int
	evListeners map[int]session.EventListener

	regOnce    sync.Once
	monitorReg *monitorRegistry

	metricsMu   sync.Mutex
	metricsSubs map[int]func(sessionID string, sample []byte)
	metricsNext int

	alertMu   sync.Mutex
	alertSubs map[int]func(nodeID, level, message string)
	alertNext int
}

// Deps carries the server's collaborators; all are required except UI
// (nil = API-only, useful for tests and frontend dev via the Vite proxy).
type Deps struct {
	Sessions *session.Manager
	Authn    auth.Authenticator
	Store    *store.Store
	Vault    vault.Vault
	Connect  *connect.Service
	Monitor  *monitor.Manager
	UI       fs.FS
	Log      *slog.Logger
	// PassAuth wires the master-password authenticator (remote mode).
	// Optional — nil means local mode.
	PassAuth *auth.Password
	// EncryptedVault wires the AES-GCM vault (remote mode). Optional —
	// nil means local mode (Keyring).
	EncryptedVault *vault.Encrypted

	// Push wires the Web Push notifier (remote mode). Optional.
	Push *PushDeps
	// Tool wires the AI/MCP tool registry (shared by the MCP server,
	// Ctrl+K command generation, and the future built-in agent).
	Tool *tool.Registry
	// LLM wires the provider client for Ctrl+K command generation.
	// Optional; nil disables /api/ai/ctrlk.
	LLM *llm.Client
}

// New assembles the HTTP server.
func New(d Deps) *Server {
	s := &Server{
		sessions:    d.Sessions,
		authn:       d.Authn,
		store:       d.Store,
		vault:       d.Vault,
		connect:     d.Connect,
		monitor:     d.Monitor,
		ui:          d.UI,
		log:         d.Log,
		passAuth:    d.PassAuth,
		encVault:    d.EncryptedVault,
		push:        d.Push,
		toolReg:     d.Tool,
		llm:         d.LLM,
		evListeners: map[int]session.EventListener{},
		metricsSubs: map[int]func(string, []byte){},
		alertSubs:   map[int]func(string, string, string){},
	}
	// Single manager subscription fanned out to per-connection listeners,
	// so WebSocket connections can unsubscribe (Manager.OnEvent is append-only).
	s.sessions.OnEvent(func(ev session.Event) {
		if ev.Type == "closed" {
			if s.monitor != nil {
				s.monitor.StopCollector(ev.SessionID)
			}
			if _, exists := s.sessions.Get(ev.SessionID); !exists {
				s.getMonitorRegistry().Unlink(ev.SessionID)
			}
		}
		s.evMu.Lock()
		ls := make([]session.EventListener, 0, len(s.evListeners))
		for _, l := range s.evListeners {
			ls = append(ls, l)
		}
		s.evMu.Unlock()
		for _, l := range ls {
			l(ev)
		}
	})

	// Wire monitor callbacks to broadcast metrics/alerts to WS clients.
	if s.monitor != nil {
		s.monitor.OnSample = func(sid session.ID, nodeID string, smp monitor.Sample) {
			data, err := json.Marshal(smp)
			if err != nil {
				return
			}
			s.broadcastMetrics(string(sid), data)
		}
		s.monitor.OnAlert = func(nodeID string, a alert.Alert) {
			if s.store != nil {
				if _, err := s.store.AppendAlert(nodeID, a.NodeLabel, string(a.Level), a.Message); err != nil {
					if s.log != nil {
						s.log.Error("save alert history", "err", err)
					}
				}
			}
			s.broadcastAlert(nodeID, string(a.Level), a.Message)
		}
	}
	// Register the server-scoped get_monitor_metrics tool on top of the
	// defaults wired from main, so it can close over the monitor manager.
	if s.toolReg != nil {
		s.registerMonitorTool()
		// Expose the tool layer to external MCP clients over JSON-RPC.
		// The resolve closure routes a nodeId to a live session through
		// the server's own session manager + store + monitor registry.
		s.mcpServer = mcp.New(s.toolReg,
			func(nodeID string, actor tool.Actor) (*tool.CallContext, error) {
				return s.callContext(nodeID, actor)
			}, s.log)
	}
	return s
}

func (s *Server) subscribeEvents(l session.EventListener) (unsubscribe func()) {
	s.evMu.Lock()
	id := s.evNextID
	s.evNextID++
	s.evListeners[id] = l
	s.evMu.Unlock()
	return func() {
		s.evMu.Lock()
		delete(s.evListeners, id)
		s.evMu.Unlock()
	}
}

func (s *Server) subscribeMetrics(l func(sessionID string, sample []byte)) (unsubscribe func()) {
	s.metricsMu.Lock()
	id := s.metricsNext
	s.metricsNext++
	s.metricsSubs[id] = l
	s.metricsMu.Unlock()
	return func() {
		s.metricsMu.Lock()
		delete(s.metricsSubs, id)
		s.metricsMu.Unlock()
	}
}

func (s *Server) broadcastMetrics(sessionID string, sample []byte) {
	s.metricsMu.Lock()
	subs := make([]func(string, []byte), 0, len(s.metricsSubs))
	for _, l := range s.metricsSubs {
		subs = append(subs, l)
	}
	s.metricsMu.Unlock()
	for _, l := range subs {
		l(sessionID, sample)
	}
}

func (s *Server) subscribeAlerts(l func(nodeID, level, message string)) (unsubscribe func()) {
	s.alertMu.Lock()
	id := s.alertNext
	s.alertNext++
	s.alertSubs[id] = l
	s.alertMu.Unlock()
	return func() {
		s.alertMu.Lock()
		delete(s.alertSubs, id)
		s.alertMu.Unlock()
	}
}

func (s *Server) broadcastAlert(nodeID, level, message string) {
	s.alertMu.Lock()
	subs := make([]func(string, string, string), 0, len(s.alertSubs))
	for _, l := range s.alertSubs {
		subs = append(subs, l)
	}
	s.alertMu.Unlock()
	for _, l := range subs {
		l(nodeID, level, message)
	}
}

func (s *Server) Handler() http.Handler {
	r := chi.NewRouter()
	r.Use(middleware.Recoverer)
	r.Use(sameOrigin)

	// Public auth + health endpoints. Registered BEFORE the gated /api
	// block and at distinct paths so chi's Mount doesn't see them as
	// a duplicate. Login + setup must work without a cookie, by
	// definition.
	r.Route("/api/auth", func(r chi.Router) {
		r.Get("/status", s.handleAuthStatus)
		r.Post("/setup", s.handleSetup)
		r.Post("/login", s.handleLogin)
		r.With(s.authn.Middleware).Post("/logout", s.handleLogout)
	})

	s.mountPushRoutes(r)
	r.Get("/api/health", s.handleHealth)

	r.Route("/api", func(r chi.Router) {
		r.Use(s.authn.Middleware)
		r.Get("/sessions", s.handleListSessions)
		r.Post("/sessions", s.handleCreateSession)
		r.Delete("/sessions/{id}", s.handleCloseSession)

		r.Get("/nodes", s.handleListNodes)
		r.Post("/connections/test", s.handleTestConnection)
		r.Get("/alerts", s.handleListAlerts)
		r.Post("/alerts/{id}/ack", s.handleAckAlert)
		r.Post("/nodes", s.handleCreateNode)
		r.Put("/nodes/{id}", s.handleUpdateNode)
		r.Delete("/nodes/{id}", s.handleDeleteNode)
		r.Post("/nodes/{id}/connect", s.handleConnectNode)

		// SFTP (session selected via ?session=<id>)
		r.Get("/files/list", s.handleFileList)
		r.Get("/files/download", s.handleFileDownload)
		r.Post("/files/upload", s.handleFileUpload)
		r.Post("/files/mkdir", s.handleFileMkdir)
		r.Post("/files/delete", s.handleFileDelete)
		r.Post("/files/rename", s.handleFileRename)

		r.Post("/import/sshconfig", s.handleImportSSHConfig)

		// Monitor
		r.Get("/metrics/{nodeId}", s.handleGetMetrics)

		// MCP: expose the tool layer to external MCP clients.
		if s.mcpServer != nil {
			r.Post("/mcp", s.mcpServer.HTTPHandler().ServeHTTP)
		}

		// AI: Ctrl+K command generation + provider config.
		if s.llm != nil {
			r.Get("/ai/config", s.handleGetAIConfig)
			r.Post("/ai/config", s.handleSetAIConfig)
			r.Post("/ai/ctrlk", s.handleCtrlK)
		}
	})

	r.With(s.authn.Middleware).Get("/ws", s.handleWS)

	if s.ui != nil {
		r.NotFound(s.serveUI())
	}
	return r
}

// serveUI serves the embedded frontend with SPA index fallback.
func (s *Server) serveUI() http.HandlerFunc {
	fileServer := http.FileServer(http.FS(s.ui))
	return func(w http.ResponseWriter, r *http.Request) {
		// Default security headers for the SPA shell. No HSTS here —
		// that's only meaningful over HTTPS, and adding it blindly would
		// block access if a misconfigured proxy terminates TLS on a
		// host that can't actually serve HTTPS directly.
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Content-Security-Policy",
			"default-src 'self'; "+
				"script-src 'self'; "+
				"style-src 'self' 'unsafe-inline'; "+
				"img-src 'self' data:; "+
				"connect-src 'self' ws: wss:; "+
				"frame-ancestors 'none'")
		path := r.URL.Path
		isShell := false
		if path == "/" {
			isShell = true
		} else {
			if _, err := fs.Stat(s.ui, path[1:]); err != nil {
				r.URL.Path = "/"
				isShell = true
			}
		}
		if isShell {
			// Always revalidate network navigations; the PWA caches only static assets.
			w.Header().Set("Cache-Control", "no-store")
			s.serveShell(w)
			return
		}
		fileServer.ServeHTTP(w, r)
	}
}

// serveShell serves static HTML; runtime configuration comes from /api/auth/status.
func (s *Server) serveShell(w http.ResponseWriter) {
	data, err := fs.ReadFile(s.ui, "index.html")
	if err != nil {
		http.Error(w, "ui: index not found", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write(data)
}
