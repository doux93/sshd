package server

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"sshx/internal/store"
	"strings"
	"time"
)

func (s *Server) handleTestConnection(w http.ResponseWriter, r *http.Request) {
	var req struct {
		nodeRequest
		NodeID  string            `json:"nodeId"`
		Accepts map[string]string `json:"accepts"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, 400, err)
		return
	}
	if strings.TrimSpace(req.Config.Host) == "" || strings.TrimSpace(req.Config.User) == "" || req.Config.Port < 1 || req.Config.Port > 65535 {
		writeError(w, 400, errors.New("host, user and port (1-65535) are required"))
		return
	}
	node := &store.Node{ID: req.NodeID, Name: req.Name, Kind: "connection", Config: req.Config}
	node.Config.HasPassword = false
	node.Config.HasPassphrase = false
	if req.NodeID != "" {
		existing, err := s.store.GetNode(req.NodeID)
		if err != nil {
			writeError(w, 404, err)
			return
		}
		node.Config.HasPassword = existing.Config.HasPassword
		node.Config.HasPassphrase = existing.Config.HasPassphrase
	}
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	start := time.Now()
	client, err := s.connect.DialDraft(ctx, node, req.Password, req.Passphrase, req.Accepts)
	if err != nil {
		writeDialError(w, err)
		return
	}
	defer client.Close()
	writeJSON(w, 200, map[string]any{"ok": true, "target": client.Target(), "elapsedMs": time.Since(start).Milliseconds()})
}
