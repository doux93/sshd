package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"sshx/internal/tool"
)

// fakeResolve returns a CallContext whose Policy is trusted so the guard
// lets write tools through. The session is nil; tests use tools that
// don't require one.
func fakeResolve(nodeID string, actor tool.Actor) (*tool.CallContext, error) {
	if nodeID == "bad" {
		return nil, errUnreachable
	}
	return &tool.CallContext{Policy: tool.PolicyTrusted, Actor: actor}, nil
}

var errUnreachable = errors.New("unreachable")

func newTestServer(t *testing.T) *Server {
	reg := tool.NewRegistry(tool.NewGuard(), nil, nil)
	reg.Register(&tool.Tool{
		Name:   "echo",
		Desc:   "Echo the message back.",
		Writes: false,
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"msg": map[string]any{"type": "string"},
			},
			"required": []string{"msg"},
		},
		Handler: func(ctx context.Context, cc *tool.CallContext, args json.RawMessage) (tool.Result, error) {
			var a struct {
				Msg string `json:"msg"`
			}
			_ = json.Unmarshal(args, &a)
			return tool.Result{Output: map[string]any{"echo": a.Msg}}, nil
		},
	})
	return New(reg, fakeResolve, nil)
}

func call(t *testing.T, s *Server, body string) map[string]any {
	t.Helper()
	out := s.Handle(context.Background(), []byte(body))
	if out == nil {
		t.Fatal("nil response")
	}
	var m map[string]any
	if err := json.Unmarshal(out, &m); err != nil {
		t.Fatalf("decode: %v; raw=%s", err, out)
	}
	return m
}

func TestInitialize(t *testing.T) {
	s := newTestServer(t)
	m := call(t, s, `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}`)
	if m["result"] == nil {
		t.Fatalf("no result: %v", m)
	}
	res := m["result"].(map[string]any)
	if res["protocolVersion"] != protocolVersion {
		t.Fatalf("protocolVersion=%v want %s", res["protocolVersion"], protocolVersion)
	}
	si := res["serverInfo"].(map[string]any)
	if si["name"] != serverName {
		t.Fatalf("serverInfo.name=%v", si["name"])
	}
}

func TestToolsList(t *testing.T) {
	s := newTestServer(t)
	m := call(t, s, `{"jsonrpc":"2.0","id":2,"method":"tools/list"}`)
	res := m["result"].(map[string]any)
	tools := res["tools"].([]any)
	if len(tools) != 1 {
		t.Fatalf("want 1 tool, got %d", len(tools))
	}
	et := tools[0].(map[string]any)
	if et["name"] != "echo" {
		t.Fatalf("name=%v", et["name"])
	}
	if et["inputSchema"] == nil {
		t.Fatal("inputSchema nil")
	}
}

func TestToolsCall_OK(t *testing.T) {
	s := newTestServer(t)
	m := call(t, s, `{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"echo","arguments":{"nodeId":"host1","msg":"hi"}}}`)
	res := m["result"].(map[string]any)
	content := res["content"].([]any)
	text := content[0].(map[string]any)["text"].(string)
	if !strings.Contains(text, `"echo"`) || !strings.Contains(text, `"hi"`) {
		t.Fatalf("unexpected content: %s", text)
	}
	if isErr, _ := res["isError"].(bool); isErr {
		t.Fatal("isError should be false on success")
	}
}

func TestToolsCall_MissingNodeId(t *testing.T) {
	s := newTestServer(t)
	m := call(t, s, `{"jsonrpc":"2.0","id":4,"method":"tools/call","params":{"name":"echo","arguments":{"msg":"hi"}}}`)
	if m["error"] == nil {
		t.Fatal("expected error for missing nodeId")
	}
}

func TestToolsCall_UnreachableNode(t *testing.T) {
	s := newTestServer(t)
	m := call(t, s, `{"jsonrpc":"2.0","id":5,"method":"tools/call","params":{"name":"echo","arguments":{"nodeId":"bad","msg":"hi"}}}`)
	res := m["result"].(map[string]any)
	if isErr, _ := res["isError"].(bool); !isErr {
		t.Fatal("expected isError=true for unreachable node")
	}
}

func TestNotificationsProduceNoResponse(t *testing.T) {
	s := newTestServer(t)
	out := s.Handle(context.Background(), []byte(`{"jsonrpc":"2.0","method":"notifications/initialized"}`))
	if out != nil {
		t.Fatalf("notification should yield nil response, got %s", out)
	}
}

func TestBatch(t *testing.T) {
	s := newTestServer(t)
	body := `[{"jsonrpc":"2.0","id":1,"method":"tools/list"},{"jsonrpc":"2.0","id":2,"method":"ping"}]`
	out := s.Handle(context.Background(), []byte(body))
	var arr []map[string]any
	if err := json.Unmarshal(out, &arr); err != nil {
		t.Fatalf("decode batch: %v", err)
	}
	if len(arr) != 2 {
		t.Fatalf("want 2 responses, got %d", len(arr))
	}
}

func TestMethodNotFound(t *testing.T) {
	s := newTestServer(t)
	m := call(t, s, `{"jsonrpc":"2.0","id":9,"method":"nope"}`)
	e := m["error"].(map[string]any)
	if int(e["code"].(float64)) != errMethodNotFound {
		t.Fatalf("code=%v", e["code"])
	}
}
