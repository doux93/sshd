package mcp

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"sshx/internal/tool"
)

func TestHTTPMalformedBodiesReturnJSONBeforeDispatch(t *testing.T) {
	s := newTestServer(t)
	calls := 0
	s.reg.Register(&tool.Tool{Name: "count", Handler: func(context.Context, *tool.CallContext, json.RawMessage) (tool.Result, error) {
		calls++
		return tool.Result{}, nil
	}})
	request := `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"count","arguments":{"nodeId":"host1"}}}`
	for _, body := range []string{
		`{"a":1 "b":2}`, // The decoder's error contains a double quote.
		`{"a":"\q"}`, "", "{\n",
		request + `{}`, request + ` trailing`,
	} {
		t.Run(body, func(t *testing.T) {
			w := httptest.NewRecorder()
			s.HTTPHandler().ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/api/mcp", strings.NewReader(body)))
			if w.Code != http.StatusBadRequest || w.Header().Get("Content-Type") != "application/json" {
				t.Fatalf("status=%d headers=%v", w.Code, w.Header())
			}
			var response map[string]string
			if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil || response["error"] == "" {
				t.Fatalf("invalid error response %q: %v", w.Body.String(), err)
			}
			if calls != 0 {
				t.Fatal("malformed request executed a tool")
			}
		})
	}
}

func TestHTTPValidRequestsAndNotifications(t *testing.T) {
	s := newTestServer(t)
	for _, tc := range []struct {
		body   string
		status int
	}{
		{"{\"jsonrpc\":\"2.0\",\"id\":1,\"method\":\"ping\"}\n\t ", http.StatusOK},
		{`[{"jsonrpc":"2.0","id":1,"method":"ping"},{"jsonrpc":"2.0","id":2,"method":"ping"}]`, http.StatusOK},
		{`{"jsonrpc":"2.0","method":"notifications/initialized"}`, http.StatusAccepted},
	} {
		w := httptest.NewRecorder()
		s.HTTPHandler().ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/api/mcp", strings.NewReader(tc.body)))
		if w.Code != tc.status {
			t.Fatalf("status=%d body=%q", w.Code, w.Body.String())
		}
		if tc.status == http.StatusAccepted {
			if w.Body.Len() != 0 {
				t.Fatal("notification returned a body")
			}
		} else if !json.Valid(w.Body.Bytes()) {
			t.Fatalf("invalid response: %s", w.Body.String())
		}
	}
}
