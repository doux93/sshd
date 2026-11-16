// Package store persists configuration in SQLite: the connection tree,
// known host keys, settings and encrypted vault envelopes. Node configs never
// contain plaintext credentials.
package store

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"

	_ "modernc.org/sqlite" // pure-Go driver, no CGO
)

// migrations run in order; user_version tracks progress.
var migrations = []string{
	`
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
	`,
	// Migration 2 (M5-M7): audit log and alert de-duplication.
	//
	// These MUST be a separate entry, not additions to migration 1. Databases
	// created before they existed already sit at user_version = 1, so the
	// loop below would skip them entirely and the tables would never appear —
	// only brand-new databases would have them, which is exactly the kind of
	// bug that hides during local testing.
	`
	CREATE TABLE audit_log (
		id      INTEGER PRIMARY KEY AUTOINCREMENT,
		ts      INTEGER NOT NULL,
		actor   TEXT NOT NULL,           -- 'human' | 'ai' | 'mcp'
		action  TEXT NOT NULL,           -- tool name, e.g. 'run_command'
		detail  TEXT NOT NULL DEFAULT '{}' -- JSON: command, exit code, etc.
	);
	CREATE INDEX idx_audit_ts ON audit_log(ts);

	CREATE TABLE alerts_fired (
		id          INTEGER PRIMARY KEY AUTOINCREMENT,
		node_id     TEXT NOT NULL,
		metric      TEXT NOT NULL,
		op          TEXT NOT NULL,
		threshold   REAL NOT NULL,
		ts          INTEGER NOT NULL,
		notified_at INTEGER
	);
	CREATE INDEX idx_alerts_node ON alerts_fired(node_id);
	`,
	// Migration 3 (M8 long-running mode): master-password auth, encrypted
	// vault, opaque session cookies. The auth_config row is a singleton
	// (CHECK id=1) so two processes booting concurrently cannot both think
	// they're first-run.
	`
	CREATE TABLE auth_config (
		id                 INTEGER PRIMARY KEY CHECK (id = 1),
		verifier           BLOB    NOT NULL,
		verifier_salt      BLOB    NOT NULL,
		vault_salt         BLOB    NOT NULL,
		argon_time         INTEGER NOT NULL,
		argon_memory_kib   INTEGER NOT NULL,
		argon_threads      INTEGER NOT NULL,
		created_at         INTEGER NOT NULL,
		updated_at         INTEGER NOT NULL
	);

	CREATE TABLE sessions (
		id            TEXT    PRIMARY KEY,
		created_at    INTEGER NOT NULL,
		expires_at    INTEGER NOT NULL,
		last_seen_at  INTEGER NOT NULL,
		user_agent    TEXT    NOT NULL DEFAULT ''
	);
	CREATE INDEX sessions_expires_at_idx ON sessions(expires_at);

	CREATE TABLE secret_blobs (
		node_id    TEXT    NOT NULL,
		kind       TEXT    NOT NULL,
		ciphertext BLOB    NOT NULL,
		nonce      BLOB    NOT NULL,
		aad        BLOB    NOT NULL,
		created_at INTEGER NOT NULL,
		updated_at INTEGER NOT NULL,
			PRIMARY KEY (node_id, kind)
	);
	`,
	// Migration 4 (M8 Web Push): VAPID key on auth_config + push_subscriptions.
	//
	// VAPID keys are generated on first use in remote mode and pinned to the
	// auth_config singleton. push_subscriptions is keyed by an internal id
	// (not the endpoint URL) so the same operator can register several
	// devices without colliding on long URLs.
	`
	ALTER TABLE auth_config ADD COLUMN vapid_private BLOB NOT NULL DEFAULT X'';
	ALTER TABLE auth_config ADD COLUMN vapid_public  BLOB NOT NULL DEFAULT X'';

	CREATE TABLE push_subscriptions (
		id          TEXT    PRIMARY KEY,
		created_at  INTEGER NOT NULL,
		updated_at  INTEGER NOT NULL,
		last_used_at INTEGER NOT NULL DEFAULT 0,
		endpoint    TEXT    NOT NULL,
		p256dh      TEXT    NOT NULL,
		auth        TEXT    NOT NULL,
		user_agent  TEXT    NOT NULL DEFAULT '',
		revoked_at  INTEGER NOT NULL DEFAULT 0
	);
	CREATE INDEX idx_push_subs_revoked ON push_subscriptions(revoked_at);
	`,
	// Migration 5: bounded, durable operator-facing alert history.
	`
	CREATE TABLE alert_history (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		node_id TEXT NOT NULL, node_label TEXT NOT NULL,
		level TEXT NOT NULL, message TEXT NOT NULL,
		ts INTEGER NOT NULL, acknowledged_at INTEGER NOT NULL DEFAULT 0
	);
	CREATE INDEX idx_alert_history_node ON alert_history(node_id, id);
	`,
	// Migration 6: durable identities for opt-in remote tmux terminals.
	`CREATE TABLE terminal_recovery (
 id TEXT PRIMARY KEY, node_id TEXT NOT NULL UNIQUE REFERENCES nodes(id) ON DELETE RESTRICT,
 label TEXT NOT NULL, target TEXT NOT NULL, route_key TEXT NOT NULL, created_at INTEGER NOT NULL
 );`,
}

type Store struct {
	db *sql.DB
}

// DefaultPath returns the per-user database location, creating the directory.
func DefaultPath() (string, error) {
	base, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	dir := filepath.Join(base, "sshx")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	return filepath.Join(dir, "sshx.db"), nil
}

// Open opens (creating if needed) the database and applies migrations.
func Open(path string) (*Store, error) {
	db, err := sql.Open("sqlite", path+"?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)&_pragma=foreign_keys(ON)")
	if err != nil {
		return nil, err
	}
	// modernc.org/sqlite serializes writes; a single connection avoids
	// SQLITE_BUSY between concurrent API handlers.
	db.SetMaxOpenConns(1)

	s := &Store{db: db}
	if err := s.migrate(); err != nil {
		db.Close()
		return nil, err
	}
	return s, nil
}

func (s *Store) migrate() error {
	var version int
	if err := s.db.QueryRow("PRAGMA user_version").Scan(&version); err != nil {
		return err
	}
	for i := version; i < len(migrations); i++ {
		tx, err := s.db.Begin()
		if err != nil {
			return err
		}
		if _, err = tx.Exec(migrations[i]); err == nil {
			_, err = tx.Exec(fmt.Sprintf("PRAGMA user_version = %d", i+1))
		}
		if err != nil {
			tx.Rollback()
			return fmt.Errorf("migration %d: %w", i+1, err)
		}
		if err = tx.Commit(); err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) Close() error { return s.db.Close() }

// DB exposes the underlying *sql.DB. Reserved for callers that need to
// run ad-hoc statements outside the typed helpers — e.g. tests that
// need to advance the clock. Avoid in production code; add typed
// methods on Store instead.
func (s *Store) DB() *sql.DB { return s.db }
