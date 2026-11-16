// Package sshconfig imports connection nodes from the user's OpenSSH client
// configuration (~/.ssh/config).
package sshconfig

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	ssh_config "github.com/kevinburke/ssh_config"

	"sshx/internal/store"
)

// ImportedHost is one usable Host entry from ssh_config.
type ImportedHost struct {
	Alias     string
	Host      string
	Port      int
	User      string
	KeyPath   string
	ProxyJump string // raw value, e.g. "user@bastion:2222" or an alias
}

// DefaultPath returns ~/.ssh/config.
func DefaultPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".ssh", "config"), nil
}

// Parse reads an ssh_config file and extracts concrete host entries,
// skipping wildcard patterns.
func Parse(path string) ([]ImportedHost, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	cfg, err := ssh_config.Decode(f)
	if err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}

	var hosts []ImportedHost
	for _, host := range cfg.Hosts {
		for _, pattern := range host.Patterns {
			alias := pattern.String()
			if strings.ContainsAny(alias, "*?!") {
				continue // wildcard/negation patterns are not connectable entries
			}
			h := ImportedHost{Alias: alias}
			h.Host, _ = cfg.Get(alias, "HostName")
			if h.Host == "" {
				h.Host = alias
			}
			portStr, _ := cfg.Get(alias, "Port")
			h.Port, _ = strconv.Atoi(portStr)
			h.User, _ = cfg.Get(alias, "User")
			h.KeyPath, _ = cfg.Get(alias, "IdentityFile")
			if h.KeyPath != "" {
				h.KeyPath = expandHome(h.KeyPath)
			}
			h.ProxyJump, _ = cfg.Get(alias, "ProxyJump")
			hosts = append(hosts, h)
		}
	}
	return hosts, nil
}

func expandHome(p string) string {
	if strings.HasPrefix(p, "~/") || p == "~" {
		if home, err := os.UserHomeDir(); err == nil {
			return filepath.Join(home, strings.TrimPrefix(p, "~"))
		}
	}
	return p
}

// BuildNodes converts imported hosts into store nodes under parentID,
// resolving ProxyJump references between the imported entries (by alias,
// hostname, or user@host[:port] form). Unresolvable jumps are dropped with
// a note in the returned warnings.
func BuildNodes(hosts []ImportedHost, parentID string) (nodes []*store.Node, warnings []string) {
	byAlias := map[string]*store.Node{}
	byHost := map[string]*store.Node{}

	for _, h := range hosts {
		authType := "agent" // ssh_config entries without a key normally rely on the agent
		if h.KeyPath != "" {
			authType = "key"
		}
		n := &store.Node{
			ID:       store.NewNodeID(), // pre-assigned so jump links can reference it
			ParentID: parentID,
			Kind:     "connection",
			Name:     h.Alias,
			Config: store.ConnConfig{
				Host:     h.Host,
				Port:     h.Port,
				User:     h.User,
				AuthType: authType,
				KeyPath:  h.KeyPath,
			},
		}
		nodes = append(nodes, n)
		byAlias[h.Alias] = n
		byHost[h.Host] = n
	}

	// Second pass: resolve ProxyJump now that all nodes exist.
	for i, h := range hosts {
		if h.ProxyJump == "" || strings.EqualFold(h.ProxyJump, "none") {
			continue
		}
		// Multi-hop "a,b,c": link to the last hop; OpenSSH dials left-to-
		// right, and our chain follows JumpNodeID recursively, so linking
		// each imported node to its immediate predecessor is a follow-up —
		// for now use the final hop and warn on multi-hop.
		hops := strings.Split(h.ProxyJump, ",")
		if len(hops) > 1 {
			warnings = append(warnings, fmt.Sprintf("%s: 多级 ProxyJump 仅保留最后一跳 %q", h.Alias, hops[len(hops)-1]))
		}
		hop := strings.TrimSpace(hops[len(hops)-1])
		target := hop
		if at := strings.LastIndex(hop, "@"); at >= 0 {
			target = hop[at+1:]
		}
		if colon := strings.LastIndex(target, ":"); colon >= 0 {
			target = target[:colon]
		}
		var jump *store.Node
		if n, ok := byAlias[target]; ok {
			jump = n
		} else if n, ok := byHost[target]; ok {
			jump = n
		}
		if jump == nil {
			warnings = append(warnings, fmt.Sprintf("%s: 找不到 ProxyJump 目标 %q,已忽略跳板设置", h.Alias, hop))
			continue
		}
		nodes[i].Config.JumpNodeID = jump.ID
	}
	return nodes, warnings
}
