package store

import (
	"database/sql"
	"path/filepath"
	"testing"
)

func openTestStore(t *testing.T) *Store {
	t.Helper()
	s, err := Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func TestNodeCRUD(t *testing.T) {
	s := openTestStore(t)

	folder := &Node{Kind: "folder", Name: "生产环境"}
	if err := s.CreateNode(folder); err != nil {
		t.Fatal(err)
	}
	conn := &Node{
		ParentID: folder.ID,
		Kind:     "connection",
		Name:     "web-1",
		Config: ConnConfig{
			Host: "10.0.0.1", Port: 22, User: "deploy",
			AuthType: "password", HasPassword: true,
		},
	}
	if err := s.CreateNode(conn); err != nil {
		t.Fatal(err)
	}

	got, err := s.GetNode(conn.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Config.Host != "10.0.0.1" || !got.Config.HasPassword {
		t.Fatalf("roundtrip mismatch: %+v", got.Config)
	}

	got.Name = "web-1-renamed"
	got.Config.Port = 2222
	if err := s.UpdateNode(got); err != nil {
		t.Fatal(err)
	}
	again, _ := s.GetNode(conn.ID)
	if again.Name != "web-1-renamed" || again.Config.Port != 2222 {
		t.Fatalf("update not persisted: %+v", again)
	}

	nodes, err := s.ListNodes()
	if err != nil {
		t.Fatal(err)
	}
	if len(nodes) != 2 {
		t.Fatalf("want 2 nodes, got %d", len(nodes))
	}
}

func TestDeleteSubtree(t *testing.T) {
	s := openTestStore(t)

	root := &Node{Kind: "folder", Name: "root"}
	s.CreateNode(root)
	child := &Node{ParentID: root.ID, Kind: "folder", Name: "child"}
	s.CreateNode(child)
	leaf := &Node{ParentID: child.ID, Kind: "connection", Name: "leaf"}
	s.CreateNode(leaf)
	sibling := &Node{Kind: "connection", Name: "untouched"}
	s.CreateNode(sibling)

	ids, err := s.DeleteSubtree(root.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(ids) != 3 {
		t.Fatalf("want 3 deleted ids, got %v", ids)
	}
	nodes, _ := s.ListNodes()
	if len(nodes) != 1 || nodes[0].ID != sibling.ID {
		t.Fatalf("sibling should survive, got %+v", nodes)
	}

	if _, err := s.DeleteSubtree("missing"); err != ErrNodeNotFound {
		t.Fatalf("want ErrNodeNotFound, got %v", err)
	}
}

func TestKnownHosts(t *testing.T) {
	s := openTestStore(t)

	if kh, _ := s.GetKnownHost("h:22", "ssh-ed25519"); kh != nil {
		t.Fatal("expected nil for unknown host")
	}
	err := s.PutKnownHost(&KnownHost{
		Host: "h:22", KeyType: "ssh-ed25519",
		Fingerprint: "SHA256:abc", PublicKey: "AAAA",
	})
	if err != nil {
		t.Fatal(err)
	}
	kh, err := s.GetKnownHost("h:22", "ssh-ed25519")
	if err != nil || kh == nil || kh.Fingerprint != "SHA256:abc" {
		t.Fatalf("got %+v, err %v", kh, err)
	}

	// Replace (key rotation after user confirms).
	s.PutKnownHost(&KnownHost{
		Host: "h:22", KeyType: "ssh-ed25519",
		Fingerprint: "SHA256:new", PublicKey: "BBBB",
	})
	kh, _ = s.GetKnownHost("h:22", "ssh-ed25519")
	if kh.PublicKey != "BBBB" {
		t.Fatalf("replace failed: %+v", kh)
	}
}

// schemaV1 is the frozen v1 schema, copied verbatim from the first release.
// It must NOT reference the migrations slice: the bug this guards against is
// new tables being appended to migration 1, which would make a slice-derived
// fixture already contain them — the test could then never fail.
const schemaV1 = `
	CREATE TABLE nodes (
		id         TEXT PRIMARY KEY,
		parent_id  TEXT NOT NULL DEFAULT '',
		kind       TEXT NOT NULL CHECK (kind IN ('folder','connection')),
		name       TEXT NOT NULL,
		sort_order INTEGER NOT NULL DEFAULT 0,
		config     TEXT NOT NULL DEFAULT '{}',
		created_at INTEGER NOT NULL,
		updated_at INTEGER NOT NULL
	);
	CREATE INDEX idx_nodes_parent ON nodes(parent_id);

	CREATE TABLE known_hosts (
		host        TEXT NOT NULL,
		key_type    TEXT NOT NULL,
		fingerprint TEXT NOT NULL,
		public_key  TEXT NOT NULL,
		first_seen  INTEGER NOT NULL,
		PRIMARY KEY (host, key_type)
	);

	CREATE TABLE settings (
		key   TEXT PRIMARY KEY,
		value TEXT NOT NULL
	);
`

// A database created by an earlier version must gain later tables when
// reopened, without losing existing rows. Regression test: audit_log and
// alerts_fired were once appended to migration 1, so databases already at
// user_version = 1 never received them and every audit write failed with
// "no such table" — invisible to tests that only open fresh databases.
func TestMigrationUpgradesExistingDatabase(t *testing.T) {
	path := filepath.Join(t.TempDir(), "old.db")

	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(schemaV1); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec("PRAGMA user_version = 1"); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(
		`INSERT INTO nodes (id, parent_id, kind, name, sort_order, config, created_at, updated_at)
		 VALUES ('keep-me', '', 'connection', '既有连接', 0, '{"host":"10.0.0.9"}', 1, 1)`,
	); err != nil {
		t.Fatal(err)
	}
	db.Close()

	// Reopening with the current code must migrate it forward.
	s, err := Open(path)
	if err != nil {
		t.Fatalf("reopen old database: %v", err)
	}
	defer s.Close()

	// The pre-existing row must survive the upgrade.
	nodes, err := s.ListNodes()
	if err != nil {
		t.Fatal(err)
	}
	if len(nodes) != 1 || nodes[0].Name != "既有连接" {
		t.Fatalf("existing node lost during migration: %+v", nodes)
	}

	// Tables added by later migrations must now exist and be usable.
	if err := s.InsertAudit("human", "run_command", map[string]any{"cmd": "uptime"}); err != nil {
		t.Fatalf("audit_log missing after upgrade: %v", err)
	}
	var n int
	if err := s.db.QueryRow("SELECT COUNT(*) FROM alerts_fired").Scan(&n); err != nil {
		t.Fatalf("alerts_fired missing after upgrade: %v", err)
	}
	// Migration 3 (M8 long-running mode) must also land on existing DBs.
	if _, err := s.db.Exec(
		`INSERT INTO auth_config (id, verifier, verifier_salt, vault_salt, argon_time, argon_memory_kib, argon_threads, created_at, updated_at)
		 VALUES (1, x'', x'', x'', 1, 1, 1, 1, 1)`,
	); err != nil {
		t.Fatalf("auth_config not usable after upgrade: %v", err)
	}
}
