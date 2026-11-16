package server

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"

	"github.com/go-chi/chi/v5"

	"sshx/internal/alert"
	"sshx/internal/connect"
	"sshx/internal/session"
	"sshx/internal/sshconfig"
	"sshx/internal/store"
	"sshx/internal/vault"
)

// nodeRequest creates or updates a tree node. Secrets travel only in this
// request (TLS/loopback) and go straight to the vault; they are never
// persisted in the store or echoed back.
type nodeRequest struct {
	ParentID  string           `json:"parentId"`
	Kind      string           `json:"kind"`
	Name      string           `json:"name"`
	SortOrder int              `json:"sortOrder"`
	Config    store.ConnConfig `json:"config"`
	// nil = leave unchanged; "" = clear; other = set.
	Password   *string `json:"password,omitempty"`
	Passphrase *string `json:"passphrase,omitempty"`
}

func (s *Server) handleListNodes(w http.ResponseWriter, r *http.Request) {
	nodes, err := s.store.ListNodes()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	if nodes == nil {
		nodes = []*store.Node{}
	}
	writeJSON(w, http.StatusOK, nodes)
}

func (s *Server) handleCreateNode(w http.ResponseWriter, r *http.Request) {
	s.nodeMu.Lock()
	defer s.nodeMu.Unlock()
	var req nodeRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	if req.Name == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "name is required"})
		return
	}
	node := &store.Node{
		ParentID:  req.ParentID,
		Kind:      req.Kind,
		Name:      req.Name,
		SortOrder: req.SortOrder,
		Config:    req.Config,
	}
	node.Config.HasPassword = req.Password != nil && *req.Password != ""
	node.Config.HasPassphrase = req.Passphrase != nil && *req.Passphrase != ""
	if err := s.store.CreateNode(node); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	if err := s.storeSecrets(node.ID, req.Password, req.Passphrase); err != nil {
		if _, rollbackErr := s.store.DeleteSubtree(node.ID); rollbackErr != nil {
			err = errors.Join(err, rollbackErr)
		}
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusCreated, node)
}

func (s *Server) handleUpdateNode(w http.ResponseWriter, r *http.Request) {
	s.nodeMu.Lock()
	defer s.nodeMu.Unlock()
	id := chi.URLParam(r, "id")
	existing, err := s.store.GetNode(id)
	if err != nil {
		writeError(w, http.StatusNotFound, err)
		return
	}
	var req nodeRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}

	before := *existing
	existing.ParentID = req.ParentID
	existing.Name = req.Name
	existing.SortOrder = req.SortOrder
	req.Config.HasPassword = existing.Config.HasPassword
	req.Config.HasPassphrase = existing.Config.HasPassphrase
	existing.Config = req.Config
	if req.Password != nil {
		existing.Config.HasPassword = *req.Password != ""
	}
	if req.Passphrase != nil {
		existing.Config.HasPassphrase = *req.Passphrase != ""
	}

	if err := s.store.UpdateNode(existing); err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	if err := s.storeSecrets(id, req.Password, req.Passphrase); err != nil {
		if rollbackErr := s.store.UpdateNode(&before); rollbackErr != nil {
			err = errors.Join(err, rollbackErr)
		}
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, existing)
}

// storeSecrets applies pointer semantics: nil = unchanged, "" = delete.
// Two secrets may live in an external OS keyring, so use compensating writes
// on failure. Callers serialize node mutations and roll back node metadata.
func (s *Server) storeSecrets(nodeID string, password, passphrase *string) error {
	type change struct {
		key             string
		value, previous *string
	}
	changes := []change{}
	for _, c := range []change{{key: vault.PasswordKey(nodeID), value: password}, {key: vault.PassphraseKey(nodeID), value: passphrase}} {
		if c.value == nil {
			continue
		}
		if s.vault == nil {
			return fmt.Errorf("credential store unavailable")
		}
		old, err := s.vault.Get(c.key)
		if err == nil {
			c.previous = &old
		} else if !errors.Is(err, vault.ErrNotFound) {
			return err
		}
		changes = append(changes, c)
	}
	apply := func(key string, value *string) error {
		if value == nil || *value == "" {
			return s.vault.Delete(key)
		}
		return s.vault.Put(key, *value)
	}
	for i, c := range changes {
		if err := apply(c.key, c.value); err != nil {
			for j := i; j >= 0; j-- {
				if rollback := apply(changes[j].key, changes[j].previous); rollback != nil {
					err = errors.Join(err, fmt.Errorf("credential rollback: %w", rollback))
				}
			}
			return err
		}
	}
	return nil
}

