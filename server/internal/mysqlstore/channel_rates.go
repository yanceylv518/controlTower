package mysqlstore

import (
	"context"
	"controltower/server/internal/tuning"
	"fmt"
	"strings"
	"time"
)

func channelRateMergeSQL(values []string) string {
	return "INSERT INTO channel_rate_seconds(instance_id,channel_id,bucket_time,request_count,tokens) VALUES " + strings.Join(values, ",") + " ON DUPLICATE KEY UPDATE request_count=request_count+VALUES(request_count),tokens=tokens+VALUES(tokens)"
}

// Only instances with evidence of log collection own channel traffic. Legacy
// metrics-only Agents do not send rate markers and must not block their site.
// Use durable cursors, not recent reports: an offline collector must remain a
// required source even after all of its rate buckets have expired.
const channelRateSourcesSQL = `SELECT i.id FROM instances i
WHERE i.enabled=1 AND CASE WHEN i.site_id='' THEN i.id ELSE i.site_id END=?
AND (EXISTS(SELECT 1 FROM agents a WHERE a.instance_id=i.id AND (a.source_latest_log_id>0 OR a.last_log_id>0))
 OR EXISTS(SELECT 1 FROM log_offsets o WHERE o.instance_id=i.id AND o.last_log_id>0))`

const channelRateCoverageSQL = `SELECT COUNT(*),COALESCE(SUM(EXISTS(
 SELECT 1 FROM channel_rate_seconds r WHERE r.instance_id=sources.id AND r.channel_id=0 AND r.bucket_time>=? AND r.bucket_time<=?
)),0) FROM (` + channelRateSourcesSQL + `) sources`

const rollingChannelRatesSQL = `SELECT channel_id,SUM(request_count),SUM(tokens) FROM channel_rate_seconds
WHERE instance_id IN (` + channelRateSourcesSQL + `)
AND channel_id>0 AND bucket_time>? AND bucket_time<=? GROUP BY channel_id`

// Rates are queried directly, independent of persisted tuning evaluations.
// Coverage markers distinguish a quiet source from an old/offline Agent.
func (s Store) QueryRollingChannelRates(id string, now time.Time) ([]tuning.ChannelMetric, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	var enabled, covered int
	err := s.db.QueryRowContext(ctx, channelRateCoverageSQL, now.Add(-90*time.Second), now, id).Scan(&enabled, &covered)
	if err != nil {
		return nil, err
	}
	if enabled == 0 || covered != enabled {
		return nil, fmt.Errorf("current rates unavailable: waiting for fresh Agent rate reports")
	}
	rows, err := s.db.QueryContext(ctx, rollingChannelRatesSQL, id, now.Add(-60*time.Second), now)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []tuning.ChannelMetric{}
	for rows.Next() {
		var m tuning.ChannelMetric
		if err := rows.Scan(&m.ChannelID, &m.RequestCount, &m.TPM); err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}
