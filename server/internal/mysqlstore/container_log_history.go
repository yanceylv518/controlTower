package mysqlstore

import (
	"context"
	cl "controltower/internal/containerlog"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// Assign surviving pre-batch records once, before pagination. No log bodies are
// read. Row locks serialize concurrent upgrades; newer explicit batch IDs stay intact.
func (s Store) backfillLogBatches(ctx context.Context) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	rows, err := tx.QueryContext(ctx, `SELECT t.id,t.instance_id,t.agent_id,t.actor_id,t.query_json,t.created_at,COALESCE(NULLIF(i.site_id,''),t.instance_id) FROM container_log_tasks t LEFT JOIN instances i ON i.id=t.instance_id WHERE t.history_batch='' ORDER BY t.created_at DESC,t.id DESC FOR UPDATE`)
	if err != nil {
		return err
	}
	type legacyGroup struct {
		batch   string
		at      time.Time
		sources map[string]bool
	}
	groups := map[string][]*legacyGroup{}
	updates := map[string]string{}
	for rows.Next() {
		var id, instance, agent, raw, site string
		var actor int64
		var at time.Time
		var q cl.Query
		if err = rows.Scan(&id, &instance, &agent, &actor, &raw, &at, &site); err != nil {
			rows.Close()
			return err
		}
		if err = json.Unmarshal([]byte(raw), &q); err != nil {
			rows.Close()
			return err
		}
		sig, _ := json.Marshal([]any{actor, site, q.From, q.To, q.Keyword, q.RequestID, q.ErrorCode, q.Kind, q.Host, q.Path, q.Level, q.MinDurationMS})
		source := instance + "/" + agent + "/" + q.SourceID + "/" + q.Container
		var selected *legacyGroup
		for _, g := range groups[string(sig)] {
			if g.at.Sub(at) <= 10*time.Second && !g.sources[source] {
				selected = g
				break
			}
		}
		if selected == nil {
			selected = &legacyGroup{batch: "legacy-" + id, at: at, sources: map[string]bool{}}
			groups[string(sig)] = append(groups[string(sig)], selected)
		}
		selected.sources[source] = true
		updates[id] = selected.batch
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	for id, batch := range updates {
		if _, err = tx.ExecContext(ctx, `UPDATE container_log_tasks SET query_json=JSON_SET(query_json,'$.batch_id',?) WHERE id=?`, batch, id); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (s Store) ListContainerLogHistory(ctx context.Context, f cl.HistoryFilter) (cl.HistoryPage, error) {
	out := cl.HistoryPage{Items: []cl.HistoryGroup{}, Page: f.Page, PageSize: f.PageSize}
	if f.Page < 1 || f.PageSize < 1 || f.PageSize > 100 {
		return out, fmt.Errorf("invalid pagination")
	}
	if err := s.expireContainerLogs(ctx); err != nil {
		return out, err
	}
	if err := s.backfillLogBatches(ctx); err != nil {
		return out, err
	}
	where := []string{"t.created_at >= UTC_TIMESTAMP() - INTERVAL 1 DAY"}
	args := []any{}
	if f.ActorID != 0 {
		where = append(where, "t.actor_id=?")
		args = append(args, f.ActorID)
	}
	if f.Site != "" {
		where = append(where, "COALESCE(NULLIF(i.site_id,''),t.instance_id)=?")
		args = append(args, f.Site)
	}
	if f.Actor != "" {
		where = append(where, "(t.actor=? OR t.actor_name=?)")
		args = append(args, f.Actor, f.Actor)
	}
	if f.RequestID != "" {
		where = append(where, "JSON_UNQUOTE(JSON_EXTRACT(t.query_json,'$.request_id'))=?")
		args = append(args, f.RequestID)
	}
	// Filter batch submission time rather than individual child submission times.
	having := []string{}
	if !f.From.IsZero() {
		having = append(having, "MAX(t.created_at)>=?")
		args = append(args, f.From)
	}
	if !f.To.IsZero() {
		having = append(having, "MAX(t.created_at)<=?")
		args = append(args, f.To)
	}
	base := ` FROM container_log_tasks t LEFT JOIN instances i ON i.id=t.instance_id WHERE ` + strings.Join(where, " AND ") + ` GROUP BY t.actor_id,t.history_batch`
	if len(having) > 0 {
		base += " HAVING " + strings.Join(having, " AND ")
	}
	// Count and page share a consistent snapshot; fetch summaries only for the
	// selected batches. Log bodies remain behind the existing detail endpoint.
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true, Isolation: sql.LevelRepeatableRead})
	if err != nil {
		return out, err
	}
	defer tx.Rollback()
	if err = tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM (SELECT 1`+base+`) batches`, args...).Scan(&out.Total); err != nil {
		return out, err
	}
	pageArgs := append(append([]any{}, args...), f.PageSize, (f.Page-1)*f.PageSize)
	rows, err := tx.QueryContext(ctx, `SELECT t.actor_id,t.history_batch,MAX(t.created_at),MAX(t.id)`+base+` ORDER BY MAX(t.created_at) DESC,MAX(t.id) DESC LIMIT ? OFFSET ?`, pageArgs...)
	if err != nil {
		return out, err
	}
	type entry struct {
		actor int64
		batch string
		group cl.HistoryGroup
	}
	entries := []entry{}
	for rows.Next() {
		var e entry
		if err = rows.Scan(&e.actor, &e.batch, &e.group.CreatedAt, &e.group.ID); err != nil {
			rows.Close()
			return out, err
		}
		entries = append(entries, e)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return out, err
	}
	for _, e := range entries {
		query := `SELECT t.id,t.instance_id,t.agent_id,t.actor_id,t.actor,t.actor_name,t.query_json,JSON_OBJECT('status',t.status,'lines',JSON_ARRAY()),t.created_at FROM container_log_tasks t LEFT JOIN instances i ON i.id=t.instance_id WHERE t.actor_id=? AND t.history_batch=?`
		values := []any{e.actor, e.batch}
		if f.Site != "" {
			query += ` AND COALESCE(NULLIF(i.site_id,''),t.instance_id)=?`
			values = append(values, f.Site)
		}
		r, err := tx.QueryContext(ctx, query+` ORDER BY t.created_at,t.id`, values...)
		if err != nil {
			return out, err
		}
		e.group.Tasks = []cl.Task{}
		for r.Next() {
			t, err := scanContainerLog(r)
			if err != nil {
				r.Close()
				return out, err
			}
			e.group.Tasks = append(e.group.Tasks, t)
		}
		err = r.Err()
		r.Close()
		if err != nil {
			return out, err
		}
		out.Items = append(out.Items, e.group)
	}
	return out, tx.Commit()
}
