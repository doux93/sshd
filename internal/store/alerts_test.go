package store

import (
	"database/sql"
	"path/filepath"
	"testing"
)

func TestAlertHistoryPersistsAcknowledgmentAndPagination(t *testing.T) {
	path := filepath.Join(t.TempDir(), "history.db")
	st, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	var last int64
	for i := 0; i < 1005; i++ {
		last, err = st.AppendAlert("host", "label", "warning", "message")
		if err != nil {
			t.Fatal(err)
		}
	}
	if ok, err := st.AcknowledgeAlert(last); err != nil || !ok {
		t.Fatal(ok, err)
	}
	st.Close()
	st, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	first, err := st.ListAlerts("host", 0, 100)
	if err != nil || len(first) != 100 || first[0].AcknowledgedAt == 0 {
		t.Fatal(first, err)
	}
	all := len(first)
	cursor := first[len(first)-1].ID
	for {
		page, err := st.ListAlerts("host", cursor, 200)
		if err != nil {
			t.Fatal(err)
		}
		if len(page) == 0 {
			break
		}
		if page[0].ID >= cursor {
			t.Fatal("duplicate cursor page")
		}
		all += len(page)
		cursor = page[len(page)-1].ID
	}
	if all != 1000 {
		t.Fatalf("retained %d, want 1000", all)
	}
	other, err := st.ListAlerts("other", 0, 100)
	if err != nil || len(other) != 0 {
		t.Fatal(other, err)
	}
}

func TestAlertHistoryMigrationPreservesExistingConfiguration(t *testing.T) {
	path := filepath.Join(t.TempDir(), "legacy.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	for _, migration := range migrations[:4] {
		if _, err := db.Exec(migration); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := db.Exec("PRAGMA user_version=4"); err != nil {
		t.Fatal(err)
	}
	legacy := &Store{db: db}
	node := &Node{Kind: "connection", Name: "existing", Config: ConnConfig{Host: "production", HasPassword: true}}
	if err := legacy.CreateNode(node); err != nil {
		t.Fatal(err)
	}
	cfg := &AuthConfig{Verifier: []byte("verifier"), VerifierSalt: []byte("auth-salt"), VaultSalt: []byte("vault-salt"), ArgonTime: 1, ArgonMemoryKiB: 65536, ArgonThreads: 2}
	if err := legacy.SaveAuthConfig(cfg); err != nil {
		t.Fatal(err)
	}
	legacy.Close()
	updated, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer updated.Close()
	got, err := updated.GetNode(node.ID)
	if err != nil || got.Name != "existing" || !got.Config.HasPassword {
		t.Fatal(got, err)
	}
	auth, err := updated.LoadAuthConfig()
	if err != nil || string(auth.Verifier) != "verifier" || string(auth.VaultSalt) != "vault-salt" {
		t.Fatal(auth, err)
	}
	if _, err := updated.AppendAlert(node.ID, node.Name, "warning", "sample"); err != nil {
		t.Fatal(err)
	}
}
