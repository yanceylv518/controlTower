package mysqlstore

import (
	"context"
	"controltower/server/internal/tuning"
	"database/sql"
	"encoding/json"
	"strings"
)

func (s Store) QueryProbeRounds(ctx context.Context, q tuning.ProbeRecordQuery) (tuning.ProbeRoundPage, error) {
	out := tuning.ProbeRoundPage{Items: []tuning.ProbeRound{}}
	if _, err := s.retainChannelHistory(ctx, &q); err != nil {
		return out, err
	}
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true, Isolation: sql.LevelRepeatableRead})
	if err != nil {
		return out, err
	}
	defer tx.Rollback()
	where := ` WHERE (c.instance_id=? OR c.instance_id IN (SELECT id FROM instances WHERE site_id=?)) AND c.channel_id=? AND c.command_type='channel.probe' AND c.created_at>=? AND c.created_at<?`
	args := []any{q.Site, q.Site, q.ChannelID, q.From, q.To}
	if q.RoundID != "" {
		where += " AND c.id=?"
		args = append(args, q.RoundID)
	}
	if q.Model != "" {
		where += " AND JSON_UNQUOTE(JSON_EXTRACT(c.payload_json,'$.model'))=?"
		args = append(args, q.Model)
	}
	if q.Cursor != "" {
		c, e := decodeProbeCursor(q.Cursor)
		if e != nil {
			return out, e
		}
		where += " AND (c.created_at<? OR (c.created_at=? AND c.id<?))"
		args = append(args, c.At, c.At, c.ID)
	}
	limit := max(1, min(q.Limit, 50))
	args = append(args, limit+1)
	rows, err := tx.QueryContext(ctx, `SELECT c.id,c.channel_id,c.payload_json,c.status,c.created_at,c.updated_at,COALESCE((SELECT a.after_summary FROM operation_audits a WHERE a.id=c.id LIMIT 1),'{}') FROM channel_commands c`+where+` ORDER BY c.created_at DESC,c.id DESC LIMIT ?`, args...)
	if err != nil {
		return out, err
	}
	for rows.Next() {
		var v tuning.ProbeRound
		var payload, report []byte
		if err = rows.Scan(&v.ID, &v.ChannelID, &payload, &v.Status, &v.CreatedAt, &v.UpdatedAt, &report); err != nil {
			rows.Close()
			return out, err
		}
		var p struct {
			Model string `json:"model"`
			Count int    `json:"probe_count"`
		}
		if err = json.Unmarshal(payload, &p); err != nil {
			rows.Close()
			return out, err
		}
		v.Model = p.Model
		v.Planned = p.Count
		var a struct {
			Result struct {
				Attempts  *int `json:"attempts"`
				Successes *int `json:"successes"`
			} `json:"result"`
		}
		if json.Unmarshal(report, &a) == nil {
			v.ReportedAttempts = a.Result.Attempts
			v.ReportedSuccesses = a.Result.Successes
		}
		out.Items = append(out.Items, v)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return out, err
	}
	if len(out.Items) > limit {
		out.Items = out.Items[:limit]
		last := out.Items[limit-1]
		out.NextCursor = encodeProbeCursor(last.CreatedAt, last.ID)
	}
	if len(out.Items) > 0 {
		ids := []any{q.Now, q.Site, q.ChannelID, q.From}
		for _, v := range out.Items {
			ids = append(ids, v.ID)
		}
		// Include the whole batch up to now, not just the historical list's end.
		// The list window selects batches; it never truncates their progress.
		rs, e := tx.QueryContext(ctx, `SELECT round_id,`+probeMetricsSQL+` FROM (SELECT round_id,duration_seconds,reason,started_at,CASE WHEN outcome='pending' AND deadline_at<=? THEN 'unconfirmed' ELSE outcome END AS outcome FROM tuning_probe_records WHERE instance_id=? AND channel_id=? AND started_at>=? AND round_id IN (`+strings.TrimSuffix(strings.Repeat("?,", len(out.Items)), ",")+`)) raw_records GROUP BY round_id`, ids...)
		if e != nil {
			return out, e
		}
		byID := map[string]*tuning.ProbeMetrics{}
		for rs.Next() {
			var id string
			var m tuning.ProbeMetrics
			var scan metricScan
			dest := append([]any{&id}, scan.args(&m)...)
			if e = rs.Scan(dest...); e != nil {
				rs.Close()
				return out, e
			}
			scan.finish(&m)
			byID[id] = &m
		}
		e = rs.Err()
		rs.Close()
		if e != nil {
			return out, e
		}
		for i := range out.Items {
			out.Items[i].Metrics = byID[out.Items[i].ID]
		}
	}
	return out, tx.Commit()
}
