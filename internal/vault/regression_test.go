package vault

import (
	"sync"
	"testing"
)

func TestReview_ConcurrentKeyRefresh(t *testing.T) {
	key := make([]byte, 32)
	v := NewEncrypted(nil, key)
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		for i := 0; i < 10000; i++ {
			v.SetKey(key)
		}
	}()
	go func() {
		defer wg.Done()
		for i := 0; i < 10000; i++ {
			v.newAEAD()
		}
	}()
	wg.Wait()
}

func TestVaultRejectsMovedCiphertext(t *testing.T) {
	st := newTestStore(t)
	v := NewEncrypted(st, freshKey(t))
	if err := v.Put("a:password", "secret"); err != nil {
		t.Fatal(err)
	}
	blob, err := st.GetSecret("a", "password")
	if err != nil {
		t.Fatal(err)
	}
	blob.NodeID = "b"
	if err := st.PutSecret(blob); err != nil {
		t.Fatal(err)
	}
	if _, err := v.Get("b:password"); err == nil {
		t.Fatal("accepted ciphertext moved to another ID")
	}
}
func TestVaultRejectsInvalidNonceWithoutPanic(t *testing.T) {
	st := newTestStore(t)
	v := NewEncrypted(st, freshKey(t))
	v.Put("a:password", "secret")
	blob, _ := st.GetSecret("a", "password")
	blob.Nonce = []byte{1}
	st.PutSecret(blob)
	if _, err := v.Get("a:password"); err == nil {
		t.Fatal("accepted invalid nonce")
	}
}
