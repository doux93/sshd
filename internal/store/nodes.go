package store

import (
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

// ConnConfig is the connection-node payload. Secrets are NOT here — only
// presence flags so the UI knows whether a password/passphrase is on file.
type ConnConfig struct {
	PersistentTerminal bool   `json:"persistentTerminal,omitempty"`
	Host               string `json:"host,omitempty"`
	Port               int    `json:"port,omitempty"`
	User               string `json:"user,omitempty"`
	AuthType           string `json:"authType,omitempty"` // "password" | "key" | "agent"
	KeyPath            string `json:"keyPath,omitempty"`
	JumpNodeID         string `json:"jumpNodeId,omitempty"` // another connection node used as bastion
	HasPassword        bool   `json:"hasPassword,omitempty"`
	HasPassphrase      bool   `json:"hasPassphrase,omitempty"`
	// AIPolicy controls what AI tools may do on this host:
	//   "readonly" - only read-only whitelisted commands (default)
	//   "confirm"  - write operations pause for human confirmation
	//   "trusted"  - execute directly (test machines only)
	AIPolicy string `json:"aiPolicy,omitempty"`
	// Proxy is an optional SOCKS5 proxy address ("host:port") for tunneling
	// the SSH connection through a local proxy (Shadowsocks/V2Ray).
	Proxy string `json:"proxy,omitempty"`
	// Monitor enables agentless metric collection on this host.
	Monitor *MonitorConfig `json:"monitor,omitempty"`
	// Alerts are the alert rules evaluated against collected metrics.
	Alerts []AlertRule `json:"alerts,omitempty"`
}

// MonitorConfig controls the agentless collector for a connection node.
type MonitorConfig struct {
	Enabled  bool `json:"enabled"`
	Interval int  `json:"interval,omitempty"` // seconds, 0 = default (5), min 2
}

// AlertRule is one alert condition. Metric matches Sample field names.
type AlertRule struct {
	Metric    string  `json:"metric"`             // "cpuUsage" | "memUsage" | "load1" | "disk"
	Op        string  `json:"op"`                 // ">" | ">=" | "<" | "<="
	Threshold float64 `json:"threshold"`          // e.g. 90 for disk>90%
	Duration  string  `json:"duration,omitempty"` // e.g. "5m"
}

type Node struct {
	ID        string     `json:"id"`
	ParentID  string     `json:"parentId"`
	Kind      string     `json:"kind"` // "folder" | "connection"
	Name      string     `json:"name"`
	SortOrder int        `json:"sortOrder"`
	Config    ConnConfig `json:"config"`
	CreatedAt time.Time  `json:"createdAt"`
	UpdatedAt time.Time  `json:"updatedAt"`
}

var ErrNodeNotFound = errors.New("node not found")

// NewNodeID mints a node id. Exposed so batch operations (imports) can
// assign ids and wire cross-references before inserting.
func NewNodeID() string {
	b := make([]byte, 8)
	rand.Read(b)
	return hex.EncodeToString(b)
}

func (s *Store) CreateNode(n *Node) error {
	if n.Kind != "folder" && n.Kind != "connection" {
		return fmt.Errorf("invalid node kind %q", n.Kind)
	}
	if n.ID == "" {
		n.ID = NewNodeID()
	}
	now := time.Now()
	n.CreatedAt, n.UpdatedAt = now, now
	cfg, err := json.Marshal(n.Config)
	if err != nil {
		return err
	}
	_, err = s.db.Exec(
		`INSERT INTO nodes (id, parent_id, kind, name, sort_order, config, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		n.ID, n.ParentID, n.Kind, n.Name, n.SortOrder, string(cfg),
		now.UnixMilli(), now.UnixMilli(),
	)
	return err
}

func (s *Store) UpdateNode(n *Node) error {
	n.UpdatedAt = time.Now()
	cfg, err := json.Marshal(n.Config)
	if err != nil {
		return err
	}
	res, err := s.db.Exec(
		`UPDATE nodes SET parent_id=?, name=?, sort_order=?, config=?, updated_at=? WHERE id=?`,
		n.ParentID, n.Name, n.SortOrder, string(cfg), n.UpdatedAt.UnixMilli(), n.ID,
	)
	if err != nil {
		return err
	}
	if rows, _ := res.RowsAffected(); rows == 0 {
		return ErrNodeNotFound
	}
	return nil
}

func (s *Store) GetNode(id string) (*Node, error) {
	row := s.db.QueryRow(
		`SELECT id, parent_id, kind, name, sort_order, config, created_at, updated_at
		 FROM nodes WHERE id=?`, id)
	return scanNode(row)
}

// ListNodes returns the whole tree as a flat list ordered for display.
func (s *Store) ListNodes() ([]*Node, error) {
	rows, err := s.db.Query(
		`SELECT id, parent_id, kind, name, sort_order, config, created_at, updated_at
		 FROM nodes ORDER BY parent_id, kind DESC, sort_order, name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var nodes []*Node
	for rows.Next() {
		n, err := scanNode(rows)
		if err != nil {
			return nil, err
		}
		nodes = append(nodes, n)
	}
	return nodes, rows.Err()
}

// DeleteSubtree removes a node and all descendants, returning every deleted
// node id so the caller can purge vault entries.
func (s *Store) DeleteSubtree(id string) ([]string, error) {
	rows, err := s.db.Query(`
		WITH RECURSIVE subtree(id) AS (
			SELECT id FROM nodes WHERE id = ?
			UNION ALL
			SELECT n.id FROM nodes n JOIN subtree st ON n.parent_id = st.id
		)
		SELECT id FROM subtree`, id)
	if err != nil {
		return nil, err
	}
	var ids []string
	for rows.Next() {
		var nid string
		if err := rows.Scan(&nid); err != nil {
			rows.Close()
			return nil, err
		}
		ids = append(ids, nid)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if len(ids) == 0 {
		return nil, ErrNodeNotFound
	}

	tx, err := s.db.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	for _, nid := range ids {
		var count int
		if err := tx.QueryRow(`SELECT count(*) FROM terminal_recovery WHERE node_id=?`, nid).Scan(&count); err != nil {
			return nil, err
		}
		if count > 0 {
			return nil, ErrRecoveryExists
		}
		if _, err := tx.Exec(`DELETE FROM nodes WHERE id=?`, nid); err != nil {
			return nil, err
		}
	}
	return ids, tx.Commit()
}

type rowScanner interface{ Scan(dest ...any) error }

func scanNode(r rowScanner) (*Node, error) {
	var n Node
	var cfg string
	var created, updated int64
	err := r.Scan(&n.ID, &n.ParentID, &n.Kind, &n.Name, &n.SortOrder, &cfg, &created, &updated)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNodeNotFound
	}
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal([]byte(cfg), &n.Config); err != nil {
		return nil, fmt.Errorf("node %s config: %w", n.ID, err)
	}
	n.CreatedAt = time.UnixMilli(created)
	n.UpdatedAt = time.UnixMilli(updated)
	return &n, nil
}
