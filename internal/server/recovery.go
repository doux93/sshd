package server

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/go-chi/chi/v5"
	"net/http"
	"sshx/internal/alert"
	"sshx/internal/session"
	"sshx/internal/sshclient"
	"sshx/internal/store"
)

// Bind a recovery identity to the endpoint and entire jump route. Passwords,
// keys, display names and monitoring settings may change without losing it.
// Callers hold nodeMu across resolving/dialing so edits cannot change the route.
func (s *Server) recoveryRoute(node *store.Node) (string, error) {
	type endpoint struct {
		ID, Host, User, Proxy, Jump string
		Port                        int
	}
	var route []endpoint
	for depth := 0; node != nil; depth++ {
		if depth >= 5 {
			return "", errors.New("跳板链过深或包含循环")
		}
		c := node.Config
		port := c.Port
		if port == 0 {
			port = 22
		}
		route = append(route, endpoint{node.ID, c.Host, c.User, c.Proxy, c.JumpNodeID, port})
		if c.JumpNodeID == "" {
			break
		}
		var err error
		node, err = s.store.GetNode(c.JumpNodeID)
		if err != nil {
			return "", err
		}
	}
	data, _ := json.Marshal(route)
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:]), nil
}
func (s *Server) recoveryNode(rec *store.Recovery) (*store.Node, error) {
	node, err := s.store.GetNode(rec.NodeID)
	if err != nil {
		return nil, err
	}
	key, err := s.recoveryRoute(node)
	if err != nil {
		return nil, err
	}
	if key != rec.RouteKey {
		return nil, errors.New("主机地址、用户或跳板路径已变化，不能接入原终端。请恢复原配置，或仅忘记恢复记录后新建连接")
	}
	return node, nil
}
func (s *Server) liveRecovery(id string) *session.Info {
	for _, info := range s.sessions.List() {
		if info.RecoveryID == id && info.State == session.StateActive {
			return &info
		}
	}
	return nil
}

type recoveryView struct {
	*store.Recovery
	LiveSessionID session.ID `json:"liveSessionId,omitempty"`
}

