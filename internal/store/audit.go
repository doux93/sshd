package store

import (
	"encoding/json"
	"time"
)

// AuditEntry is one record in the audit log. Every AI/MCP tool call is
// recorded here; human commands are logged when they pass through the Guard.
type AuditEntry struct {
	ID     int64           `json:"id"`
	TS     int64           `json:"ts"`
	Actor  string          `json:"actor"`  // "human" | "ai" | "mcp"
	Action string          `json:"action"` // tool name, e.g. "run_command"
	Detail json.RawMessage `json:"detail"` // free-form JSON: command, exit code, ...
}

// InsertAudit writes one audit record. detail may be nil for an empty JSON.
func (s *Store) InsertAudit(actor, action string, detail any) error {
	var raw []byte
	if detail != nil {
		var err error
		raw, err = json.Marshal(detail)
		if err != nil {
			return err
		}
	} else {
		raw = []byte("{}")
	}
	_, err := s.db.Exec(
		`INSERT INTO audit_log (ts, actor, action, detail) VALUES (?, ?, ?, ?)`,
		time.Now().UnixMilli(), actor, action, string(raw),
	)
	return err
}

// ListAudit returns the most recent entries (newest first), up to limit.
func (s *Store) ListAudit(limit int) ([]AuditEntry, error) {
	if limit <= 0 || limit > 1000 {
		limit = 100
	}
	rows, err := s.db.Query(
		`SELECT id, ts, actor, action, detail FROM audit_log ORDER BY ts DESC LIMIT ?`,
		limit,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var entries []AuditEntry
	for rows.Next() {
		var e AuditEntry
		var detail string
		if err := rows.Scan(&e.ID, &e.TS, &e.Actor, &e.Action, &detail); err != nil {
			return nil, err
		}
		e.Detail = json.RawMessage(detail)
		entries = append(entries, e)
	}
	return entries, rows.Err()
}
