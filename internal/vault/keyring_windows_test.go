//go:build windows

package vault

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"os"
	"testing"
)

func TestWindowsCredentialManagerRoundTrip(t *testing.T) {
	if os.Getenv("SSHX_TEST_SYSTEM_KEYRING") != "1" {
		t.Skip("set SSHX_TEST_SYSTEM_KEYRING=1 to exercise Windows Credential Manager")
	}
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		t.Fatal(err)
	}
	id := "ci-test-" + hex.EncodeToString(b)
	v := Keyring{}
	t.Cleanup(func() {
		if err := v.Delete(id); err != nil {
			t.Errorf("cleanup credential: %v", err)
		}
	})
	if err := v.Put(id, "test-only-first"); err != nil {
		t.Fatal(err)
	}
	if got, err := v.Get(id); err != nil || got != "test-only-first" {
		t.Fatal("roundtrip", err)
	}
	if err := v.Put(id, "test-only-updated"); err != nil {
		t.Fatal(err)
	}
	if got, err := v.Get(id); err != nil || got != "test-only-updated" {
		t.Fatal("update", err)
	}
	if err := v.Delete(id); err != nil {
		t.Fatal(err)
	}
	if _, err := v.Get(id); !errors.Is(err, ErrNotFound) {
		t.Fatal("deleted credential remains", err)
	}
}
