package store

import (
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"time"
)

// Recovery records identify a single opt-in terminal per saved host. No secrets
// or terminal output are persisted. A record does not assert remote liveness.
type Recovery struct {
	ID        string    `json:"id"`
	NodeID    string    `json:"nodeId"`
	Label     string    `json:"label"`
	Target    string    `json:"target"`
	RouteKey  string    `json:"-"`
	CreatedAt time.Time `json:"createdAt"`
}

var ErrRecoveryNotFound = errors.New("recoverable terminal not found")
var ErrRecoveryExists = errors.New("主机仍有可恢复终端，请先在会话库结束远端终端或忘记恢复记录")

func (s *Store) CreateRecovery(nodeID, label, target, route string) (*Recovery, error) {
	var id [16]byte
	if _, err := rand.Read(id[:]); err != nil {
		return nil, err
	}
	r := &Recovery{ID: hex.EncodeToString(id[:]), NodeID: nodeID, Label: label, Target: target, RouteKey: route, CreatedAt: time.Now()}
	_, err := s.db.Exec(`INSERT INTO terminal_recovery(id,node_id,label,target,route_key,created_at) VALUES(?,?,?,?,?,?)`, r.ID, r.NodeID, r.Label, r.Target, r.RouteKey, r.CreatedAt.UnixMilli())
	return r, err
}
func scanRecovery(row rowScanner) (*Recovery, error) {
	var r Recovery
	var ts int64
	err := row.Scan(&r.ID, &r.NodeID, &r.Label, &r.Target, &r.RouteKey, &ts)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrRecoveryNotFound
	}
	r.CreatedAt = time.UnixMilli(ts)
	return &r, err
}
func (s *Store) GetRecovery(id string) (*Recovery, error) {
	return scanRecovery(s.db.QueryRow(`SELECT id,node_id,label,target,route_key,created_at FROM terminal_recovery WHERE id=?`, id))
}
func (s *Store) RecoveryByNode(id string) (*Recovery, error) {
	return scanRecovery(s.db.QueryRow(`SELECT id,node_id,label,target,route_key,created_at FROM terminal_recovery WHERE node_id=?`, id))
}
func (s *Store) ListRecovery() ([]*Recovery, error) {
	rows, err := s.db.Query(`SELECT id,node_id,label,target,route_key,created_at FROM terminal_recovery ORDER BY created_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	all := []*Recovery{}
	for rows.Next() {
		r, err := scanRecovery(rows)
		if err != nil {
			return nil, err
		}
		all = append(all, r)
	}
	return all, rows.Err()
}
func (s *Store) DeleteRecovery(id string) error {
	_, err := s.db.Exec(`DELETE FROM terminal_recovery WHERE id=?`, id)
	return err
}
