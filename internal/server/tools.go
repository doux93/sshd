package server

import (
	"context"
	"encoding/json"
	"fmt"

	"sshx/internal/session"
	"sshx/internal/tool"
)

// callContext resolves a nodeID into a *tool.CallContext: it loads the
// node (for its AIPolicy), finds the node's active session, and bundles
// them so the registered tool handlers can operate against the live
// session. Returns an error if there is no live session for the node
// (the caller must connect first) or the node is unknown.
//
// This is the shared seam consumed by the MCP server, Ctrl+K command
// generation, and the future built-in agent: every caller that wants
// to drive the tool layer goes through here, so the Guard always sees a
// correctly-scoped CallContext with the node's real policy.
func (s *Server) callContext(nodeID string, actor tool.Actor) (*tool.CallContext, error) {
	if s.toolReg == nil {
		return nil, fmt.Errorf("tool layer not wired")
	}
	node, err := s.store.GetNode(nodeID)
	if err != nil {
		return nil, fmt.Errorf("node: %w", err)
	}
	sess, ok := s.activeSessionForNode(nodeID)
	if !ok {
		return nil, fmt.Errorf("no active session for node %q; connect first", nodeID)
	}

	return &tool.CallContext{
		Session: sess,
		Policy:  tool.AIPolicy(node.Config.AIPolicy),
		Actor:   actor,
	}, nil
}

// registerMonitorTool adds the get_monitor_metrics tool on top of the
// core defaults registered in main. It is registered as a method on the
// server so the handler can close over the monitor manager; the tool
// layer itself stays free of a monitor import.
func (s *Server) registerMonitorTool() {
	if s.toolReg == nil || s.monitor == nil {
		return
	}
	s.toolReg.Register(&tool.Tool{
		Name:   "get_monitor_metrics",
		Desc:   "Read recent agentless system metrics (CPU, memory, load, disk) for the session's host. Optional limit sets the sample count (default 60, max 720).",
		Writes: false,
		Handler: func(ctx context.Context, cc *tool.CallContext, args json.RawMessage) (tool.Result, error) {
			var a struct {
				Limit int `json:"limit,omitempty"`
			}
			if len(args) > 0 {
				_ = json.Unmarshal(args, &a)
			}
			limit := a.Limit
			if limit <= 0 || limit > 720 {
				limit = 60
			}
			reg := s.getMonitorRegistry()
			nodeID := reg.NodeBySession(cc.Session.ID())
			if nodeID == "" {
				return tool.Result{Error: "session has no associated node"}, nil
			}
			series := s.monitor.Metrics(cc.Session.ID())
			if series == nil {
				return tool.Result{Output: map[string]any{"nodeId": nodeID, "samples": []any{}}}, nil
			}
			return tool.Result{Output: map[string]any{
				"nodeId":  nodeID,
				"count":   limit,
				"samples": series.Recent(limit),
			}}, nil
		},
	})
}

func (s *Server) activeSessionForNode(nodeID string) (*session.Session, bool) {
	infos := s.sessions.List()
	reg := s.getMonitorRegistry()
	for i := len(infos) - 1; i >= 0; i-- {
		info := infos[i]
		if info.State == session.StateActive && reg.NodeBySession(info.ID) == nodeID {
			return s.sessions.Get(info.ID)
		}
	}
	return nil, false
}
