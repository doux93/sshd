package vault

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"errors"
	"fmt"
	"io"
	"sync"

	"sshx/internal/store"
)

// Encrypted is an AES-256-GCM vault backed by SQLite (the secret_blobs
// table). The wrap key is supplied externally — typically derived from
// the master password via argon2id by auth.Password.
//
// Wire format per row:
//
//	ciphertext = AES-256-GCM(plaintext)
//	nonce      = 12 random bytes
//	aad        = node_id || ":" || kind   (binds ciphertext to its key)
//
// Both node_id and kind also live in cleartext columns — aad is the
// integrity-binding check, not a confidentiality boundary.
type Encrypted struct {
	mu      sync.RWMutex
	store   *store.Store
	wrapKey []byte // 32 bytes; nil = vault is locked
}

// NewEncrypted constructs a vault. wrapKey must be 32 bytes (or empty,
// in which case Put/Get return ErrLocked).
func NewEncrypted(s *store.Store, wrapKey []byte) *Encrypted {
	if len(wrapKey) != 0 && len(wrapKey) != 32 {
		panic(fmt.Sprintf("vault: wrapKey must be 32 bytes, got %d", len(wrapKey)))
	}
	return &Encrypted{store: s, wrapKey: append([]byte(nil), wrapKey...)}
}

// ErrLocked is returned when the vault has no wrap key (process started
// before the user logged in). Call sites can either surface "vault not
// ready" or fall back to the user's other storage. Matches Keyring's
// missing-key behavior closely enough for call sites that just retry.
var ErrLocked = errors.New("vault: locked")

// SetKey installs (or clears) the wrap key. Pass nil to re-lock.
func (e *Encrypted) SetKey(key []byte) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if key == nil {
		e.wrapKey = nil
		return
	}
	if len(key) != 32 {
		panic(fmt.Sprintf("vault: wrapKey must be 32 bytes, got %d", len(key)))
	}
	e.wrapKey = append([]byte(nil), key...)
}

// Locked reports whether the vault has a wrap key installed.
func (e *Encrypted) Locked() bool { e.mu.RLock(); defer e.mu.RUnlock(); return len(e.wrapKey) == 0 }

func (e *Encrypted) newAEAD() (cipher.AEAD, error) {
	e.mu.RLock()
	defer e.mu.RUnlock()
	if len(e.wrapKey) != 32 {
		return nil, ErrLocked
	}
	block, err := aes.NewCipher(e.wrapKey)
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(block)
}

// Put encrypts and stores secret under id. id is split on the last ':'
// to derive (node_id, kind); the convention matches vault.PasswordKey
// ("<nodeID>:password") and vault.PassphraseKey ("<nodeID>:passphrase").
func (e *Encrypted) Put(id, secret string) error {
	aead, err := e.newAEAD()
	if err != nil {
		return err
	}
	nodeID, kind, err := splitID(id)
	if err != nil {
		return err
	}
	nonce := make([]byte, aead.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return err
	}
	aad := []byte(id) // node_id || ":" || kind, exactly equal to id
	ct := aead.Seal(nil, nonce, []byte(secret), aad)
	return e.store.PutSecret(&store.SecretBlob{
		NodeID:     nodeID,
		Kind:       kind,
		Ciphertext: ct,
		Nonce:      nonce,
		AAD:        aad,
	})
}

// Get decrypts the secret stored under id. Returns ErrNotFound if
// absent, ErrLocked if the vault has no wrap key.
func (e *Encrypted) Get(id string) (string, error) {
	aead, err := e.newAEAD()
	if err != nil {
		return "", err
	}
	nodeID, kind, err := splitID(id)
	if err != nil {
		return "", err
	}
	blob, err := e.store.GetSecret(nodeID, kind)
	if errors.Is(err, store.ErrSecretNotFound) {
		return "", ErrNotFound
	}
	if err != nil {
		return "", err
	}
	if len(blob.Nonce) != aead.NonceSize() {
		return "", errors.New("vault: invalid nonce")
	}
	pt, err := aead.Open(nil, blob.Nonce, blob.Ciphertext, []byte(id))
	if err != nil {
		// Most likely the wrap key changed (e.g. wrong password after a
		// bad reload). Surface as a recognizable error rather than a
		// generic crypto failure.
		return "", fmt.Errorf("vault: decrypt failed: %w", err)
	}
	return string(pt), nil
}

// Delete removes the row. Missing row is not an error.
func (e *Encrypted) Delete(id string) error {
	nodeID, kind, err := splitID(id)
	if err != nil {
		return err
	}
	return e.store.DeleteSecret(nodeID, kind)
}

// splitID peels "nodeID:kind" apart. The store schema is keyed by
// (nodeID, kind) directly; the id string is the on-the-wire form used
// by Vault callers.
func splitID(id string) (nodeID, kind string, err error) {
	// Look from the right so node ids that happen to contain ':' (none
	// in current usage, but be safe) still resolve.
	i := lastColon(id)
	if i < 0 {
		return "", "", fmt.Errorf("vault: id %q must be of the form nodeID:kind", id)
	}
	return id[:i], id[i+1:], nil
}

func lastColon(s string) int {
	for i := len(s) - 1; i >= 0; i-- {
		if s[i] == ':' {
			return i
		}
	}
	return -1
}

var _ Vault = (*Encrypted)(nil)
