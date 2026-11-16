package server

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"path"
	"strings"

	"sshx/internal/session"
)

// SFTP goes over REST rather than the WebSocket (docs/DESIGN.md §3.2):
// browsers give us streaming, progress and range requests for free on
// plain HTTP, and large transfers never compete with terminal latency.

// resolveFiles looks up the session and its file client for a request.
func (s *Server) resolveFiles(r *http.Request) (session.FileClient, error) {
	id := session.ID(r.URL.Query().Get("session"))
	if id == "" {
		return nil, errors.New("session parameter is required")
	}
	sess, ok := s.sessions.Get(id)
	if !ok {
		return nil, fmt.Errorf("session %s not found", id)
	}
	return sess.Files()
}

// writeFileError maps SFTP failures onto sensible HTTP statuses.
func writeFileError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, session.ErrNoFileSupport):
		writeError(w, http.StatusNotImplemented, err)
	case strings.Contains(err.Error(), "not found"),
		strings.Contains(err.Error(), "does not exist"),
		strings.Contains(err.Error(), "no such file"):
		writeError(w, http.StatusNotFound, err)
	case strings.Contains(err.Error(), "permission denied"):
		writeError(w, http.StatusForbidden, err)
	default:
		writeError(w, http.StatusBadGateway, err)
	}
}

func (s *Server) handleFileList(w http.ResponseWriter, r *http.Request) {
	files, err := s.resolveFiles(r)
	if err != nil {
		writeFileError(w, err)
		return
	}
	dir := r.URL.Query().Get("path")
	if dir == "" {
		dir = "."
	}
	// Report the resolved absolute path so the UI can show and navigate it.
	resolved, err := files.RealPath(dir)
	if err != nil {
		writeFileError(w, err)
		return
	}
	entries, err := files.List(resolved)
	if err != nil {
		writeFileError(w, err)
		return
	}
	if entries == nil {
		entries = []session.FileEntry{}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"path":    resolved,
		"entries": entries,
	})
}

func (s *Server) handleFileDownload(w http.ResponseWriter, r *http.Request) {
	files, err := s.resolveFiles(r)
	if err != nil {
		writeFileError(w, err)
		return
	}
	p := r.URL.Query().Get("path")
	if p == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "path is required"})
		return
	}
	info, err := files.Stat(p)
	if err != nil {
		writeFileError(w, err)
		return
	}
	if info.IsDir {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "cannot download a directory"})
		return
	}
	f, err := files.Open(p)
	if err != nil {
		writeFileError(w, err)
		return
	}
	defer f.Close()

	name := path.Base(p)
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Length", fmt.Sprint(info.Size))
	w.Header().Set("Content-Disposition",
		mime.FormatMediaType("attachment", map[string]string{"filename": name}))
	io.Copy(w, f)
}

// handleFileUpload streams the raw request body to the remote path, so
// memory use stays flat regardless of file size.
func (s *Server) handleFileUpload(w http.ResponseWriter, r *http.Request) {
	files, err := s.resolveFiles(r)
	if err != nil {
		writeFileError(w, err)
		return
	}
	p := r.URL.Query().Get("path")
	if p == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "path is required"})
		return
	}
	atomic, ok := files.(session.AtomicFileClient)
	if !ok {
		writeError(w, http.StatusNotImplemented, errors.New("backend does not support atomic uploads"))
		return
	}
	written, err := atomic.WriteAtomic(r.Context(), p, r.Body)
	if err != nil {
		writeError(w, http.StatusBadGateway, err)
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{"path": p, "size": written})
}

type fileOpRequest struct {
	Path      string `json:"path"`
	NewPath   string `json:"newPath,omitempty"`
	Recursive bool   `json:"recursive,omitempty"`
}

func (s *Server) handleFileMkdir(w http.ResponseWriter, r *http.Request) {
	files, req, ok := s.decodeFileOp(w, r)
	if !ok {
		return
	}
	if err := files.Mkdir(req.Path); err != nil {
		writeFileError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleFileDelete(w http.ResponseWriter, r *http.Request) {
	files, req, ok := s.decodeFileOp(w, r)
	if !ok {
		return
	}
	var err error
	if req.Recursive {
		err = files.RemoveAll(req.Path)
	} else {
		err = files.Remove(req.Path)
	}
	if err != nil {
		writeFileError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleFileRename(w http.ResponseWriter, r *http.Request) {
	files, req, ok := s.decodeFileOp(w, r)
	if !ok {
		return
	}
	if req.NewPath == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "newPath is required"})
		return
	}
	if err := files.Rename(req.Path, req.NewPath); err != nil {
		writeFileError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) decodeFileOp(w http.ResponseWriter, r *http.Request) (session.FileClient, fileOpRequest, bool) {
	files, err := s.resolveFiles(r)
	if err != nil {
		writeFileError(w, err)
		return nil, fileOpRequest{}, false
	}
	var req fileOpRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return nil, fileOpRequest{}, false
	}
	if req.Path == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "path is required"})
		return nil, fileOpRequest{}, false
	}
	return files, req, true
}
