package connect

import (
	"context"
	"sshx/internal/sshclient"
	"sshx/internal/store"
)

// DialDraft uses unsaved settings and optional secret overrides without writing
// a node or changing its credentials. Explicitly accepted host keys are retained.
func (s *Service) DialDraft(ctx context.Context, node *store.Node, password, passphrase *string, accepts map[string]string) (*sshclient.Client, error) {
	copyNode := *node
	if password != nil {
		copyNode.Config.HasPassword = false
	}
	if passphrase != nil {
		copyNode.Config.HasPassphrase = false
	}
	cfg, err := s.resolveConfig(&copyNode, accepts)
	if err != nil {
		return nil, err
	}
	if password != nil && (node.Config.AuthType == "password" || node.Config.AuthType == "") {
		cfg.Password = *password
	}
	if passphrase != nil && node.Config.AuthType == "key" {
		cfg.KeyPassphrase = *passphrase
	}
	if node.Config.JumpNodeID == "" {
		return sshclient.Dial(ctx, cfg)
	}
	jump, err := s.store.GetNode(node.Config.JumpNodeID)
	if err != nil {
		return nil, err
	}
	via, err := s.dialNodeChain(ctx, jump, accepts, 0)
	if err != nil {
		return nil, err
	}
	client, err := sshclient.DialThrough(ctx, via, cfg)
	if err != nil {
		via.Close()
		return nil, err
	}
	client.ChainCloser(via)
	return client, nil
}
