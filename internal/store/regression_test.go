package store

import (
	"testing"
	"time"
)

func TestActiveSessionRenewsAtHalfLife(t *testing.T) {
	s := openTestStore(t)
	now := time.Now()
	s.CreateSession(&Session{ID: "active", CreatedAt: now.Add(-20 * 24 * time.Hour), ExpiresAt: now.Add(10 * 24 * time.Hour), LastSeenAt: now})
	got, err := s.TouchSession("active")
	if err != nil {
		t.Fatal(err)
	}
	if time.Until(got.ExpiresAt) < 29*24*time.Hour {
		t.Fatal("active session did not renew")
	}
}
func TestFailedMigrationRollsBackSchemaAndVersion(t *testing.T) {
	s := openTestStore(t)
	old := migrations
	defer func() { migrations = old }()
	migrations = append(append([]string(nil), old...), "CREATE TABLE rollback_probe (id INTEGER); INVALID SQL;")
	if err := s.migrate(); err == nil {
		t.Fatal("invalid migration succeeded")
	}
	var n int
	s.DB().QueryRow("SELECT count(*) FROM sqlite_master WHERE name='rollback_probe'").Scan(&n)
	if n != 0 {
		t.Fatal("partial schema committed")
	}
	s.DB().QueryRow("PRAGMA user_version").Scan(&n)
	if n != len(old) {
		t.Fatal("version advanced on failure")
	}
}