func (s *Server) handleDeleteNode(w http.ResponseWriter, r *http.Request) {
	s.nodeMu.Lock()
	defer s.nodeMu.Unlock()
	id := chi.URLParam(r, "id")
	ids, err := s.store.DeleteSubtree(id)
	if err != nil {
		writeError(w, http.StatusNotFound, err)
		return
	}
	for _, nid := range ids {
		s.vault.Delete(vault.PasswordKey(nid))
		s.vault.Delete(vault.PassphraseKey(nid))
	}
	w.WriteHeader(http.StatusNoContent)
}

// connectNodeRequest opens a session on a stored node. Accepts carries
// host-key fingerprints the user confirmed in the TOFU dialog.
type connectNodeRequest struct {
	Cols             int               `json:"cols"`
	Rows             int               `json:"rows"`
	Accepts          map[string]string `json:"accepts,omitempty"`
	ShellIntegration bool              `json:"shellIntegration,omitempty"`
}

func (s *Server) handleConnectNode(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	var req connectNodeRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}

	client, node, err := s.connect.DialNode(r.Context(), id, req.Accepts)
	if err != nil {
		writeDialError(w, err)
		return
	}
	sess, err := s.sessions.Create(client, session.Options{
		Label: node.Name,
		Cols:  req.Cols,
		Rows:  req.Rows,
		// Saved-node connects always install the OSC 7 cwd hook so the
		// file panel follows `cd`. Quick-connect still honours the
		// request-level opt-out (ShellIntegration: false).
		// A per-node toggle in ConnConfig is the long-term fix; for now
		// the saved-node path is always-on.
		ShellIntegration: true,
	})
	if err != nil {
		writeError(w, http.StatusBadGateway, err)
		return
	}

	// Start monitoring if the node has it enabled.
	if s.monitor != nil {
		rules := make([]alert.Rule, len(node.Config.Alerts))
		for i, r := range node.Config.Alerts {
			rules[i] = alert.Rule{
				Metric: r.Metric, Op: r.Op,
				Threshold: r.Threshold, Duration: r.Duration,
			}
		}
		s.monitor.StartCollector(sess, node.ID, node.Config.Monitor, rules)
	}
	s.getMonitorRegistry().Link(sess.ID(), node.ID)

	writeJSON(w, http.StatusCreated, sess.Info())
}

// writeDialError maps a host-key prompt to 409 so the UI can confirm and
// retry; everything else is a plain dial failure.
func writeDialError(w http.ResponseWriter, err error) {
	var prompt *connect.HostKeyPromptError
	if errors.As(err, &prompt) {
		writeJSON(w, http.StatusConflict, map[string]any{
			"error":   prompt.Error(),
			"hostKey": prompt,
		})
		return
	}
	code, hint := dialErrorHint(err)
	writeJSON(w, http.StatusBadGateway, map[string]string{"error": err.Error(), "code": code, "hint": hint})
}

func (s *Server) handleImportSSHConfig(w http.ResponseWriter, r *http.Request) {
	path, err := sshconfig.DefaultPath()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	hosts, err := sshconfig.Parse(path)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	if len(hosts) == 0 {
		writeJSON(w, http.StatusOK, map[string]any{"imported": 0, "warnings": []string{}})
		return
	}

	folder := &store.Node{Kind: "folder", Name: "ssh_config 导入"}
	if err := s.store.CreateNode(folder); err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	nodes, warnings := sshconfig.BuildNodes(hosts, folder.ID)
	if warnings == nil {
		warnings = []string{}
	}
	imported := 0
	for _, n := range nodes {
		if err := s.store.CreateNode(n); err != nil {
			warnings = append(warnings, n.Name+": "+err.Error())
			continue
		}
		imported++
	}
	writeJSON(w, http.StatusOK, map[string]any{"imported": imported, "warnings": warnings})
}
