package store

import (
	"crypto/rand"
	"database/sql"
	"encoding/base64"
	"errors"
	"time"
)

// AuthConfig is the singleton row stored in auth_config (id=1).
// It is the material for verifying the master password at login time
// and (with vault_salt + the master password itself) for deriving the
// in-process wrap key that unseals secret_blobs.
type AuthConfig struct {
	Verifier       []byte
	VerifierSalt   []byte
	VaultSalt      []byte
	ArgonTime      uint32
	ArgonMemoryKiB uint32
	ArgonThreads   uint8
	VAPIDPrivate   []byte // EC P-256 scalar (32B); empty until M8 Web Push generates one
	VAPIDPublic    []byte // EC P-256 point, uncompressed (65B); base64url for the SPA
	CreatedAt      time.Time
	UpdatedAt      time.Time
}

// ErrAuthNotConfigured means the auth_config table is empty — first-run
// bootstrap hasn't happened yet.
var ErrAuthNotConfigured = errors.New("auth not configured")

// ErrSecretNotFound is returned when a (nodeID, kind) row is missing
// from secret_blobs. The vault package maps this to its own
// ErrNotFound to keep the package boundary clean.
var ErrSecretNotFound = errors.New("secret not found")

// ErrSessionNotFound is returned by lookup helpers when a session id is
// unknown or expired.
var ErrSessionNotFound = errors.New("session not found")

// LoadAuthConfig returns the singleton auth_config row, or
// ErrAuthNotConfigured if absent.
func (s *Store) LoadAuthConfig() (*AuthConfig, error) {
	row := s.db.QueryRow(`SELECT verifier, verifier_salt, vault_salt, argon_time,
		argon_memory_kib, argon_threads, vapid_private, vapid_public,
		created_at, updated_at
		FROM auth_config WHERE id = 1`)
	var c AuthConfig
	var created, updated int64
	err := row.Scan(&c.Verifier, &c.VerifierSalt, &c.VaultSalt,
		&c.ArgonTime, &c.ArgonMemoryKiB, &c.ArgonThreads,
		&c.VAPIDPrivate, &c.VAPIDPublic,
		&created, &updated)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrAuthNotConfigured
	}
	if err != nil {
		return nil, err
	}
	c.CreatedAt = time.UnixMilli(created)
	c.UpdatedAt = time.UnixMilli(updated)
	return &c, nil
}