func (s *Server) handleListRecovery(w http.ResponseWriter, r *http.Request) {
	records, err := s.store.ListRecovery()
	if err != nil {
		writeError(w, 500, err)
		return
	}
	out := make([]recoveryView, 0, len(records))
	for _, rec := range records {
		view := recoveryView{Recovery: rec}
		if live := s.liveRecovery(rec.ID); live != nil {
			view.LiveSessionID = live.ID
		}
		out = append(out, view)
	}
	writeJSON(w, 200, out)
}
func writeRecoveryError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, sshclient.ErrTmuxUnavailable):
		writeJSON(w, 422, map[string]string{"error": err.Error(), "code": "tmux_unavailable"})
	case errors.Is(err, sshclient.ErrTmuxMissing):
		writeJSON(w, 410, map[string]string{"error": err.Error(), "code": "tmux_missing"})
	default:
		writeDialError(w, err)
	}
}
func (s *Server) openPersistent(w http.ResponseWriter, r *http.Request, node *store.Node, rec *store.Recovery, req connectNodeRequest) {
	route, err := s.recoveryRoute(node)
	if err != nil {
		writeError(w, 409, err)
		return
	}
	if rec != nil {
		if route != rec.RouteKey {
			writeError(w, 409, errors.New("连接路径已改变，请恢复原配置或在会话库忘记原恢复记录"))
			return
		}
		if info := s.liveRecovery(rec.ID); info != nil {
			writeJSON(w, 200, info)
			return
		}
	}
	client, _, err := s.connect.DialNode(r.Context(), node.ID, req.Accepts)
	if err != nil {
		writeDialError(w, err)
		return
	}
	create := rec == nil
	if create {
		// Save BEFORE any remote mutation. An ambiguous timeout leaves an identity
		// that can be retried by attach, never blindly recreating a running task.
		rec, err = s.store.CreateRecovery(node.ID, node.Name, client.Target(), route)
		if err != nil {
			client.Close()
			writeError(w, 500, err)
			return
		}
	}
	backend, err := client.PreparePersistent(r.Context(), rec.ID, create, req.Cols, req.Rows)
	if err != nil {
		client.Close()
		if create && errors.Is(err, sshclient.ErrTmuxUnavailable) {
			if e := s.store.DeleteRecovery(rec.ID); e != nil {
				err = errors.Join(err, e)
			}
		}
		writeRecoveryError(w, err)
		return
	}
	if err = r.Context().Err(); err != nil {
		client.Close()
		writeRecoveryError(w, err)
		return
	}
	sess, err := s.sessions.Create(backend, session.Options{Label: node.Name, Cols: req.Cols, Rows: req.Rows, RecoveryID: rec.ID})
	if err != nil {
		writeRecoveryError(w, err)
		return
	}
	s.linkNodeSession(sess, node)
	writeJSON(w, 201, sess.Info())
}
func (s *Server) linkNodeSession(sess *session.Session, node *store.Node) {
	if s.monitor != nil {
		rules := make([]alert.Rule, len(node.Config.Alerts))
		for i, r := range node.Config.Alerts {
			rules[i] = alert.Rule{Metric: r.Metric, Op: r.Op, Threshold: r.Threshold, Duration: r.Duration}
		}
		s.monitor.StartCollector(sess, node.ID, node.Config.Monitor, rules)
	}
	s.getMonitorRegistry().Link(sess.ID(), node.ID)
}
func (s *Server) handleResumeRecovery(w http.ResponseWriter, r *http.Request) {
	var req connectNodeRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, 400, err)
		return
	}
	s.recoveryMu.Lock()
	defer s.recoveryMu.Unlock()
	s.nodeMu.Lock()
	defer s.nodeMu.Unlock()
	rec, err := s.store.GetRecovery(chi.URLParam(r, "id"))
	if err != nil {
		writeError(w, 404, err)
		return
	}
	node, err := s.recoveryNode(rec)
	if err != nil {
		writeError(w, 409, err)
		return
	}
	s.openPersistent(w, r, node, rec, req)
}
func (s *Server) handleTerminateRecovery(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Confirmed bool              `json:"confirmed"`
		Accepts   map[string]string `json:"accepts"`
	}
	if json.NewDecoder(r.Body).Decode(&req) != nil || !req.Confirmed {
		writeError(w, 400, errors.New("必须明确确认结束远端终端"))
		return
	}
	s.recoveryMu.Lock()
	defer s.recoveryMu.Unlock()
	s.nodeMu.Lock()
	defer s.nodeMu.Unlock()
	rec, err := s.store.GetRecovery(chi.URLParam(r, "id"))
	if err != nil {
		writeError(w, 404, err)
		return
	}
	node, err := s.recoveryNode(rec)
	if err != nil {
		writeError(w, 409, err)
		return
	}
	client, _, err := s.connect.DialNode(r.Context(), node.ID, req.Accepts)
	if err != nil {
		writeDialError(w, err)
		return
	}
	defer client.Close()
	if err = client.TerminatePersistent(r.Context(), rec.ID); err != nil {
		writeRecoveryError(w, err)
		return
	}
	if err = s.store.DeleteRecovery(rec.ID); err != nil {
		writeError(w, 500, fmt.Errorf("远端终端已结束，但清理恢复记录失败：%w", err))
		return
	}
	s.closeRecoveryViews(rec.ID)
	w.WriteHeader(204)
}
func (s *Server) closeRecoveryViews(id string) {
	for _, info := range s.sessions.List() {
		if info.RecoveryID == id {
			s.sessions.Close(info.ID)
			s.getMonitorRegistry().Unlink(info.ID)
		}
	}
}
func (s *Server) handleForgetRecovery(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Confirmed bool `json:"confirmed"`
	}
	if json.NewDecoder(r.Body).Decode(&req) != nil || !req.Confirmed {
		writeError(w, 400, errors.New("必须明确确认忘记恢复记录"))
		return
	}
	s.recoveryMu.Lock()
	defer s.recoveryMu.Unlock()
	id := chi.URLParam(r, "id")
	if _, err := s.store.GetRecovery(id); err != nil {
		writeError(w, 404, err)
		return
	}
	if s.liveRecovery(id) != nil {
		writeError(w, 409, errors.New("请先断开并保留该终端，再忘记恢复记录"))
		return
	}
	if err := s.store.DeleteRecovery(id); err != nil {
		writeError(w, 500, err)
		return
	}
	s.closeRecoveryViews(id)
	w.WriteHeader(204)
}
