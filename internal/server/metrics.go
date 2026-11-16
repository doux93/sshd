package server

import (
	"net/http"
	"strconv"
	"sync"

	"github.com/go-chi/chi/v5"

	"sshx/internal/session"
)

// monitorRegistry tracks which session belongs to which node, so the REST
// metrics endpoint can look up the time series by nodeID. The connect
// handler registers the association when a session is created from a node.
type monitorRegistry struct {
	mu        sync.RWMutex
	byNode    map[string]session.ID // nodeID ? sessionID
	bySession map[session.ID]string // sessionID ? nodeID
}

func newMonitorRegistry() *monitorRegistry {
	return &monitorRegistry{
		byNode:    make(map[string]session.ID),
		bySession: make(map[session.ID]string),
	}
}

func (r *monitorRegistry) Link(sessionID session.ID, nodeID string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.byNode[nodeID] = sessionID
	r.bySession[sessionID] = nodeID
}

func (r *monitorRegistry) Unlink(sessionID session.ID) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if nodeID, ok := r.bySession[sessionID]; ok {
		delete(r.bySession, sessionID)
		if r.byNode[nodeID] == sessionID {
			delete(r.byNode, nodeID)
		}
	}
}

func (r *monitorRegistry) SessionByNode(nodeID string) (session.ID, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	sid, ok := r.byNode[nodeID]
	return sid, ok
}

// NodeBySession returns the node a session was opened from, or "" for
// quick-connect sessions that have no stored node.
func (r *monitorRegistry) NodeBySession(sid session.ID) string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.bySession[sid]
}

// handleGetMetrics returns the recent metric samples for a node.
// ?limit=N controls how many (default 720, max 720).
func (s *Server) handleGetMetrics(w http.ResponseWriter, r *http.Request) {
	nodeID := chi.URLParam(r, "nodeId")
	limit := 720
	if l := r.URL.Query().Get("limit"); l != "" {
		if n, err := strconv.Atoi(l); err == nil && n > 0 && n <= 720 {
			limit = n
		}
	}

	if s.monitor == nil {
		writeJSON(w, http.StatusOK, map[string]any{"samples": []any{}})
		return
	}

	sess, ok := s.activeSessionForNode(nodeID)
	if !ok {
		writeJSON(w, http.StatusOK, map[string]any{"samples": []any{}})
		return
	}

	series := s.monitor.Metrics(sess.ID())
	if series == nil {
		writeJSON(w, http.StatusOK, map[string]any{"samples": []any{}})
		return
	}

	samples := series.Recent(limit)
	writeJSON(w, http.StatusOK, map[string]any{
		"samples": samples,
		"count":   len(samples),
	})
}

// monitorRegistry is lazily created on the server to avoid changing the
// constructor for tests that don't use monitoring.
func (s *Server) getMonitorRegistry() *monitorRegistry {
	s.regOnce.Do(func() {
		s.monitorReg = newMonitorRegistry()
	})
	return s.monitorReg
}
