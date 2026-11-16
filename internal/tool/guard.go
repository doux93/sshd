package tool

import (
	"encoding/json"
	"fmt"
	"regexp"
)

// Guard is a capability gate. Arbitrary shell strings cannot be proven read-only:
// only trusted nodes may run them. Readonly/confirm nodes use structured tools.
// Destructive-pattern warnings are advisory and never an authorization decision.
type Guard struct{ dangerPatterns []*regexp.Regexp }

func NewGuard() *Guard { return &Guard{dangerPatterns: compileDangerPatterns()} }

func compileDangerPatterns() []*regexp.Regexp {
	pats := []string{
		`rm\s+(-[a-zA-Z]*f[a-zA-Z]*|--force)`, // rm with force
		`rm\s+-[a-zA-Z]*r`,                    // rm recursive
		`find\s+.*-delete`,                    // find with delete
		`truncate\s+-s\s*0`,                   // truncate to zero
		`mkfs`,                                // format filesystem
		`dd\s+.*of=`,                          // dd writing to a file/device
		`>\s*/dev/s`,                          // redirect to block device
		`shutdown|reboot|halt|poweroff`,       // system power
		`DROP\s+(DATABASE|TABLE)`,             // SQL drops
		`chmod\s+(-R\s+)?0?777`,               // world-writable
		`:\(\)\s*\{`,                          // fork bomb
	}
	out := make([]*regexp.Regexp, len(pats))
	for i, p := range pats {
		out[i] = regexp.MustCompile(p)
	}
	return out
}

// Check enforces tool capabilities and adds advisory warnings. It returns a warning string (non-empty
// if a danger pattern matched) and a GuardError if the call is blocked.
func (g *Guard) Check(name string, t *Tool, cc *CallContext, args json.RawMessage) (string, error) {
	policy := cc.Policy.Normalise()
	writes := t.Writes

	// Arbitrary shell always requires trusted policy; parse only for advisory warnings.
	var cmd string
	if name == "run_command" {
		var a struct {
			Command string `json:"command"`
		}
		_ = json.Unmarshal(args, &a)
		cmd = a.Command
		writes = true
	}

	// Authorization is based on tool capability, never shell text.
	switch policy {
	case PolicyReadonly:
		if writes {
			return "", &GuardError{Reason: "readonly", Command: cmd, Detail: fmt.Sprintf("tool %q may modify the system; use structured inspection tools", name)}
		}

	case PolicyConfirm:
		if writes {
			return "", &GuardError{
				Reason:  "confirm-needed",
				Command: cmd,
				Detail:  fmt.Sprintf("tool %q modifies the system; confirmation required", name),
			}
		}
		// PolicyTrusted and read-only tools under confirm: fall through.
	}

	// Destructive-pattern detection is advisory.
	warning := ""
	if cmd != "" {
		warning = g.dangerWarning(cmd)
	}
	return warning, nil
}

// dangerWarning returns a human-readable warning if cmd matches a danger
// pattern, or "" otherwise.
func (g *Guard) dangerWarning(cmd string) string {
	for _, p := range g.dangerPatterns {
		if p.MatchString(cmd) {
			return fmt.Sprintf("command matches a destructive pattern (%s); proceed with caution", p.String())
		}
	}
	return ""
}
