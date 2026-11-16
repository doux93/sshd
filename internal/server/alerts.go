package server

import (
	"github.com/go-chi/chi/v5"
	"net/http"
	"strconv"
)

func (s *Server) handleListAlerts(w http.ResponseWriter, r *http.Request) {
	before, _ := strconv.ParseInt(r.URL.Query().Get("before"), 10, 64)
	if before < 0 {
		http.Error(w, "invalid cursor", 400)
		return
	}
	items, err := s.store.ListAlerts(r.URL.Query().Get("nodeId"), before, 100)
	if err != nil {
		writeError(w, 500, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, 200, items)
}
func (s *Server) handleAckAlert(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil || id <= 0 {
		http.Error(w, "invalid alert id", 400)
		return
	}
	ok, err := s.store.AcknowledgeAlert(id)
	if err != nil {
		writeError(w, 500, err)
		return
	}
	if !ok {
		http.Error(w, "alert not found", 404)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
