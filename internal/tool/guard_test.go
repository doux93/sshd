package tool

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

// captureAuditor records every Audit call for assertions.
type captureAuditor struct {
	entries []auditRecord
}

type auditRecord struct {
	actor  string
	action string
}

func (a *captureAuditor) Audit(actor, action string, _ any) error {
	a.entries = append(a.entries, auditRecord{actor, action})
	return nil
}

func newTestRegistry() (*Registry, *captureAuditor) {
	aud := &captureAuditor{}
	r := NewRegistry(NewGuard(), aud, nil)
	RegisterDefaults(r)
	return r, aud
}

func TestReadonlyRejectsWriteCommand(t *testing.T) {
	r, _ := newTestRegistry()
	cc := &CallContext{Policy: PolicyReadonly, Actor: ActorMCP}
	args, _ := json.Marshal(map[string]string{"command": "rm -rf /tmp/test"})

	_, err := r.Call(context.Background(), "run_command", cc, args)
	if !IsGuardError(err) {
		t.Fatalf("expected GuardError, got %v", err)
	}
	ge := err.(*GuardError)
	if ge.Reason != "readonly" {
		t.Fatalf("reason = %q, want %q", ge.Reason, "readonly")
	}
}

func TestReadonlyRejectsArbitraryShell(t *testing.T) {
	g := NewGuard()
	for _, cmd := range []string{"ls -la /tmp", "echo hi > /tmp/test", "ls; touch /tmp/test", "env sh -c whoami", "cat $(id)", "systemctl status nginx"} {
		args, _ := json.Marshal(map[string]string{"command": cmd})
		if _, err := g.Check("run_command", &Tool{}, &CallContext{Policy: PolicyReadonly}, args); err == nil {
			t.Errorf("allowed shell %q", cmd)
		}
	}
}
func TestReadonlyAllowsStructuredInspection(t *testing.T) {
	r, _ := newTestRegistry()
	_, err := r.Call(context.Background(), "inspect_system", &CallContext{Policy: PolicyReadonly}, json.RawMessage(`{"report":"memory"}`))
	if IsGuardError(err) {
		t.Fatalf("inspection blocked: %v", err)
	}
	_, err = r.Call(context.Background(), "inspect_system", &CallContext{Policy: PolicyReadonly}, json.RawMessage(`{"report":"memory; touch /tmp/test"}`))
	if err == nil {
		t.Fatal("invalid report allowed")
	}
}

func TestReadonlyRejectsWriteFile(t *testing.T) {
	r, _ := newTestRegistry()
	cc := &CallContext{Policy: PolicyReadonly, Actor: ActorMCP}
	args, _ := json.Marshal(map[string]string{"path": "/etc/passwd", "content": "hacked"})

	_, err := r.Call(context.Background(), "write_file", cc, args)
	if !IsGuardError(err) {
		t.Fatalf("expected GuardError, got %v", err)
	}
	ge := err.(*GuardError)
	if ge.Reason != "readonly" {
		t.Fatalf("reason = %q, want %q", ge.Reason, "readonly")
	}
}

func TestConfirmBlocksWrite(t *testing.T) {
	r, _ := newTestRegistry()
	cc := &CallContext{Policy: PolicyConfirm, Actor: ActorAI}
	args, _ := json.Marshal(map[string]string{"command": "touch /tmp/x"})

	_, err := r.Call(context.Background(), "run_command", cc, args)
	if !IsGuardError(err) {
		t.Fatalf("expected GuardError, got %v", err)
	}
	ge := err.(*GuardError)
	if ge.Reason != "confirm-needed" {
		t.Fatalf("reason = %q, want %q", ge.Reason, "confirm-needed")
	}
}

func TestConfirmRejectsArbitraryShell(t *testing.T) {
	r, _ := newTestRegistry()
	_, err := r.Call(context.Background(), "run_command", &CallContext{Policy: PolicyConfirm}, json.RawMessage(`{"command":"df -h"}`))
	if !IsGuardError(err) {
		t.Fatalf("expected confirmation boundary: %v", err)
	}
}

func TestTrustedAllowsEverything(t *testing.T) {
	r, _ := newTestRegistry()
	cc := &CallContext{Policy: PolicyTrusted, Actor: ActorAI}
	args, _ := json.Marshal(map[string]string{"command": "rm -rf /tmp/x"})

	// No session, so the handler returns an error ? but the Guard must
	// not block it.
	_, err := r.Call(context.Background(), "run_command", cc, args)
	if IsGuardError(err) {
		t.Fatalf("trusted policy should not block, got %v", err)
	}
}

func TestDangerPatternProducesWarning(t *testing.T) {
	g := NewGuard()
	warn := g.dangerWarning("rm -rf /home")
	if warn == "" {
		t.Fatal("expected a danger warning for rm -rf")
	}
	warn = g.dangerWarning("find / -delete")
	if warn == "" {
		t.Fatal("expected a danger warning for find -delete")
	}
	warn = g.dangerWarning("ls -la")
	if warn != "" {
		t.Fatalf("expected no warning for ls, got %q", warn)
	}
}

func TestDeniedCallIsAudited(t *testing.T) {
	r, aud := newTestRegistry()
	cc := &CallContext{Policy: PolicyReadonly, Actor: ActorMCP}
	args, _ := json.Marshal(map[string]string{"command": "rm -rf /"})

	r.Call(context.Background(), "run_command", cc, args)

	if len(aud.entries) != 1 {
		t.Fatalf("expected 1 audit entry, got %d", len(aud.entries))
	}
	if aud.entries[0].action != "run_command" {
		t.Errorf("action = %q, want run_command", aud.entries[0].action)
	}
	if aud.entries[0].actor != "mcp" {
		t.Errorf("actor = %q, want mcp", aud.entries[0].actor)
	}
}

func TestUnknownToolRejected(t *testing.T) {
	r, _ := newTestRegistry()
	cc := &CallContext{Policy: PolicyTrusted, Actor: ActorAI}
	_, err := r.Call(context.Background(), "nonexistent", cc, nil)
	if err == nil || !strings.Contains(err.Error(), "unknown tool") {
		t.Fatalf("expected unknown tool error, got %v", err)
	}
}

func TestNormaliseDefaultsToReadonly(t *testing.T) {
	if AIPolicy("").Normalise() != PolicyReadonly {
		t.Fatal("empty policy should default to readonly")
	}
	if AIPolicy("garbage").Normalise() != PolicyReadonly {
		t.Fatal("unknown policy should default to readonly")
	}
}
