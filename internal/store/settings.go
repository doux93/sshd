package store

import (
	"database/sql"
	"errors"
)

// ErrSettingNotFound is returned by GetSetting when no row has the key.
var ErrSettingNotFound = errors.New("setting not found")

// GetSetting reads one row from the settings table. Returns
// ErrSettingNotFound when the key is absent.
func (s *Store) GetSetting(key string) (string, error) {
	var v string
	err := s.db.QueryRow(`SELECT value FROM settings WHERE key = ?`, key).Scan(&v)
	if errors.Is(err, sql.ErrNoRows) {
		return "", ErrSettingNotFound
	}
	return v, err
}

// SetSetting upserts a row.
func (s *Store) SetSetting(key, value string) error {
	_, err := s.db.Exec(
		`INSERT INTO settings (key, value) VALUES (?, ?)
		 ON CONFLICT(key) DO UPDATE SET value = excluded.value`,
		key, value)
	return err
}
