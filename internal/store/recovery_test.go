package store

import (
	"errors"
	"path/filepath"
	"testing"
)

func TestRecoverySurvivesReopenAndProtectsNodeDeletion(t *testing.T) {
	path := filepath.Join(t.TempDir(), "recovery.db")
	st, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	n := &Node{Kind: "connection", Name: "host", Config: ConnConfig{Host: "example", User: "operator", PersistentTerminal: true}}
	if err = st.CreateNode(n); err != nil {
		t.Fatal(err)
	}
	rec, err := st.CreateRecovery(n.ID, n.Name, "operator@example:22", "route")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = st.CreateRecovery(n.ID, n.Name, "target", "route"); err == nil {
		t.Fatal("allowed duplicate identity for one host")
	}
	if _, err = st.DeleteSubtree(n.ID); !errors.Is(err, ErrRecoveryExists) {
		t.Fatal(err)
	}
	st.Close()
	st, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	got, err := st.RecoveryByNode(n.ID)
	if err != nil || got.ID != rec.ID || got.RouteKey != "route" {
		t.Fatal(got, err)
	}
	node, err := st.GetNode(n.ID)
	if err != nil || !node.Config.PersistentTerminal {
		t.Fatal(node, err)
	}
	if err = st.DeleteRecovery(rec.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = st.DeleteSubtree(n.ID); err != nil {
		t.Fatal(err)
	}
}
