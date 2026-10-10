package mysqlstore

import (
	"context"
	es "controltower/internal/errorstats"
	"crypto/sha256"
	"database/sql"
	"strings"
	"time"
)

func (s Store) SaveErrorStatistics(ctx context.Context, b es.Batch) error {
	if err := b.Validate(time.Now().UTC()); err != nil {
		return err
	}
	// Expired batches are acknowledged without reapplying an aged-out ledger entry.
	if b.ObservedAt.Before(time.Now().UTC().Add(-30 * 24 * time.Hour)) {
		return nil
	}
	// DATETIME(6) rounds sub-microsecond values unless sql_mode requests
	// truncation. Normalize explicitly so activation comparisons are stable.
	// Round the start upward and coverage downward to preserve complete buckets.
	b.StartedAt = b.StartedAt.UTC().Add(time.Microsecond - 1).Truncate(time.Microsecond)
	b.ObservedAt = b.ObservedAt.UTC().Add(time.Microsecond - 1).Truncate(time.Microsecond)
	if b.CoveredUntil != nil {
		covered := b.CoveredUntil.UTC().Truncate(time.Microsecond)
		b.CoveredUntil = &covered
	}
	if b.LastLossAt != nil {
		lost := b.LastLossAt.UTC().Add(time.Microsecond - 1).Truncate(time.Microsecond)
		b.LastLossAt = &lost
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	res, err := tx.ExecContext(ctx, `INSERT IGNORE INTO error_statistics_batches(instance_id,batch_id,created_at) VALUES(?,?,?)`, b.InstanceID, b.ID, time.Now().UTC())
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return tx.Commit()
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO error_statistics_state(instance_id,started_at,observed_at,dropped,covered_until,last_loss_at) VALUES(?,?,?,?,?,?) ON DUPLICATE KEY UPDATE dropped=IF(VALUES(started_at)>started_at,VALUES(dropped),IF(VALUES(started_at)=started_at,GREATEST(dropped,VALUES(dropped)),dropped)),covered_until=IF(VALUES(started_at)>started_at,VALUES(covered_until),IF(VALUES(started_at)=started_at,COALESCE(GREATEST(covered_until,VALUES(covered_until)),covered_until,VALUES(covered_until)),covered_until)),last_loss_at=IF(VALUES(started_at)>started_at,VALUES(last_loss_at),IF(VALUES(started_at)=started_at,COALESCE(GREATEST(last_loss_at,VALUES(last_loss_at)),last_loss_at,VALUES(last_loss_at)),last_loss_at)),observed_at=GREATEST(observed_at,VALUES(observed_at)),started_at=GREATEST(started_at,VALUES(started_at))`, b.InstanceID, b.StartedAt, b.ObservedAt, b.Dropped, b.CoveredUntil, b.LastLossAt)
	if err != nil {
		return err
	}
	// The state upsert holds the instance row lock. A delayed batch from a
	// superseded activation must not mix its counts into the new activation.
	var activeStart time.Time
	if err = tx.QueryRowContext(ctx, `SELECT started_at FROM error_statistics_state WHERE instance_id=?`, b.InstanceID).Scan(&activeStart); err != nil {
		return err
	}
	if !activeStart.Equal(b.StartedAt) {
		return tx.Commit()
	}
	for start := 0; start < len(b.Rows); start += 100 {
		rows := b.Rows[start:min(start+100, len(b.Rows))]
		values := make([]string, 0, len(rows))
		args := make([]any, 0, len(rows)*8)
		for _, r := range rows {
			hash := sha256.Sum256([]byte(r.Model))
			values = append(values, "(?,?,?,?,?,?,?,?)")
			args = append(args, b.InstanceID, r.Minute, r.UserID, r.ChannelID, hash[:], r.Model, r.Code, r.Count)
		}
		_, err = tx.ExecContext(ctx, `INSERT INTO error_statistics_minutes(instance_id,bucket_time,user_id,channel_id,model_hash,model_name,error_code,record_count) VALUES `+strings.Join(values, ",")+` ON DUPLICATE KEY UPDATE record_count=record_count+VALUES(record_count)`, args...)
		if err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (s Store) QueryErrorStatistics(ctx context.Context, q es.Query) (es.Result, error) {
	out := es.Result{Since: q.Since, Until: q.Until, Codes: []es.Count{}, Trend: []es.Point{}, Channels: []es.Count{}, Models: []es.Count{}, Buckets: []es.Bucket{}}
	// Consistent snapshot: totals and the selected-code breakdown must agree even
	// when a report is committed during the query.
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true, Isolation: sql.LevelRepeatableRead})
	if err != nil {
		return out, err
	}
	defer tx.Rollback()
	var start, observed time.Time
	var covered, lost sql.NullTime
	err = tx.QueryRowContext(ctx, `SELECT started_at,observed_at,dropped,covered_until,last_loss_at FROM error_statistics_state WHERE instance_id=?`, q.InstanceID).Scan(&start, &observed, &out.Dropped, &covered, &lost)
	if err == sql.ErrNoRows {
		return out, nil
	}
	if err != nil {
		return out, err
	}
	out.StartedAt = &start
	out.ObservedAt = &observed
	if lost.Valid {
		out.LastLossAt = &lost.Time
	}
	if covered.Valid {
		out.CoveredUntil = &covered.Time
	}
	if q.Timeline {
		step := q.BucketSeconds
		if step != 300 {
			step = 60
		}
		if !covered.Valid || (out.Dropped > 0 && !lost.Valid) {
			return out, tx.Commit()
		}
		var loss *time.Time
		if lost.Valid {
			loss = &lost.Time
		}
		q.Since, q.Until = errorStatisticsWindow(start, covered.Time, loss, q.Since, q.Until, time.Duration(step)*time.Second)
		out.Since, out.Until = q.Since, q.Until
		if !q.Since.Before(q.Until) {
			return out, tx.Commit()
		}
	}
	where := `instance_id=? AND bucket_time>=? AND bucket_time<?`
	args := []any{q.InstanceID, q.Since, q.Until}
	switch q.Dimension {
	case "instance_user":
		where += " AND user_id=?"
		args = append(args, q.Key)
	case "instance_channel":
		where += " AND channel_id=?"
		args = append(args, q.Key)
	case "instance_model":
		hash := sha256.Sum256([]byte(q.Key))
		where += " AND model_hash=?"
		args = append(args, hash[:])
	}
	queryCounts := func(column, condition string, params []any) ([]es.Count, error) {
		result := []es.Count{}
		rows, e := tx.QueryContext(ctx, `SELECT `+column+`,SUM(record_count) AS n FROM error_statistics_minutes WHERE `+condition+` GROUP BY `+column+` ORDER BY n DESC,`+column+` LIMIT 501`, params...)
		if e != nil {
			return nil, e
		}
		defer rows.Close()
		for rows.Next() {
			var c es.Count
			if e = rows.Scan(&c.Key, &c.Count); e != nil {
				return nil, e
			}
			result = append(result, c)
		}
		if len(result) > 500 {
			out.Truncated = true
			result = result[:500]
		}
		return result, rows.Err()
	}
	err = tx.QueryRowContext(ctx, `SELECT COALESCE(SUM(IF(error_code='zero_output',0,record_count)),0),COALESCE(SUM(IF(error_code='zero_output',record_count,0)),0) FROM error_statistics_minutes WHERE `+where, args...).Scan(&out.TotalErrors, &out.ZeroOutputs)
	if err != nil {
		return out, err
	}
	out.Codes, err = queryCounts("error_code", where, args)
	if err != nil {
		return out, err
	}
	if q.Timeline {
		return s.queryErrorTimeline(ctx, tx, q, out, where, args)
	}
	if !q.Details {
		return out, tx.Commit()
	}
	// Zero-output anomalies are excluded from the default error-code analysis.
	if q.Code != "" {
		where += " AND error_code=?"
		args = append(args, q.Code)
	} else {
		where += " AND error_code<>'zero_output'"
	}
	rows, err := tx.QueryContext(ctx, `SELECT bucket_time,SUM(record_count) FROM error_statistics_minutes WHERE `+where+` GROUP BY bucket_time ORDER BY bucket_time`, args...)
	if err != nil {
		return out, err
	}
	for rows.Next() {
		var p es.Point
		if err = rows.Scan(&p.Time, &p.Count); err != nil {
			rows.Close()
			return out, err
		}
		out.Trend = append(out.Trend, p)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return out, err
	}
	if q.Channels {
		out.Channels, err = queryCounts("channel_id", where, args)
		if err != nil {
			return out, err
		}
	}
	out.Models, err = queryCounts("model_name", where, args)
	if err != nil {
		return out, err
	}
	return out, tx.Commit()
}
