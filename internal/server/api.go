package server

import (
	"encoding/json"
	"net/http"

	"github.com/go-chi/chi/v5"

	"sshx/internal/connect"
	"sshx/internal/session"
	"sshx/internal/store"
)

// createSessionRequest is the ad-hoc quick-connect payload (not saved to
// the tree). Saved connections use POST /api/nodes/{id}/connect instead.
type createSessionRequest struct {
	Host             string            `json:"host"`
	Port             int               `json:"port"`
	User             string            `json:"user"`
	Password         string            `json:"password"`
	Label            string            `json:"label"`
	Cols             int               `json:"cols"`
	Rows             int               `json:"rows"`
	Accepts          map[string]string `json:"accepts,omitempty"`
	ShellIntegration bool              `json:"shellIntegration,omitempty"`
	// Monitor opts the quick-connect caller into the same metrics collector
	// as a saved-node session. Without this, quick-connect sessions are
	// invisible to /api/metrics and MonitorBar never renders.
	Monitor *store.MonitorConfig `json:"monitor,omitempty"`
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, err error) {
	writeJSON(w, status, map[string]string{"error": err.Error()})
}

// sessionView is session.Info plus the server-layer facts the UI needs:
// which node the session came from, and whether metrics are actually being
// collected. Without the latter the UI cannot tell "no data yet" from
// "monitoring was never enabled for this connection", and shows a permanently
// empty monitor panel.
type sessionView struct {
	session.Info
	NodeID    string `json:"nodeId,omitempty"`
	Monitored bool   `json:"monitored"`
}

func (s *Server) sessionViews() []sessionView {
	infos := s.sessions.List()
	reg := s.getMonitorRegistry()
	views := make([]sessionView, 0, len(infos))
	for _, info := range infos {
		v := sessionView{Info: info, NodeID: reg.NodeBySession(info.ID)}
		if s.monitor != nil {
			v.Monitored = s.monitor.Metrics(info.ID) != nil
		}
		views = append(views, v)
	}
	return views
}

func (s *Server) handleListSessions(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.sessionViews())
}

func (s *Server) handleCreateSession(w http.ResponseWriter, r *http.Request) {
	var req createSessionRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	if req.Host == "" || req.User == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "host and user are required"})
		return
	}

	client, err := s.connect.DialQuick(r.Context(), connect.QuickSpec{
		Host:     req.Host,
		Port:     req.Port,
		User:     req.User,
		Password: req.Password,
	}, req.Accepts)
	if err != nil {
		writeDialError(w, err)
		return
	}
	sess, err := s.sessions.Create(client, session.Options{
		Label:            req.Label,
		Cols:             req.Cols,
		Rows:             req.Rows,
		ShellIntegration: req.ShellIntegration,
	})
	if err != nil {
		writeError(w, http.StatusBadGateway, err)
		return
	}

	// Opt-in monitoring for quick-connect. Without this, /api/metrics is
	// empty for this session and the UI's MonitorBar stays blank forever.
	if s.monitor != nil && req.Monitor != nil && req.Monitor.Enabled {
		s.monitor.StartCollector(sess, "", req.Monitor, nil)
	}

	writeJSON(w, http.StatusCreated, sess.Info())
}

func (s *Server) handleCloseSession(w http.ResponseWriter, r *http.Request) {
	id := session.ID(chi.URLParam(r, "id"))
	if err := s.sessions.Close(id); err != nil {
		writeError(w, http.StatusNotFound, err)
		return
	}
	s.getMonitorRegistry().Unlink(id)
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}
