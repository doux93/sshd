package vault

import (
	"errors"

	"github.com/zalando/go-keyring"
)

const service = "sshx"

// Keyring stores secrets in the OS keychain. Used in desktop/local mode.
type Keyring struct{}

var _ Vault = Keyring{}

func (Keyring) Put(id, secret string) error {
	return keyring.Set(service, id, secret)
}

func (Keyring) Get(id string) (string, error) {
	s, err := keyring.Get(service, id)
	if errors.Is(err, keyring.ErrNotFound) {
		return "", ErrNotFound
	}
	return s, err
}

func (Keyring) Delete(id string) error {
	err := keyring.Delete(service, id)
	if errors.Is(err, keyring.ErrNotFound) {
		return nil
	}
	return err
}

// Memory is an in-process vault for tests.
type Memory map[string]string

var _ Vault = Memory{}

func (m Memory) Put(id, secret string) error { m[id] = secret; return nil }
func (m Memory) Get(id string) (string, error) {
	s, ok := m[id]
	if !ok {
		return "", ErrNotFound
	}
	return s, nil
}
func (m Memory) Delete(id string) error { delete(m, id); return nil }
