package store

import (
	"database/sql"
	"errors"
	"time"
)

// KnownHost records a trusted host key (TOFU: trust on first use).
type KnownHost struct {
	Host        string    `json:"host"` // normalized "host:port"
	KeyType     string    `json:"keyType"`
	Fingerprint string    `json:"fingerprint"` // "SHA256:..."
	PublicKey   string    `json:"publicKey"`   // base64 of the wire-format key
	FirstSeen   time.Time `json:"firstSeen"`
}

// GetKnownHost returns the stored key for (host, keyType), or nil.
func (s *Store) GetKnownHost(host, keyType string) (*KnownHost, error) {
	var kh KnownHost
	var seen int64
	err := s.db.QueryRow(
		`SELECT host, key_type, fingerprint, public_key, first_seen
		 FROM known_hosts WHERE host=? AND key_type=?`, host, keyType,
	).Scan(&kh.Host, &kh.KeyType, &kh.Fingerprint, &kh.PublicKey, &seen)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	kh.FirstSeen = time.UnixMilli(seen)
	return &kh, nil
}

// PutKnownHost inserts or replaces the trusted key for (host, keyType).
func (s *Store) PutKnownHost(kh *KnownHost) error {
	_, err := s.db.Exec(
		`INSERT OR REPLACE INTO known_hosts (host, key_type, fingerprint, public_key, first_seen)
		 VALUES (?, ?, ?, ?, ?)`,
		kh.Host, kh.KeyType, kh.Fingerprint, kh.PublicKey, time.Now().UnixMilli(),
	)
	return err
}
