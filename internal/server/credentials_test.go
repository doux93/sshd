package server

import (
	"errors"
	"net/http/httptest"
	"sshx/internal/store"
	"sshx/internal/vault"
	"strings"
	"testing"
)

type failingVault struct {
	data vault.Memory
	fail string
}

func (v *failingVault) Get(k string) (string, error) { return v.data.Get(k) }
func (v *failingVault) Delete(k string) error        { return v.data.Delete(k) }
func (v *failingVault) Put(k, value string) error {
	if value == v.fail {
		return errors.New("credential store failure")
	}
	return v.data.Put(k, value)
}
func TestNodeUpdateRollsBackSecretsAndMetadata(t *testing.T) {
	s, sid := reviewPasswordServer(t)
	v := &failingVault{data: vault.Memory{}, fail: "fail-value"}
	s.vault = v
	node := &store.Node{Kind: "connection", Name: "original", Config: store.ConnConfig{HasPassword: true, HasPassphrase: true}}
	if err := s.store.CreateNode(node); err != nil {
		t.Fatal(err)
	}
	v.data[vault.PasswordKey(node.ID)] = "old-password"
	v.data[vault.PassphraseKey(node.ID)] = "old-passphrase"
	req := httptest.NewRequest("PUT", "/api/nodes/"+node.ID, strings.NewReader(`{"name":"changed","config":{},"password":"new-password","passphrase":"fail-value"}`))
	req.Header.Set("X-Auth-Token", sid)
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, req)
	if w.Code != 500 {
		t.Fatal(w.Code)
	}
	got, _ := s.store.GetNode(node.ID)
	if got.Name != "original" {
		t.Fatal("metadata not rolled back")
	}
	if v.data[vault.PasswordKey(node.ID)] != "old-password" || v.data[vault.PassphraseKey(node.ID)] != "old-passphrase" {
		t.Fatal("partial credential update")
	}
}
func TestNodeCreateFailureLeavesNoPhantomNode(t *testing.T) {
	s, sid := reviewPasswordServer(t)
	s.vault = &failingVault{data: vault.Memory{}, fail: "fail-value"}
	req := httptest.NewRequest("POST", "/api/nodes", strings.NewReader(`{"kind":"connection","name":"new","config":{},"password":"fail-value"}`))
	req.Header.Set("X-Auth-Token", sid)
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, req)
	if w.Code != 500 {
		t.Fatal(w.Code)
	}
	nodes, _ := s.store.ListNodes()
	if len(nodes) != 0 {
		t.Fatal("phantom node left after failure")
	}
}
