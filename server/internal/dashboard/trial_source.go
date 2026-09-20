package dashboard

import (
	"context"
	"controltower/server/internal/voicealert"
	"database/sql"
	"fmt"
	"time"
)

func (h *PassthroughHandler) trialDB(site string) (*sql.DB, error) {
	db, ok, err := h.database(site)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, fmt.Errorf("readonly logs database unavailable")
	}
	return db, nil
}
func (h *PassthroughHandler) TrialHead(ctx context.Context, site string) (int64, error) {
	db, err := h.trialDB(site)
	if err != nil {
		return 0, err
	}
	var id int64
	err = db.QueryRowContext(ctx, `SELECT COALESCE(MAX(id),0) FROM logs`).Scan(&id)
	return id, err
}
func (h *PassthroughHandler) TrialLogs(ctx context.Context, site string, after int64) ([]voicealert.TrialLog, error) {
	db, err := h.trialDB(site)
	if err != nil {
		return nil, err
	}
	// Bounded overlap catches short out-of-order commits without scanning history.
	after -= 1000
	if after < 0 {
		after = 0
	}
	rows, err := db.QueryContext(ctx, `SELECT id,user_id,COALESCE(token_id,0),type,created_at,COALESCE(model_name,'') FROM logs WHERE id>? ORDER BY id LIMIT 10000`, after)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []voicealert.TrialLog{}
	for rows.Next() {
		var l voicealert.TrialLog
		var at int64
		if err = rows.Scan(&l.ID, &l.UserID, &l.TokenID, &l.Type, &at, &l.Model); err != nil {
			return nil, err
		}
		l.CreatedAt = time.Unix(at, 0).UTC()
		out = append(out, l)
	}
	return out, rows.Err()
}
func (h *PassthroughHandler) TrialIdentities(ctx context.Context, site string, user int64) ([]voicealert.TrialIdentity, error) {
	db, err := h.trialDB(site)
	if err != nil {
		return nil, err
	}
	query := `SELECT id,username FROM users WHERE deleted_at IS NULL ORDER BY id LIMIT 10001`
	args := []any{}
	if user > 0 {
		query = `SELECT id,name FROM tokens WHERE user_id=? AND deleted_at IS NULL ORDER BY id LIMIT 10001`
		args = append(args, user)
	}
	rows, err := db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []voicealert.TrialIdentity{}
	for rows.Next() {
		var i voicealert.TrialIdentity
		if err = rows.Scan(&i.ID, &i.Name); err != nil {
			return nil, err
		}
		out = append(out, i)
	}
	if len(out) > 10000 {
		return nil, fmt.Errorf("identity directory exceeds supported limit")
	}
	return out, rows.Err()
}