// SaveAuthConfig inserts the singleton auth_config row. Fails if one
// already exists — first-run bootstrap is one-shot.
func (s *Store) SaveAuthConfig(c *AuthConfig) error {
	now := time.Now()
	c.CreatedAt = now
	c.UpdatedAt = now
	// Coerce nil to empty bytes. The VAPID columns are NOT NULL but
	// are filled lazily by push.WebPushNotifier on first delivery;
	// before that they stay zero-length.
	priv := c.VAPIDPrivate
	if priv == nil {
		priv = []byte{}
	}
	pub := c.VAPIDPublic
	if pub == nil {
		pub = []byte{}
	}
	_, err := s.db.Exec(`INSERT INTO auth_config
		(id, verifier, verifier_salt, vault_salt, argon_time, argon_memory_kib,
		 argon_threads, vapid_private, vapid_public, created_at, updated_at)
		VALUES (1, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		c.Verifier, c.VerifierSalt, c.VaultSalt,
		c.ArgonTime, c.ArgonMemoryKiB, c.ArgonThreads,
		priv, pub,
		now.UnixMilli(), now.UnixMilli())
	return err
}

// Session is one row of the sessions table.
type Session struct {
	ID         string
	CreatedAt  time.Time
	ExpiresAt  time.Time
	LastSeenAt time.Time
	UserAgent  string
}

// NewSessionID mints a 32-byte base64url session id (43 chars).
func NewSessionID() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

// CreateSession inserts a new session row.
func (s *Store) CreateSession(sess *Session) error {
	if sess.ID == "" {
		return errors.New("CreateSession: empty id")
	}
	_, err := s.db.Exec(`INSERT INTO sessions
		(id, created_at, expires_at, last_seen_at, user_agent)
		VALUES (?, ?, ?, ?, ?)`,
		sess.ID,
		sess.CreatedAt.UnixMilli(),
		sess.ExpiresAt.UnixMilli(),
		sess.LastSeenAt.UnixMilli(),
		sess.UserAgent)
	return err
}

// GetSession returns the session row by id. ErrSessionNotFound if absent
// or expired.
func (s *Store) GetSession(id string) (*Session, error) {
	row := s.db.QueryRow(`SELECT id, created_at, expires_at, last_seen_at, user_agent
		FROM sessions WHERE id = ?`, id)
	var sess Session
	var created, expires, lastSeen int64
	err := row.Scan(&sess.ID, &created, &expires, &lastSeen, &sess.UserAgent)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrSessionNotFound
	}
	if err != nil {
		return nil, err
	}
	now := time.Now()
	sess.CreatedAt = time.UnixMilli(created)
	sess.ExpiresAt = time.UnixMilli(expires)
	sess.LastSeenAt = time.UnixMilli(lastSeen)
	if !sess.ExpiresAt.After(now) {
		// Expired — best-effort cleanup, treat as not-found.
		_, _ = s.db.Exec(`DELETE FROM sessions WHERE id = ?`, id)
		return nil, ErrSessionNotFound
	}
	return &sess, nil
}

// TouchSession atomically validates and renews an unexpired session. Renewal
// occurs at half-life even for clients that make requests every day.
func (s *Store) TouchSession(id string) (*Session, error) {
	now := time.Now()
	row := s.db.QueryRow(`UPDATE sessions SET last_seen_at = ?,
   expires_at = CASE WHEN expires_at < ? THEN ? ELSE expires_at END
   WHERE id = ? AND expires_at > ?
   RETURNING id, created_at, expires_at, last_seen_at, user_agent`,
		now.UnixMilli(), now.Add(15*24*time.Hour).UnixMilli(),
		now.Add(30*24*time.Hour).UnixMilli(), id, now.UnixMilli())
	var sess Session
	var created, expires, seen int64
	err := row.Scan(&sess.ID, &created, &expires, &seen, &sess.UserAgent)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrSessionNotFound
	}
	if err != nil {
		return nil, err
	}
	sess.CreatedAt, sess.ExpiresAt, sess.LastSeenAt = time.UnixMilli(created), time.UnixMilli(expires), time.UnixMilli(seen)
	return &sess, nil
}

// DeleteSession removes a session row by id.
func (s *Store) DeleteSession(id string) error {
	_, err := s.db.Exec(`DELETE FROM sessions WHERE id = ?`, id)
	return err
}

// PurgeExpiredSessions removes sessions whose expires_at < now. Returns
// rows removed. Janitor-style helper.
func (s *Store) PurgeExpiredSessions() (int64, error) {
	res, err := s.db.Exec(`DELETE FROM sessions WHERE expires_at < ?`,
		time.Now().UnixMilli())
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

// SecretBlob is one row of secret_blobs.
type SecretBlob struct {
	NodeID     string
	Kind       string
	Ciphertext []byte
	Nonce      []byte
	AAD        []byte
	CreatedAt  time.Time
	UpdatedAt  time.Time
}

// PutSecret upserts a secret blob. Composite PK (node_id, kind).
func (s *Store) PutSecret(b *SecretBlob) error {
	now := time.Now()
	if b.CreatedAt.IsZero() {
		b.CreatedAt = now
	}
	b.UpdatedAt = now
	_, err := s.db.Exec(`INSERT INTO secret_blobs
		(node_id, kind, ciphertext, nonce, aad, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(node_id, kind) DO UPDATE SET
			ciphertext = excluded.ciphertext,
			nonce = excluded.nonce,
			aad = excluded.aad,
			updated_at = excluded.updated_at`,
		b.NodeID, b.Kind, b.Ciphertext, b.Nonce, b.AAD,
		b.CreatedAt.UnixMilli(), b.UpdatedAt.UnixMilli())
	return err
}

// GetSecret returns the ciphertext envelope for (nodeID, kind).
func (s *Store) GetSecret(nodeID, kind string) (*SecretBlob, error) {
	row := s.db.QueryRow(`SELECT node_id, kind, ciphertext, nonce, aad,
		created_at, updated_at FROM secret_blobs WHERE node_id = ? AND kind = ?`,
		nodeID, kind)
	var b SecretBlob
	var created, updated int64
	err := row.Scan(&b.NodeID, &b.Kind, &b.Ciphertext, &b.Nonce, &b.AAD,
		&created, &updated)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrSecretNotFound
	}
	if err != nil {
		return nil, err
	}
	b.CreatedAt = time.UnixMilli(created)
	b.UpdatedAt = time.UnixMilli(updated)
	return &b, nil
}

// DeleteSecret removes a secret blob by (nodeID, kind). Missing rows
// return nil — matches Vault interface contract.
func (s *Store) DeleteSecret(nodeID, kind string) error {
	_, err := s.db.Exec(`DELETE FROM secret_blobs WHERE node_id = ? AND kind = ?`,
		nodeID, kind)
	return err
}

// DeleteSecretsForNodes purges every secret for the given node ids.
// Used when nodes are deleted; matches the DeleteSubtree return shape.
func (s *Store) DeleteSecretsForNodes(nodeIDs []string) error {
	if len(nodeIDs) == 0 {
		return nil
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, id := range nodeIDs {
		if _, err := tx.Exec(`DELETE FROM secret_blobs WHERE node_id = ?`, id); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// UpdateVAPIDKeys patches the singleton auth_config row with new VAPID
// keys. Idempotent; called by the Web Push layer when it discovers the
// keys are missing on a remote-mode boot.
func (s *Store) UpdateVAPIDKeys(priv, pub []byte) error {
	now := time.Now().UnixMilli()
	_, err := s.db.Exec(`UPDATE auth_config SET vapid_private = ?, vapid_public = ?,
		updated_at = ? WHERE id = 1`, priv, pub, now)
	return err
}

// PushSubscription is one row of push_subscriptions — a single browser
// (or a single endpoint registered by a browser) that's allowed to
// receive Web Push from sshx.
type PushSubscription struct {
	ID         string
	Endpoint   string
	P256DH     string
	Auth       string
	UserAgent  string
	CreatedAt  time.Time
	UpdatedAt  time.Time
	LastUsedAt time.Time
	Revoked    bool
}

// NewPushSubscriptionID mints a 16-byte base64url id (22 chars).
func NewPushSubscriptionID() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

// CreatePushSubscription inserts a new row.
func (s *Store) CreatePushSubscription(sub *PushSubscription) error {
	if sub.ID == "" {
		return errors.New("CreatePushSubscription: empty id")
	}
	now := time.Now()
	sub.CreatedAt = now
	sub.UpdatedAt = now
	sub.LastUsedAt = time.Time{}
	_, err := s.db.Exec(`INSERT INTO push_subscriptions
		(id, created_at, updated_at, last_used_at, endpoint, p256dh, auth, user_agent, revoked_at)
		VALUES (?, ?, ?, 0, ?, ?, ?, ?, 0)`,
		sub.ID,
		now.UnixMilli(),
		now.UnixMilli(),
		sub.Endpoint, sub.P256DH, sub.Auth, sub.UserAgent)
	return err
}

// UpdatePushSubscription updates the endpoint / keys / user agent. The
// browser rotates the endpoint on rare occasions (e.g. Firefox profile
// reset) and we want to track the new one rather than minting a new id.
func (s *Store) UpdatePushSubscription(sub *PushSubscription) error {
	now := time.Now()
	sub.UpdatedAt = now
	_, err := s.db.Exec(`UPDATE push_subscriptions SET
		endpoint = ?, p256dh = ?, auth = ?, user_agent = ?, updated_at = ?,
		revoked_at = 0
		WHERE id = ?`,
		sub.Endpoint, sub.P256DH, sub.Auth, sub.UserAgent,
		now.UnixMilli(), sub.ID)
	return err
}

// GetPushSubscription fetches by id; ErrSessionNotFound if absent.
func (s *Store) GetPushSubscription(id string) (*PushSubscription, error) {
	row := s.db.QueryRow(`SELECT id, endpoint, p256dh, auth, user_agent,
		created_at, updated_at, last_used_at, revoked_at
		FROM push_subscriptions WHERE id = ?`, id)
	var sub PushSubscription
	var created, updated, lastUsed, revoked int64
	err := row.Scan(&sub.ID, &sub.Endpoint, &sub.P256DH, &sub.Auth,
		&sub.UserAgent, &created, &updated, &lastUsed, &revoked)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrSessionNotFound
	}
	if err != nil {
		return nil, err
	}
	sub.CreatedAt = time.UnixMilli(created)
	sub.UpdatedAt = time.UnixMilli(updated)
	sub.LastUsedAt = time.UnixMilli(lastUsed)
	sub.Revoked = revoked != 0
	return &sub, nil
}

// ListActivePushSubscriptions returns every non-revoked row. Revoked
// rows are kept (audit trail) but excluded from the delivery set.
func (s *Store) ListActivePushSubscriptions() ([]PushSubscription, error) {
	rows, err := s.db.Query(`SELECT id, endpoint, p256dh, auth, user_agent,
		created_at, updated_at, last_used_at, revoked_at
		FROM push_subscriptions WHERE revoked_at = 0`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []PushSubscription
	for rows.Next() {
		var sub PushSubscription
		var created, updated, lastUsed, revoked int64
		if err := rows.Scan(&sub.ID, &sub.Endpoint, &sub.P256DH, &sub.Auth,
			&sub.UserAgent, &created, &updated, &lastUsed, &revoked); err != nil {
			return nil, err
		}
		sub.CreatedAt = time.UnixMilli(created)
		sub.UpdatedAt = time.UnixMilli(updated)
		sub.LastUsedAt = time.UnixMilli(lastUsed)
		sub.Revoked = revoked != 0
		out = append(out, sub)
	}
	return out, rows.Err()
}

// ListAllPushSubscriptions returns every row including revoked. Used by
// the UI to render the device list ("3 devices, last seen 2 days ago").
func (s *Store) ListAllPushSubscriptions() ([]PushSubscription, error) {
	rows, err := s.db.Query(`SELECT id, endpoint, p256dh, auth, user_agent,
		created_at, updated_at, last_used_at, revoked_at
		FROM push_subscriptions ORDER BY created_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []PushSubscription
	for rows.Next() {
		var sub PushSubscription
		var created, updated, lastUsed, revoked int64
		if err := rows.Scan(&sub.ID, &sub.Endpoint, &sub.P256DH, &sub.Auth,
			&sub.UserAgent, &created, &updated, &lastUsed, &revoked); err != nil {
			return nil, err
		}
		sub.CreatedAt = time.UnixMilli(created)
		sub.UpdatedAt = time.UnixMilli(updated)
		sub.LastUsedAt = time.UnixMilli(lastUsed)
		sub.Revoked = revoked != 0
		out = append(out, sub)
	}
	return out, rows.Err()
}

// RevokePushSubscription marks the row revoked. Idempotent. Endpoint is
// not deleted; it stays around so the UI can still show "this device
// was unsubscribed on YYYY-MM-DD".
func (s *Store) RevokePushSubscription(id string) error {
	now := time.Now().UnixMilli()
	_, err := s.db.Exec(`UPDATE push_subscriptions SET revoked_at = ?
		WHERE id = ? AND revoked_at = 0`, now, id)
	return err
}

// TouchPushSubscription bumps last_used_at. Called after a successful
// delivery so the UI can rank devices by recency.
func (s *Store) TouchPushSubscription(id string) error {
	_, err := s.db.Exec(`UPDATE push_subscriptions SET last_used_at = ?
		WHERE id = ?`, time.Now().UnixMilli(), id)
	return err
}

// DeletePushSubscription hard-deletes a row. Reserved for the operator
// to wipe a device they no longer want listed at all.
func (s *Store) DeletePushSubscription(id string) error {
	_, err := s.db.Exec(`DELETE FROM push_subscriptions WHERE id = ?`, id)
	return err
}
