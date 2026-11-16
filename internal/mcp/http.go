package mcp

import (
	"encoding/json"
	"net/http"
)

// HTTPHandler returns an http.Handler that speaks MCP over plain
// request/response JSON-RPC. POST a single request or a batch; the
// response comes back as JSON. Notifications (no id) yield 202 with no
// body. GET returns the allowed methods.
//
// This is the "streamable HTTP" transport in its simplest form: tool
// calls in sshx are synchronous, so we never need a server-initiated
// SSE stream. A future server-side notification (e.g. a monitor alert
// pushed to an MCP client) can upgrade this to an SSE channel.
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
		if err := json.NewDecoder(r.Body).Decode(&raw); err != nil {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"error":"invalid JSON: ` + err.Error() + `"}`))
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
