package mysqlstore

import (
	"context"
	"controltower/server/internal/storage"
	"time"
)

func (s Store) InsertAlertEvents(v []storage.AlertEvent) error {
	for _, e := range v {
		if _, x := s.db.ExecContext(context.Background(), "INSERT INTO alert_events(alert_id,event_type,actor,note,created_at) VALUES(?,?,?,?,?)", e.AlertID, e.EventType, e.Actor, e.Note, e.CreatedAt); x != nil {
			return x
		}
	}
	return nil
}
func (s Store) QueryAlertEvents(id string, limit int) ([]storage.AlertEvent, error) {
	if limit <= 0 {
		limit = 100
	}
	if limit > 500 {
		limit = 500
	}
	r, e := s.db.Query("SELECT id,alert_id,event_type,actor,note,created_at FROM alert_events WHERE alert_id=? ORDER BY created_at ASC LIMIT ?", id, limit)
	if e != nil {
		return nil, e
	}
	defer r.Close()
	var o []storage.AlertEvent
	for r.Next() {
		var v storage.AlertEvent
		if e = r.Scan(&v.ID, &v.AlertID, &v.EventType, &v.Actor, &v.Note, &v.CreatedAt); e != nil {
			return nil, e
		}
		o = append(o, v)
	}
	return o, r.Err()
}
func (s Store) MarkDeliveryForResend(id string, n time.Time) (bool, error) {
	r, e := s.db.Exec("UPDATE notification_deliveries SET status='failed',attempts=0,next_attempt_at=? WHERE id=?", n, id)
	if e != nil {
		return false, e
	}
	x, e := r.RowsAffected()
	return x > 0, e
}
