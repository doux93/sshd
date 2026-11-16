package sshconfig

import (
	"os"
	"path/filepath"
	"testing"
)

const sampleConfig = `
Host web
    HostName web.example.com
    User deploy
    Port 2222
    IdentityFile ~/.ssh/id_web

Host bastion
    HostName bastion.example.com
    User ops

Host internal
    HostName 10.0.0.5
    User root
    ProxyJump bastion

Host *
    ServerAliveInterval 60
`

func TestParseAndBuildNodes(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config")
	if err := os.WriteFile(path, []byte(sampleConfig), 0o600); err != nil {
		t.Fatal(err)
	}

	hosts, err := Parse(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(hosts) != 3 {
		t.Fatalf("want 3 hosts (wildcard skipped), got %d: %+v", len(hosts), hosts)
	}

	byAlias := map[string]ImportedHost{}
	for _, h := range hosts {
		byAlias[h.Alias] = h
	}
	web := byAlias["web"]
	if web.Host != "web.example.com" || web.Port != 2222 || web.User != "deploy" {
		t.Fatalf("web parsed wrong: %+v", web)
	}
	if web.KeyPath == "" || web.KeyPath[0] == '~' {
		t.Fatalf("IdentityFile should be expanded: %q", web.KeyPath)
	}
	if byAlias["internal"].ProxyJump != "bastion" {
		t.Fatalf("ProxyJump lost: %+v", byAlias["internal"])
	}

	nodes, warnings := BuildNodes(hosts, "parent1")
	if len(warnings) != 0 {
		t.Fatalf("unexpected warnings: %v", warnings)
	}
	nodeByName := map[string]int{}
	for i, n := range nodes {
		if n.ParentID != "parent1" || n.ID == "" {
			t.Fatalf("node %s: bad parent or missing id", n.Name)
		}
		nodeByName[n.Name] = i
	}
	internal := nodes[nodeByName["internal"]]
	bastion := nodes[nodeByName["bastion"]]
	if internal.Config.JumpNodeID != bastion.ID {
		t.Fatalf("jump link not resolved: %q != %q", internal.Config.JumpNodeID, bastion.ID)
	}
	if nodes[nodeByName["web"]].Config.AuthType != "key" {
		t.Fatal("web should use key auth")
	}
	if bastion.Config.AuthType != "agent" {
		t.Fatal("bastion without key should default to agent auth")
	}
}

func TestParseUnresolvableJump(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config")
	os.WriteFile(path, []byte("Host a\n  HostName a.example.com\n  ProxyJump nowhere\n"), 0o600)
	hosts, err := Parse(path)
	if err != nil {
		t.Fatal(err)
	}
	nodes, warnings := BuildNodes(hosts, "")
	if len(warnings) != 1 {
		t.Fatalf("want 1 warning, got %v", warnings)
	}
	if nodes[0].Config.JumpNodeID != "" {
		t.Fatal("unresolvable jump must be dropped")
	}
}
