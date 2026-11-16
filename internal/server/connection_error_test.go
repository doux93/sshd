package server

import (
	"errors"
	"fmt"
	"net"
	"os"
	"syscall"
	"testing"
)

func TestDialErrorHint(t *testing.T) {
	for _, tc := range []struct {
		name string
		err  error
		code string
	}{
		{"dns", &net.DNSError{Err: "not found", Name: "invalid"}, "dns"},
		{"timeout", &net.OpError{Op: "dial", Net: "tcp", Err: os.ErrDeadlineExceeded}, "timeout"},
		{"refused", fmt.Errorf("dial: %w", syscall.ECONNREFUSED), "refused"},
		{"key", &os.PathError{Op: "open", Path: "key", Err: os.ErrNotExist}, "key_file"},
		{"auth", errors.New("ssh: handshake failed: unable to authenticate, attempted methods [none password]"), "authentication"},
		{"locked", errors.New("vault is locked"), "vault_locked"},
		{"agent", errors.New("SSH agent unavailable"), "agent"},
		{"other", errors.New("unexpected EOF"), "connection"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			code, hint := dialErrorHint(tc.err)
			if code != tc.code || hint == "" {
				t.Fatalf("%q %q", code, hint)
			}
		})
	}
}
