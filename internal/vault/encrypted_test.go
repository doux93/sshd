package vault

import (
	"crypto/rand"
	"errors"
	"path/filepath"
	"testing"

	"sshx/internal/store"
)

func newTestStore(t *testing.T) *store.Store {
	t.Helper()
	dir := t.TempDir()
	st, err := store.Open(filepath.Join(dir, "t.db"))
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { st.Close() })
	return st
}

// freshKey returns a deterministic-looking 32-byte wrap key for tests.
func freshKey(t *testing.T) []byte {
	t.Helper()
	k := make([]byte, 32)
	if _, err := rand.Read(k); err != nil {
		t.Fatalf("rand: %v", err)
	}
	return k
}

func TestEncrypted_PutGetRoundTrip(t *testing.T) {
	st := newTestStore(t)
	v := NewEncrypted(st, freshKey(t))
	id := "nodeA:password"
	if err := v.Put(id, "p@ss w0rd"); err != nil {
		t.Fatalf("Put: %v", err)
	}
	got, err := v.Get(id)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got != "p@ss w0rd" {
		t.Fatalf("Get returned %q", got)
	}
}

func TestEncrypted_GetMissingReturnsErrNotFound(t *testing.T) {
	st := newTestStore(t)
	v := NewEncrypted(st, freshKey(t))
	if _, err := v.Get("missing:password"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("want ErrNotFound, got %v", err)
	}
}

func TestEncrypted_LockedVaultRejectsOperations(t *testing.T) {
	st := newTestStore(t)
	v := NewEncrypted(st, nil)
	if !v.Locked() {
		t.Fatal("vault should be locked with nil key")
	}
	if err := v.Put("x:y", "z"); !errors.Is(err, ErrLocked) {
		t.Fatalf("Put on locked vault: want ErrLocked, got %v", err)
	}
	if _, err := v.Get("x:y"); !errors.Is(err, ErrLocked) {
		t.Fatalf("Get on locked vault: want ErrLocked, got %v", err)
	}
}

func TestEncrypted_DeleteMissingIsNotAnError(t *testing.T) {
	st := newTestStore(t)
	v := NewEncrypted(st, freshKey(t))
	if err := v.Delete("never:existed"); err != nil {
		t.Fatalf("Delete missing: %v", err)
	}
}

func TestEncrypted_WrongKeyFailsOpen(t *testing.T) {
	st := newTestStore(t)
	v := NewEncrypted(st, freshKey(t))
	if err := v.Put("n:k", "secret"); err != nil {
		t.Fatalf("Put: %v", err)
	}
	v2 := NewEncrypted(st, freshKey(t)) // different key, same DB
	if _, err := v2.Get("n:k"); err == nil {
		t.Fatal("Get with wrong key should fail; got nil")
	}
}

func TestEncrypted_KeyCompositionRespectsAAD(t *testing.T) {
	st := newTestStore(t)
	v := NewEncrypted(st, freshKey(t))
	if err := v.Put("n:password", "pw"); err != nil {
		t.Fatalf("Put password: %v", err)
	}
	// Get must distinguish by the full id — re-keying under a different
	// kind should not collide. Since the store also keys by (node, kind)
	// this is implicitly tested, but verify it anyway.
	if err := v.Put("n:passphrase", "pp"); err != nil {
		t.Fatalf("Put passphrase: %v", err)
	}
	pw, _ := v.Get("n:password")
	pp, _ := v.Get("n:passphrase")
	if pw != "pw" || pp != "pp" {
		t.Fatalf("got pw=%q pp=%q", pw, pp)
	}
}
