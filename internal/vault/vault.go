// Package vault is the credential storage boundary. Connection nodes store
// only opaque references; secrets live behind this interface.
//
// Implementations:
//   - Keyring (desktop mode): OS keychain via zalando/go-keyring
//     (Windows Credential Manager / macOS Keychain / Secret Service).
//   - M6 adds an encrypted-SQLite vault for VPS mode (argon2id + AES-GCM).
package vault

import "errors"

// ErrNotFound is returned when no secret exists for the given id.
var ErrNotFound = errors.New("vault: secret not found")

// Vault stores small secrets (passwords, key passphrases) by id.
// Ids are derived from node ids, e.g. "<nodeID>:password".
type Vault interface {
	Put(id string, secret string) error
	Get(id string) (string, error) // ErrNotFound if absent
	Delete(id string) error        // deleting a missing id is not an error
}

// Key naming helpers keep id construction in one place.
func PasswordKey(nodeID string) string   { return nodeID + ":password" }
func PassphraseKey(nodeID string) string { return nodeID + ":passphrase" }
