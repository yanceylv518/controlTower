package mysqlstore

import (
	"context"
	"controltower/server/internal/tuning"
	"encoding/json"
	"strings"
	"time"
)

func (s Store) StoreProbeRuntime(ctx context.Context, values []tuning.ProbeRuntime) error {
	for start := 0; start < len(values); start += 200 {
		batch := values[start:min(start+200, len(values))]
		args := make([]any, 0, len(batch)*4)
		for _, v := range batch {
			raw, err := json.Marshal(v)
			if err != nil {
				return err
			}
			args = append(args, v.InstanceID, v.ChannelID, v.ConfirmedAt, string(raw))
		}
		_, err := s.db.ExecContext(ctx, `INSERT INTO tuning_probe_runtime(instance_id,channel_id,confirmed_at,snapshot_json) VALUES `+strings.TrimSuffix(strings.Repeat("(?,?,?,?),", len(batch)), ",")+` ON DUPLICATE KEY UPDATE snapshot_json=IF(VALUES(confirmed_at)>=confirmed_at,VALUES(snapshot_json),snapshot_json),confirmed_at=GREATEST(confirmed_at,VALUES(confirmed_at))`, args...)
		if err != nil {
			return err
		}
	}
	return nil
}
func (s Store) QueryProbeRuntime(ctx context.Context, site string) ([]tuning.ProbeRuntime, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT snapshot_json FROM tuning_probe_runtime WHERE instance_id=?`, site)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []tuning.ProbeRuntime{}
	for rows.Next() {
		var raw []byte
		var v tuning.ProbeRuntime
		if err = rows.Scan(&raw); err != nil {
			return nil, err
		}
		if err = json.Unmarshal(raw, &v); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}
func (s Store) QueryLifelineCurrent(ctx context.Context, site string) (tuning.LifelineCurrent, error) {
	out := tuning.LifelineCurrent{QueriedAt: time.Now().UTC(), Channels: []tuning.LifelineChannel{}}
	channels, err := s.LatestChannels(site)
	if err != nil {
		return out, err
	}
	policy, found, err := s.GetPolicy(site)
	if err != nil {
		return out, err
	}
	if !found {
		policy.Policy = tuning.DefaultPolicy()
	}
	bases, err := s.ListChannelBaseValues(site, "")
	if err != nil {
		return out, err
	}
	states, err := s.ListContinuousStates(site)
	if err != nil {
		return out, err
	}
	runtime, err := s.QueryProbeRuntime(ctx, site)
	if err != nil {
		return out, err
	}
	byBase := map[int64]tuning.ChannelBaseValue{}
	for _, v := range bases {
		byBase[v.ChannelID] = v
	}
	byState := map[int64]tuning.ContinuousState{}
	for _, v := range states {
		byState[v.ChannelID] = v
	}
	byRuntime := map[int64]tuning.ProbeRuntime{}
	for _, v := range runtime {
		byRuntime[v.ChannelID] = v
	}
	rounds, err := s.currentRecoveryRounds(ctx, site, out.QueriedAt, policy.Policy.Continuous.Rules().ChannelHistoryRetentionDays)
	if err != nil {
		return out, err
	}
	for _, c := range channels {
		var round *tuning.ProbeRound
		if v, ok := rounds[c.ID]; ok {
			round = &v
		}
		out.Channels = append(out.Channels, tuning.DescribeLifelineChannel(c, byBase[c.ID], byState[c.ID], policy, byRuntime[c.ID], out.QueriedAt, round))
	}
	return out, nil
}

// Read active recovery progress in two site-scoped queries, independent of the
// number of channels. There is no per-channel historical overview scan.
func (s Store) currentRecoveryRounds(ctx context.Context, site string, now time.Time, days int) (map[int64]tuning.ProbeRound, error) {
	out := map[int64]tuning.ProbeRound{}
	floor := now.Add(-time.Duration(days) * 24 * time.Hour)
	rows, err := s.db.QueryContext(ctx, `SELECT c.id,c.channel_id,c.payload_json,c.status,c.created_at,c.updated_at FROM channel_commands c WHERE c.command_type='channel.probe' AND (c.instance_id=? OR c.instance_id IN (SELECT id FROM instances WHERE site_id=?)) AND (c.status IN ('pending','delivered','executing') OR EXISTS (SELECT 1 FROM tuning_probe_records p WHERE p.instance_id=? AND p.round_id=c.id AND p.channel_id=c.channel_id AND p.started_at>=? AND p.outcome='pending' AND p.deadline_at>?)) ORDER BY (c.status IN ('pending','delivered','executing')) DESC,c.created_at DESC,c.id DESC`, site, site, site, floor, now)
	if err != nil {
		return out, err
	}
	ids := []any{now, site, floor}
	for rows.Next() {
		var r tuning.ProbeRound
		var raw []byte
		if err = rows.Scan(&r.ID, &r.ChannelID, &raw, &r.Status, &r.CreatedAt, &r.UpdatedAt); err != nil {
			rows.Close()
			return out, err
		}
		var payload struct {
			Model string `json:"model"`
			Count int    `json:"probe_count"`
		}
		if err = json.Unmarshal(raw, &payload); err != nil {
			rows.Close()
			return out, err
		}
		if _, exists := out[r.ChannelID]; exists {
			continue
		}
		r.Model = payload.Model
		r.Planned = payload.Count
		out[r.ChannelID] = r
		ids = append(ids, r.ID)
	}
	err = rows.Err()
	rows.Close()
	if err != nil || len(out) == 0 {
		return out, err
	}
	rs, err := s.db.QueryContext(ctx, `SELECT channel_id,round_id,`+probeMetricsSQL+` FROM (SELECT channel_id,round_id,duration_seconds,reason,started_at,CASE WHEN outcome='pending' AND deadline_at<=? THEN 'unconfirmed' ELSE outcome END outcome FROM tuning_probe_records WHERE instance_id=? AND started_at>=? AND round_id IN (`+strings.TrimSuffix(strings.Repeat("?,", len(out)), ",")+`)) p GROUP BY channel_id,round_id`, ids...)
	if err != nil {
		return out, err
	}
	defer rs.Close()
	for rs.Next() {
		var channel int64
		var round string
		var m tuning.ProbeMetrics
		var scan metricScan
		dest := append([]any{&channel, &round}, scan.args(&m)...)
		if err = rs.Scan(dest...); err != nil {
			return out, err
		}
		scan.finish(&m)
		r, ok := out[channel]
		if ok && r.ID == round {
			r.Metrics = &m
			out[channel] = r
		}
	}
	return out, rs.Err()
}
