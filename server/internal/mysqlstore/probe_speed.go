package mysqlstore

import (
	"context"
	"math"
	"time"

	"controltower/server/internal/tuning"
)

func (s Store) RecordRoutineProbeSample(ctx context.Context, v tuning.RoutineProbeSample) error {
	if v.DurationSeconds <= 0 || v.DurationSeconds > 300 || math.IsNaN(v.DurationSeconds) || math.IsInf(v.DurationSeconds, 0) {
		return nil
	}
	_, err := s.db.ExecContext(ctx, `INSERT INTO tuning_routine_probe_minutes
(instance_id,channel_id,model_name,control_revision,base_updated_at,bucket_time,sample_count,duration_seconds,last_started_at)
VALUES(?,?,?,?,?,?,1,?,?) ON DUPLICATE KEY UPDATE sample_count=sample_count+1,duration_seconds=duration_seconds+VALUES(duration_seconds),last_started_at=GREATEST(last_started_at,VALUES(last_started_at))`,
		v.InstanceID, v.ChannelID, v.ModelName, v.ControlRevision, v.BaseUpdatedAt, v.StartedAt.UTC().Truncate(time.Minute), v.DurationSeconds, v.StartedAt.UTC())
	return err
}

func (s Store) QueryRoutineProbeSpeeds(ctx context.Context, site string, from, to time.Time) (map[int64]tuning.ProbeSpeedStats, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT p.channel_id,SUM(p.sample_count),SUM(p.duration_seconds)/SUM(p.sample_count),MAX(p.last_started_at)
FROM tuning_routine_probe_minutes p
JOIN channel_base_values b ON b.instance_id=p.instance_id AND b.channel_id=p.channel_id AND b.model_name=p.model_name AND b.updated_at=p.base_updated_at
LEFT JOIN tuning_continuous_states s ON s.instance_id=p.instance_id AND s.channel_id=p.channel_id
WHERE p.instance_id=? AND p.bucket_time>=? AND p.bucket_time<? AND p.control_revision=COALESCE(s.control_revision,0)
GROUP BY p.channel_id`, site, from, to)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := map[int64]tuning.ProbeSpeedStats{}
	for rows.Next() {
		var id int64
		var v tuning.ProbeSpeedStats
		if err := rows.Scan(&id, &v.Samples, &v.MeanSeconds, &v.LastStartedAt); err != nil {
			return nil, err
		}
		result[id] = v
	}
	return result, rows.Err()
}

func (s Store) PruneRoutineProbeSamples(ctx context.Context, before time.Time) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM tuning_routine_probe_minutes WHERE bucket_time<? LIMIT 10000`, before)
	return err
}
