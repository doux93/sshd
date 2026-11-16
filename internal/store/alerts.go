package store

import "time"

type AlertRecord struct {
	ID             int64  `json:"id"`
	NodeID         string `json:"nodeId"`
	NodeLabel      string `json:"nodeLabel"`
	Level          string `json:"level"`
	Message        string `json:"message"`
	TS             int64  `json:"ts"`
	AcknowledgedAt int64  `json:"acknowledgedAt"`
}

// Keep the latest 1000 events across restarts, independently of browser presence.
func (s *Store) AppendAlert(nodeID, label, level, message string) (int64, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	r, err := tx.Exec(`INSERT INTO alert_history(node_id,node_label,level,message,ts) VALUES(?,?,?,?,?)`, nodeID, label, level, message, time.Now().UnixMilli())
	if err != nil {
		return 0, err
	}
	id, err := r.LastInsertId()
	if err != nil {
		return 0, err
	}
	_, err = tx.Exec(`DELETE FROM alert_history WHERE id NOT IN (SELECT id FROM alert_history ORDER BY id DESC LIMIT 1000)`)
	if err != nil {
		return 0, err
	}
	return id, tx.Commit()
}

func (s *Store) ListAlerts(nodeID string, before int64, limit int) ([]AlertRecord, error) {
	if limit < 1 || limit > 200 {
		limit = 100
	}
	rows, err := s.db.Query(`SELECT id,node_id,node_label,level,message,ts,acknowledged_at FROM alert_history
		WHERE (? = '' OR node_id = ?) AND (? = 0 OR id < ?) ORDER BY id DESC LIMIT ?`, nodeID, nodeID, before, before, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []AlertRecord{}
	for rows.Next() {
		var a AlertRecord
		if err := rows.Scan(&a.ID, &a.NodeID, &a.NodeLabel, &a.Level, &a.Message, &a.TS, &a.AcknowledgedAt); err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

func (s *Store) AcknowledgeAlert(id int64) (bool, error) {
	r, err := s.db.Exec(`UPDATE alert_history SET acknowledged_at = CASE WHEN acknowledged_at=0 THEN ? ELSE acknowledged_at END WHERE id=?`, time.Now().UnixMilli(), id)
	if err != nil {
		return false, err
	}
	n, err := r.RowsAffected()
	return n > 0, err
}
