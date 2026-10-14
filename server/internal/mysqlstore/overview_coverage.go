package mysqlstore

import (
	"context"
	"controltower/server/internal/aggregator"
	"database/sql"
	"time"
)

// Persist only closed minutes bracketed by adjacent caught-up markers. A
// restart or missed report cannot certify the intervening gap as empty.
func closedCoveredMinutes(previous, through time.Time) []time.Time {
	if previous.IsZero() || !through.After(previous) {
		return nil
	}
	start := previous.UTC().Truncate(time.Minute)
	if start.Before(previous) {
		start = start.Add(time.Minute)
	}
	var out []time.Time
	for !start.Add(time.Minute).After(through) {
		out = append(out, start)
		start = start.Add(time.Minute)
	}
	return out
}

func persistOverviewCoverage(ctx context.Context, tx *sql.Tx, id string, metrics []aggregator.Metric) error {
	var through time.Time
	for _, m := range metrics {
		if m.DimensionType == "channel_rate_second" && m.DimensionKey == "0" && m.RequestCount == 0 && m.BucketTime.After(through) {
			through = m.BucketTime
		}
	}
	if through.IsZero() || through.After(time.Now().UTC().Add(5*time.Second)) {
		return nil
	}
	// A quiet source with zero log IDs must not disappear when short-lived rate
	// markers expire; an offline collector still belongs in expected coverage.
	if _, err := tx.ExecContext(ctx, `INSERT INTO overview_metric_sources(instance_id,last_marker_at) VALUES(?,?) ON DUPLICATE KEY UPDATE last_marker_at=GREATEST(last_marker_at,VALUES(last_marker_at))`, id, through); err != nil {
		return err
	}
	// Called before the batch's markers are inserted, in the same transaction.
	rows, err := tx.QueryContext(ctx, `SELECT bucket_time FROM channel_rate_seconds WHERE instance_id=? AND channel_id=0 AND bucket_time>=? AND bucket_time<? ORDER BY bucket_time DESC`, id, through.Add(-3*time.Minute), through)
	if err != nil {
		return err
	}
	previous := through
	for rows.Next() {
		var at time.Time
		if err = rows.Scan(&at); err != nil {
			rows.Close()
			return err
		}
		if previous.Sub(at) > 90*time.Second {
			break
		}
		previous = at
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	for _, minute := range closedCoveredMinutes(previous, through) {
		if _, err := tx.ExecContext(ctx, `INSERT INTO overview_metric_coverage(instance_id,bucket_time,confirmed_at) VALUES(?,?,?) ON DUPLICATE KEY UPDATE confirmed_at=GREATEST(confirmed_at,VALUES(confirmed_at))`, id, minute, time.Now().UTC()); err != nil {
			return err
		}
	}
	_, err = tx.ExecContext(ctx, `DELETE FROM overview_metric_coverage WHERE bucket_time<? LIMIT 1000`, time.Now().UTC().Add(-48*time.Hour))
	return err
}
