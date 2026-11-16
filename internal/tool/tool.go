package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"sync"

	"sshx/internal/session"
)

// AIPolicy is the per-node structural guardrail ? the only real security
// property in the AI tool layer (see DESIGN.md ?4.8). Everything else
// (whitelist, regex blacklist) is advisory.
type AIPolicy string

const (
	// PolicyReadonly: AI may only use structured read-only tools. Write
	// operations are structurally refused, no matter what the LLM says.
	// Production machines default to this.
	PolicyReadonly AIPolicy = "readonly"
	// PolicyConfirm: write operations are refused with confirm-needed; execute them
	// manually in the terminal. No automatic approval/resume queue exists.
	PolicyConfirm AIPolicy = "confirm"
	// PolicyTrusted: everything runs directly (test machines only).
	PolicyTrusted AIPolicy = "trusted"
)

// Normalise returns the policy, defaulting to the safest one (readonly).
func (p AIPolicy) Normalise() AIPolicy {
	switch p {
	case PolicyConfirm, PolicyTrusted:
		return p
	default:
		return PolicyReadonly
	}
}

// Actor identifies who initiated a tool call, for audit purposes only.
type Actor string

const (
	ActorHuman Actor = "human"
	ActorAI    Actor = "ai"
	ActorMCP   Actor = "mcp"
)

// CallContext carries everything a Tool handler needs: the session to
// operate on, the node's AI policy, and the actor (for audit logging).
type CallContext struct {
	Session *session.Session
	Policy  AIPolicy
	Actor   Actor
}

// Result is what a tool returns. Output must be JSON-marshalable.
type Result struct {
	Output  any    `json:"output"`
	Error   string `json:"error,omitempty"`
	Warning string `json:"warning,omitempty"` // non-fatal Guard warning (blacklist match)
}

// Tool is one registered tool. Writes indicates whether the tool modifies
// the remote system; the Guard uses it to decide confirmation under
// PolicyConfirm.
type Tool struct {
	Name   string
	Desc   string
	Writes bool
	// InputSchema is a JSON-Schema (as a Go value) describing the args
	// the Handler accepts. MCP/Ctrl+K clients use it to render the call
	// form; nil means "object, no declared properties".
	InputSchema any
	Handler     func(ctx context.Context, cc *CallContext, args json.RawMessage) (Result, error)
}

// Auditor records every tool call. The store implements this; tests use a
// no-op stub.
type Auditor interface {
	Audit(actor, action string, detail any) error
}

// noopAuditor discards everything; used when no store is wired.
type noopAuditor struct{}

func (noopAuditor) Audit(string, string, any) error { return nil }

// Registry holds registered tools and dispatches calls through the Guard.
// MCP servers, Ctrl+K, and (future) built-in agents all consume it.
type Registry struct {
	mu    sync.RWMutex
	tools map[string]*Tool
	guard *Guard
	audit Auditor
	log   *slog.Logger
}

// NewRegistry creates an empty registry with the given Guard. If audit is
// nil, calls are not recorded.
func NewRegistry(guard *Guard, audit Auditor, log *slog.Logger) *Registry {
	if audit == nil {
		audit = noopAuditor{}
	}
	if log == nil {
		log = slog.Default()
	}
	return &Registry{
		tools: make(map[string]*Tool),
		guard: guard,
		audit: audit,
		log:   log,
	}
}

// Register adds a tool. Re-registering a name replaces the previous entry.
func (r *Registry) Register(t *Tool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.tools[t.Name] = t
}

// Names returns the registered tool names.
func (r *Registry) Names() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	names := make([]string, 0, len(r.tools))
	for n := range r.tools {
		names = append(names, n)
	}
	return names
}

// List returns shallow copies of the registered tools (stable enough
// for a tools/list snapshot). Callers must not mutate the returned
// handlers; only the metadata fields are copied.
func (r *Registry) List() []*Tool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]*Tool, 0, len(r.tools))
	for _, t := range r.tools {
		out = append(out, t)
	}
	return out
}

// GuardError is returned when the Guard blocks a call.
type GuardError struct {
	Reason  string `json:"reason"`            // "readonly" | "confirm-needed"
	Command string `json:"command,omitempty"` // for run_command
	Detail  string `json:"detail,omitempty"`
}

func (e *GuardError) Error() string {
	return fmt.Sprintf("guard: %s: %s", e.Reason, e.Detail)
}

// IsGuardError reports whether err is a GuardError.
func IsGuardError(err error) bool {
	_, ok := err.(*GuardError)
	return ok
}

// Call dispatches a tool call through the Guard. The Guard may deny the
// call (readonly), request confirmation (confirm), or allow it (trusted).
// Every call ? allowed or denied ? is recorded in the audit log.
func (r *Registry) Call(ctx context.Context, name string, cc *CallContext, args json.RawMessage) (Result, error) {
	r.mu.RLock()
	t, ok := r.tools[name]
	r.mu.RUnlock()
	if !ok {
		return Result{}, fmt.Errorf("unknown tool %q", name)
	}

	// Let the Guard decide before the handler runs.
	warning, gerr := r.guard.Check(name, t, cc, args)
	if gerr != nil {
		r.audit.Audit(string(cc.Actor), name, map[string]any{
			"denied":       true,
			"reason":       gerr.(*GuardError).Reason,
			"commandBytes": len(gerr.(*GuardError).Command),
		})
		return Result{}, gerr
	}

	res, err := t.Handler(ctx, cc, args)
	if warning != "" {
		res.Warning = warning
	}

	detail := map[string]any{"argumentBytes": len(args)}
	if cc.Session != nil {
		detail["sessionId"] = cc.Session.ID()
	}
	if err != nil {
		detail["error"] = err.Error()
	} else if res.Error != "" {
		detail["error"] = res.Error
	}
	r.audit.Audit(string(cc.Actor), name, detail)

	return res, err
}
