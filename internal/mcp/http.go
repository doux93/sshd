package mcp

import (
	"encoding/json"
	"io"
	"net/http"
)

// HTTPHandler returns an http.Handler that speaks MCP over plain
// request/response JSON-RPC. POST a single request or a batch; the
// response comes back as JSON. Notifications (no id) yield 202 with no
// body. Other HTTP methods receive 405 and an Allow header.
// This is a basic JSON-RPC transport, not full MCP Streamable HTTP.
func (s *Server) HTTPHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			w.Header().Set("Allow", "POST")
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusMethodNotAllowed)
			_, _ = w.Write([]byte(`{"error":"method not allowed; use POST"}`))
			return
		}
		var raw json.RawMessage
		decoder := json.NewDecoder(r.Body)
		if err := decoder.Decode(&raw); err != nil {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusBadRequest)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": "invalid JSON: " + err.Error()})
			return
		}
		// Reject trailing data before dispatching, so a malformed body cannot
		// partially execute its first JSON-RPC request.
		var extra json.RawMessage
		if err := decoder.Decode(&extra); err != io.EOF {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusBadRequest)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": "invalid JSON: expected a single JSON value"})
			return
		}
		resp := s.Handle(r.Context(), raw)
		if resp == nil {
			w.WriteHeader(http.StatusAccepted)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(resp)
	})
}
