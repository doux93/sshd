// Package connect orchestrates dialing: it resolves connection nodes to
// sshclient configs (pulling secrets from the vault), walks ProxyJump
// chains, and enforces host-key trust (TOFU) backed by the store and the
// user's OpenSSH known_hosts file.
package connect

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"

	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/knownhosts"

	"sshx/internal/sshclient"
	"sshx/internal/store"
	"sshx/internal/vault"
)

// HostKeyPromptError aborts a dial because the host key needs user
// confirmation. The API layer turns this into a 409 response; the client
// retries with the fingerprint in Accepts.
type HostKeyPromptError struct {
	Host        string `json:"host"` // "host:port"
	KeyType     string `json:"keyType"`
	Fingerprint string `json:"fingerprint"`
	Changed     bool   `json:"changed"` // true: key differs from the stored one (possible MITM)
}

func (e *HostKeyPromptError) Error() string {
	if e.Changed {
		return fmt.Sprintf("host key for %s CHANGED (fingerprint %s)", e.Host, e.Fingerprint)
	}
	return fmt.Sprintf("unknown host key for %s (fingerprint %s)", e.Host, e.Fingerprint)
}

// Service wires store + vault into dial operations.
type Service struct {
	store *store.Store
	vault vault.Vault
}

func NewService(st *store.Store, v vault.Vault) *Service {
	return &Service{store: st, vault: v}
}

// maxJumpDepth bounds ProxyJump chains (and breaks reference cycles).
const maxJumpDepth = 5

// DialNode connects to a stored connection node, following its jump chain.
// accepts maps "host:port" → fingerprint the user has just confirmed.
func (s *Service) DialNode(ctx context.Context, nodeID string, accepts map[string]string) (*sshclient.Client, *store.Node, error) {
	node, err := s.store.GetNode(nodeID)
	if err != nil {
		return nil, nil, err
	}
	if node.Kind != "connection" {
		return nil, nil, fmt.Errorf("node %q is not a connection", node.Name)
	}
	client, err := s.dialNodeChain(ctx, node, accepts, 0)
	if err != nil {
		return nil, nil, err
	}
	return client, node, nil
}

func (s *Service) dialNodeChain(ctx context.Context, node *store.Node, accepts map[string]string, depth int) (*sshclient.Client, error) {
	if depth >= maxJumpDepth {
		return nil, fmt.Errorf("jump chain too deep (max %d); check for cycles", maxJumpDepth)
	}
	cfg, err := s.resolveConfig(node, accepts)
	if err != nil {
		return nil, err
	}

	if node.Config.JumpNodeID == "" {
		return sshclient.Dial(ctx, cfg)
	}

	jumpNode, err := s.store.GetNode(node.Config.JumpNodeID)
	if err != nil {
		return nil, fmt.Errorf("jump host of %q: %w", node.Name, err)
	}
	via, err := s.dialNodeChain(ctx, jumpNode, accepts, depth+1)
	if err != nil {
		return nil, err
	}
	client, err := sshclient.DialThrough(ctx, via, cfg)
	if err != nil {
		via.Close()
		return nil, err
	}
	// The jump client must outlive the target client; tie their lifetimes.
	client.ChainCloser(via)
	return client, nil
}

// QuickSpec is an ad-hoc connection (not saved as a node).
type QuickSpec struct {
	Host     string
	Port     int
	User     string
	Password string
}

// DialQuick connects without a stored node, still enforcing TOFU.
func (s *Service) DialQuick(ctx context.Context, spec QuickSpec, accepts map[string]string) (*sshclient.Client, error) {
	return sshclient.Dial(ctx, sshclient.Config{
		Host:          spec.Host,
		Port:          spec.Port,
		User:          spec.User,
		Password:      spec.Password,
		VerifyHostKey: s.verifier(accepts),
	})
}

// resolveConfig builds the sshclient.Config for one node, fetching secrets
// from the vault as needed.
func (s *Service) resolveConfig(node *store.Node, accepts map[string]string) (sshclient.Config, error) {
	c := node.Config
	cfg := sshclient.Config{
		Host:          c.Host,
		Port:          c.Port,
		User:          c.User,
		VerifyHostKey: s.verifier(accepts),
		Proxy:         c.Proxy,
	}
	switch c.AuthType {
	case "agent":
		cfg.UseAgent = true
	case "key":
		cfg.KeyPath = c.KeyPath
		if c.HasPassphrase {
			pp, err := s.vault.Get(vault.PassphraseKey(node.ID))
			if err != nil && !errors.Is(err, vault.ErrNotFound) {
				return cfg, fmt.Errorf("vault passphrase for %q: %w", node.Name, err)
			}
			cfg.KeyPassphrase = pp
		}
	default: // "password"
		if c.HasPassword {
			pw, err := s.vault.Get(vault.PasswordKey(node.ID))
			if err != nil && !errors.Is(err, vault.ErrNotFound) {
				return cfg, fmt.Errorf("vault password for %q: %w", node.Name, err)
			}
			cfg.Password = pw
		}
	}
	return cfg, nil
}

// verifier implements TOFU:
//  1. trust keys recorded in our store;
//  2. trust keys validated by the user's OpenSSH known_hosts (read-only);
//  3. otherwise require explicit confirmation via accepts, then record.
func (s *Service) verifier(accepts map[string]string) sshclient.HostKeyVerifier {
	return func(hostport string, key ssh.PublicKey) error {
		fingerprint := ssh.FingerprintSHA256(key)
		wireKey := base64.StdEncoding.EncodeToString(key.Marshal())

		known, err := s.store.GetKnownHost(hostport, key.Type())
		if err != nil {
			return err
		}
		if known != nil && known.PublicKey == wireKey {
			return nil
		}
		changed := known != nil // recorded before, but the key differs

		if !changed && opensshKnownHostsValidates(hostport, key) {
			return nil
		}

		if accepts[hostport] == fingerprint {
			return s.store.PutKnownHost(&store.KnownHost{
				Host:        hostport,
				KeyType:     key.Type(),
				Fingerprint: fingerprint,
				PublicKey:   wireKey,
			})
		}
		return &HostKeyPromptError{
			Host:        hostport,
			KeyType:     key.Type(),
			Fingerprint: fingerprint,
			Changed:     changed,
		}
	}
}

// opensshKnownHostsValidates reports whether ~/.ssh/known_hosts already
// trusts this exact key. Any parse/mismatch/missing-file condition simply
// returns false — our own store and the TOFU prompt remain authoritative.
func opensshKnownHostsValidates(hostport string, key ssh.PublicKey) bool {
	home, err := os.UserHomeDir()
	if err != nil {
		return false
	}
	path := filepath.Join(home, ".ssh", "known_hosts")
	callback, err := knownhosts.New(path)
	if err != nil {
		return false
	}
	return callback(hostport, dummyAddr(hostport), key) == nil
}

// dummyAddr satisfies net.Addr for knownhosts, which only uses String().
type dummyAddr string

func (d dummyAddr) Network() string { return "tcp" }
func (d dummyAddr) String() string  { return string(d) }

var _ net.Addr = dummyAddr("")
