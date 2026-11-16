package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"

	"sshx/internal/tool"
)

// ResolveFunc maps a nodeID to a tool.CallContext (live session + the
// node's AI policy). The HTTP server provides this by closing over its
// session manager, store and monitor registry; the MCP layer stays free
// of those imports.
type ResolveFunc func(nodeID string, actor tool.Actor) (*tool.CallContext, error)

const (
	protocolVersion = "2024-11-05"
	serverName      = "sshx"
	serverVersion   = "0.1.0"
)

// Server is the MCP JSON-RPC dispatcher. It is stateless apart from the
// shared tool registry and node resolver, so one instance can serve any
// number of HTTP requests concurrently.
type Server struct {
	reg     *tool.Registry
	resolve ResolveFunc
	log     *slog.Logger
}

// New builds a dispatcher. resolve may be nil (tools/list still works;
// tools/call will report that no resolver is wired).
func New(reg *tool.Registry, resolve ResolveFunc, log *slog.Logger) *Server {
	if log == nil {
		log = slog.Default()
	}
	return &Server{reg: reg, resolve: resolve, log: log}
}

// Handle processes one request body, which may be a single JSON-RPC
// object or a batch array. It returns the JSON response, or nil if the
// input was a notification (no id) and so needs no reply.
func (s *Server) Handle(ctx context.Context, raw []byte) []byte {
	// Batch?
	var batch []json.RawMessage
	if err := json.Unmarshal(raw, &batch); err == nil && len(batch) > 0 {
		// Heuristic: a non-empty top-level array is a batch.
		resp := make([]rpcResponse, 0, len(batch))
		for _, b := range batch {
			if r := s.handleOne(ctx, b); r != nil {
				resp = append(resp, *r)
			}
		}
		if len(resp) == 0 {
			return nil
		}
		out, _ := json.Marshal(resp)
		return out
	}
	if r := s.handleOne(ctx, raw); r != nil {
		out, _ := json.Marshal(r)
		return out
	}
	return nil
}

func (s *Server) handleOne(ctx context.Context, raw []byte) *rpcResponse {
	var req rpcRequest
	if err := json.Unmarshal(raw, &req); err != nil {
		return &rpcResponse{JSONRPC: "2.0", Error: &rpcError{Code: errParseError, Message: "parse error"}}
	}
	if req.JSONRPC == "" {
		req.JSONRPC = "2.0"
	}
	isNotif := len(req.ID) == 0 || string(req.ID) == "null"
	resp := &rpcResponse{JSONRPC: "2.0", ID: req.ID}

	var result any
	var rerr *rpcError
	switch req.Method {
	case "initialize":
		result, rerr = s.handleInitialize(req.Params)
	case "ping":
		result = map[string]any{}
	case "tools/list":
		result, rerr = s.handleToolsList()
	case "tools/call":
		result, rerr = s.handleToolsCall(ctx, req.Params)
	case "notifications/initialized":
		return nil // notification: no response
	default:
		rerr = &rpcError{Code: errMethodNotFound, Message: fmt.Sprintf("method not found: %s", req.Method)}
	}
	if isNotif {
		return nil
	}
	resp.Result = result
	resp.Error = rerr
	return resp
}

func (s *Server) handleInitialize(params json.RawMessage) (any, *rpcError) {
	// Accept an optional default nodeId binding (params._sshx.nodeId).
	// We do not currently persist per-connection bindings across the
	// stateless HTTP transport; the field is acknowledged for forward
	// compatibility but tools/call still requires nodeId per call.
	return map[string]any{
		"protocolVersion": protocolVersion,
		"capabilities": map[string]any{
			"tools":   map[string]any{},
			"logging": map[string]any{},
		},
		"serverInfo": map[string]any{
			"name":    serverName,
			"version": serverVersion,
		},
		"_sshx": map[string]any{
			"nodeRouting": "tools/call arguments must include nodeId to target a host",
		},
	}, nil
}

func (s *Server) handleToolsList() (any, *rpcError) {
	tools := s.reg.List()
	out := make([]map[string]any, 0, len(tools))
	for _, t := range tools {
		out = append(out, map[string]any{
			"name":        t.Name,
			"description": t.Desc,
			"inputSchema": routedSchema(t.InputSchema),
		})
	}
	return map[string]any{"tools": out}, nil
}

func (s *Server) handleToolsCall(ctx context.Context, params json.RawMessage) (any, *rpcError) {
	var p struct {
		Name      string          `json:"name"`
		Arguments json.RawMessage `json:"arguments"`
	}
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, &rpcError{Code: errInvalidParams, Message: "bad params: " + err.Error()}
	}
	if p.Name == "" {
		return nil, &rpcError{Code: errInvalidParams, Message: "name is required"}
	}

	// Pop the vendor nodeId from arguments; the rest go to the handler.
	var args map[string]json.RawMessage
	nodeID := ""
	if len(p.Arguments) > 0 {
		if err := json.Unmarshal(p.Arguments, &args); err != nil {
			return nil, &rpcError{Code: errInvalidParams, Message: "arguments must be an object"}
		}
		if rawNode, ok := args["nodeId"]; ok {
			_ = json.Unmarshal(rawNode, &nodeID)
			delete(args, "nodeId")
		}
	}
	if nodeID == "" {
		return nil, &rpcError{Code: errInvalidParams, Message: "nodeId is required in arguments (target host)"}
	}
	if s.resolve == nil {
		return nil, &rpcError{Code: errInternal, Message: "no resolver wired"}
	}

	cc, err := s.resolve(nodeID, tool.ActorMCP)
	if err != nil {
		return mcpToolError("node/session unavailable: " + err.Error()), nil
	}

	var callArgs json.RawMessage
	if len(args) > 0 {
		callArgs, _ = json.Marshal(args)
	} else {
		callArgs = json.RawMessage("{}")
	}

	res, err := s.reg.Call(ctx, p.Name, cc, callArgs)
	if err != nil {
		if tool.IsGuardError(err) {
			return mcpToolError("blocked by guard: " + err.Error()), nil
		}
		return nil, &rpcError{Code: errInternal, Message: err.Error()}
	}
	body, _ := json.MarshalIndent(res, "", "  ")
	return map[string]any{
		"content": []map[string]any{
			{"type": "text", "text": string(body)},
		},
		"isError": res.Error != "",
	}, nil
}

// mcpToolError builds a non-fatal MCP tool result (isError=true) for
// semantic failures (no live session, guard denial, tool-reported
// error). Distinct from a JSON-RPC error, which means the call could
// not be made at all.
func mcpToolError(msg string) any {
	return map[string]any{
		"content": []map[string]any{
			{"type": "text", "text": msg},
		},
		"isError": true,
	}
}

func routedSchema(schema any) map[string]any {
	out := map[string]any{"type": "object"}
	if raw, err := json.Marshal(schema); err == nil {
		_ = json.Unmarshal(raw, &out)
	}
	if out == nil {
		out = map[string]any{"type": "object"}
	}
	props, ok := out["properties"].(map[string]any)
	if !ok {
		props = map[string]any{}
	}
	props["nodeId"] = map[string]any{"type": "string", "description": "Saved target node ID"}
	out["properties"] = props
	required, _ := out["required"].([]any)
	out["required"] = append(required, "nodeId")
	return out
}
