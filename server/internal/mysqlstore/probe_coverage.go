package mysqlstore

import (
	"context"
	"database/sql"
	"strings"
	"time"

	"controltower/server/internal/tuning"
)

func (s Store) StoreRoutineProbeCoverage(ctx context.Context, values []tuning.ProbeCoverageBucket) error {
	// Each row is an absolute, monotonic snapshot. Retrying a partially committed
	// batch is safe, including when the client never received the commit result.
	for start := 0; start < len(values); start += 200 {
		batch := values[start:min(start+200, len(values))]
		args := make([]any, 0, len(batch)*14)
		for _, v := range batch {
			args = append(args, v.InstanceID, v.ChannelID, v.BucketTime, v.RunID, v.Scheduled, v.Due, v.Sent, v.GlobalLimited, v.ChannelLimited, v.EvidenceLimited, v.LastDueAt, v.LastSentAt, v.LastSkippedAt, time.Now().UTC())
		}
		_, err := s.db.ExecContext(ctx, `INSERT INTO tuning_routine_probe_coverage
(instance_id,channel_id,bucket_time,run_id,scheduled,due_count,sent_count,global_limited,channel_limited,evidence_limited,last_due_at,last_sent_at,last_skipped_at,updated_at)
VALUES `+strings.TrimSuffix(strings.Repeat("(?,?,?,?,?,?,?,?,?,?,?,?,?,?),", len(batch)), ",")+`
ON DUPLICATE KEY UPDATE due_count=GREATEST(due_count,VALUES(due_count)),sent_count=GREATEST(sent_count,VALUES(sent_count)),
global_limited=GREATEST(global_limited,VALUES(global_limited)),channel_limited=GREATEST(channel_limited,VALUES(channel_limited)),evidence_limited=GREATEST(evidence_limited,VALUES(evidence_limited)),
last_due_at=GREATEST(last_due_at,VALUES(last_due_at)),
last_sent_at=CASE WHEN last_sent_at IS NULL THEN VALUES(last_sent_at) ELSE GREATEST(last_sent_at,COALESCE(VALUES(last_sent_at),last_sent_at)) END,
last_skipped_at=CASE WHEN last_skipped_at IS NULL THEN VALUES(last_skipped_at) ELSE GREATEST(last_skipped_at,COALESCE(VALUES(last_skipped_at),last_skipped_at)) END,
updated_at=GREATEST(updated_at,VALUES(updated_at))`, args...)
		if err != nil {
			return err
		}
	}
	return nil
}

func (s Store) QueryRoutineProbeCoverage(ctx context.Context, site string, from, to time.Time) (map[int64]tuning.RoutineProbeCoverage, time.Time, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT channel_id,scheduled,SUM(due_count),SUM(sent_count),SUM(global_limited),SUM(channel_limited),SUM(evidence_limited),MAX(last_due_at),MAX(last_sent_at),MAX(last_skipped_at),MAX(updated_at)
FROM tuning_routine_probe_coverage WHERE instance_id=? AND bucket_time>=? AND bucket_time<? GROUP BY channel_id,scheduled`, site, from, to)
	if err != nil {
		return nil, time.Time{}, err
	}
	defer rows.Close()
	items := map[int64]tuning.RoutineProbeCoverage{}
	var asOf time.Time
	for rows.Next() {
		var id int64
		var scheduled bool
		var v tuning.ProbeDispatchCounts
		var sent, skipped sql.NullTime
		var updated time.Time
		if err := rows.Scan(&id, &scheduled, &v.Due, &v.Sent, &v.GlobalLimited, &v.ChannelLimited, &v.EvidenceLimited, &v.LastDueAt, &sent, &skipped, &updated); err != nil {
			return nil, time.Time{}, err
		}
		if sent.Valid {
			v.LastSentAt = &sent.Time
		}
		if skipped.Valid {
			v.LastSkippedAt = &skipped.Time
		}
		item := items[id]
		if scheduled {
			item.Scheduled = v
		} else {
			item.Accelerated = v
		}
		items[id] = item
		if updated.After(asOf) {
			asOf = updated
		}
	}
	return items, asOf, rows.Err()
}

func (s Store) PruneRoutineProbeCoverage(ctx context.Context, before time.Time) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM tuning_routine_probe_coverage WHERE bucket_time<? LIMIT 10000`, before)
	return err
}
