package connect

import (
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"path/filepath"
	"testing"

	"golang.org/x/crypto/ssh"

	"sshx/internal/store"
	"sshx/internal/vault"
)

func testKey(t *testing.T) ssh.PublicKey {
	t.Helper()
	pub, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	key, err := ssh.NewPublicKey(pub)
	if err != nil {
		t.Fatal(err)
	}
	return key
}

func testService(t *testing.T) *Service {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	return NewService(st, vault.Memory{})
}

func TestTOFUUnknownThenAccepted(t *testing.T) {
	svc := testService(t)
	key := testKey(t)
	fp := ssh.FingerprintSHA256(key)

	// First contact: must prompt.
	err := svc.verifier(nil)("example.com:22", key)
	var prompt *HostKeyPromptError
	if !errors.As(err, &prompt) {
		t.Fatalf("want HostKeyPromptError, got %v", err)
	}
	if prompt.Changed || prompt.Fingerprint != fp {
		t.Fatalf("bad prompt: %+v", prompt)
	}

	// User confirms: accepted and recorded.
	accepts := map[string]string{"example.com:22": fp}
	if err := svc.verifier(accepts)("example.com:22", key); err != nil {
		t.Fatalf("accept failed: %v", err)
	}

	// Subsequent connections need no accepts.
	if err := svc.verifier(nil)("example.com:22", key); err != nil {
		t.Fatalf("recorded key rejected: %v", err)
	}
}

func TestTOFUChangedKey(t *testing.T) {
	svc := testService(t)
	key1 := testKey(t)
	key2 := testKey(t)

	accepts := map[string]string{"h:22": ssh.FingerprintSHA256(key1)}
	if err := svc.verifier(accepts)("h:22", key1); err != nil {
		t.Fatal(err)
	}

	// Different key for the same host: must prompt with changed=true.
	err := svc.verifier(nil)("h:22", key2)
	var prompt *HostKeyPromptError
	if !errors.As(err, &prompt) || !prompt.Changed {
		t.Fatalf("want changed-key prompt, got %v", err)
	}

	// Wrong fingerprint in accepts must not bypass the check.
	bad := map[string]string{"h:22": "SHA256:forged"}
	if err := svc.verifier(bad)("h:22", key2); err == nil {
		t.Fatal("forged accept fingerprint must fail")
	}

	// Correct confirmation replaces the stored key.
	good := map[string]string{"h:22": ssh.FingerprintSHA256(key2)}
	if err := svc.verifier(good)("h:22", key2); err != nil {
		t.Fatal(err)
	}
	if err := svc.verifier(nil)("h:22", key2); err != nil {
		t.Fatalf("rotated key rejected: %v", err)
	}
}
