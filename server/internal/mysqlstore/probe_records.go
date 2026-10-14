package mysqlstore

import (
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"controltower/server/internal/tuning"
)

func (s Store) StoreProbeRecords(ctx context.Context, records []tuning.ProbeRecord, health []tuning.ProbeRecorderHealth) error {
	for start := 0; start < len(records); start += 200 {
		batch := records[start:min(start+200, len(records))]
		args := make([]any, 0, len(batch)*13)
		for _, v := range batch {
			args = append(args, v.ID, v.InstanceID, v.ChannelID, v.ChannelName, v.ModelName, v.Kind, v.StartedAt, v.DeadlineAt, v.FinishedAt, v.Outcome, v.Reason, v.DurationSeconds, v.RoundID, v.RoundIndex)
		}
		_, err := s.db.ExecContext(ctx, `INSERT INTO tuning_probe_records (id,instance_id,channel_id,channel_name,model_name,kind,started_at,deadline_at,finished_at,outcome,reason,duration_seconds,round_id,round_index) VALUES `+strings.TrimSuffix(strings.Repeat("(?,?,?,?,?,?,?,?,?,?,?,?,?,?),", len(batch)), ",")+`
ON DUPLICATE KEY UPDATE finished_at=IF(outcome='pending',VALUES(finished_at),finished_at),reason=IF(outcome='pending',VALUES(reason),reason),duration_seconds=IF(outcome='pending',VALUES(duration_seconds),duration_seconds),outcome=IF(outcome='pending',VALUES(outcome),outcome)`, args...)
		if err != nil {
			return err
		}
	}
	for _, h := range health {
		_, err := s.db.ExecContext(ctx, `INSERT INTO tuning_probe_recorders (instance_id,run_id,started_at,last_seen_at,last_loss_at,dropped_records,queued_records) VALUES(?,?,?,?,?,?,?) ON DUPLICATE KEY UPDATE last_seen_at=VALUES(last_seen_at),last_loss_at=VALUES(last_loss_at),dropped_records=GREATEST(dropped_records,VALUES(dropped_records)),queued_records=VALUES(queued_records)`, h.InstanceID, h.RunID, h.StartedAt, h.LastSeenAt, h.LastLossAt, h.DroppedRecords, h.QueuedRecords)
		if err != nil {
			return err
		}
	}
	return nil
}
func (s Store) PruneProbeRecords(ctx context.Context, now time.Time) error {
	if _, err := s.db.ExecContext(ctx, `DELETE FROM tuning_probe_runtime WHERE confirmed_at<? LIMIT 10000`, now.Add(-24*time.Hour)); err != nil {
		return err
	}
	// Include disabled/deleted sites: retaining a stopped site must not disable
	// cleanup. Shared channel commands/audits are handled separately below.
	rows, err := s.db.QueryContext(ctx, `SELECT DISTINCT instance_id FROM tuning_probe_records UNION SELECT DISTINCT instance_id FROM channel_control_requests UNION SELECT DISTINCT instance_id FROM tuning_probe_recorders`)
	if err != nil {
		return err
	}
	var sites []string
	for rows.Next() {
		var site string
		if err = rows.Scan(&site); err != nil {
			rows.Close()
			return err
		}
		sites = append(sites, site)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	for _, site := range sites {
		days, e := s.channelHistoryDays(ctx, site)
		if e != nil {
			return e
		}
		before := now.Add(-time.Duration(days) * 24 * time.Hour)
		for _, table := range []string{"tuning_probe_records", "channel_control_requests"} {
			if _, err = s.db.ExecContext(ctx, "DELETE FROM "+table+" WHERE instance_id=? AND started_at<? LIMIT 10000", site, before); err != nil {
				return err
			}
		}
		if _, err = s.db.ExecContext(ctx, `DELETE FROM tuning_probe_recorders WHERE instance_id=? AND last_seen_at<?`, site, before); err != nil {
			return err
		}
	}
	return s.pruneChannelHistory(ctx, now)
}

// Restrict every lifecycle read immediately, even while batched cleanup catches up.
func (s Store) channelHistoryDays(ctx context.Context, site string) (int, error) {
	var raw string
	err := s.db.QueryRowContext(ctx, `SELECT policy_json FROM tuning_policies WHERE instance_id=?`, site).Scan(&raw)
	if err == sql.ErrNoRows {
		return tuning.DefaultContinuousRules().ChannelHistoryRetentionDays, nil
	}
	if err != nil {
		return 0, err
	}
	p, err := tuning.DecodePolicyJSON([]byte(raw))
	if err != nil {
		return 0, err
	}
	return p.Continuous.Rules().ChannelHistoryRetentionDays, nil
}
func (s Store) retainChannelHistory(ctx context.Context, q *tuning.ProbeRecordQuery) (int, error) {
	days, err := s.channelHistoryDays(ctx, q.Site)
	if err != nil {
		return 0, err
	}
	if q.Now.IsZero() {
		q.Now = time.Now().UTC()
	}
	floor := q.Now.Add(-time.Duration(days) * 24 * time.Hour)
	if q.From.Before(floor) {
		q.From = floor
	}
	if q.From.After(q.To) {
		q.From = q.To
	}
	return days, nil
}

func probeRecordSource(q tuning.ProbeRecordQuery) (string, []any) {
	where := "instance_id=? AND started_at>=? AND started_at<?"
	args := []any{q.Now, q.Site, q.From, q.To}
	if q.RoundID != "" {
		where += " AND round_id=?"
		args = append(args, q.RoundID)
	}
	if q.Dispatch == "sent" {
		where += " AND outcome<>'skipped'"
	} else if q.Dispatch == "unsent" {
		where += " AND outcome='skipped'"
	}
	if q.ChannelID > 0 {
		where += " AND channel_id=?"
		args = append(args, q.ChannelID)
	}
	if q.Model != "" {
		where += " AND model_name=?"
		args = append(args, q.Model)
	}
	if q.Kind != "" {
		where += " AND kind=?"
		args = append(args, q.Kind)
	}
	if q.Search != "" {
		where += " AND (LOCATE(?,channel_name)>0 OR CAST(channel_id AS CHAR)=?)"
		args = append(args, q.Search, q.Search)
	}
	return `(SELECT id,instance_id,channel_id,channel_name,model_name,kind,started_at,deadline_at,finished_at,reason,duration_seconds,round_id,round_index,CASE WHEN outcome='pending' AND deadline_at<=? THEN 'unconfirmed' ELSE outcome END AS outcome FROM tuning_probe_records WHERE ` + where + `) p`, args
}

const probeMetricsSQL = `COUNT(*),COALESCE(SUM(outcome<>'skipped'),0),
COALESCE(SUM(outcome='success'),0),COALESCE(SUM(outcome='slow'),0),COALESCE(SUM(outcome='error'),0),COALESCE(SUM(outcome='unknown'),0),COALESCE(SUM(outcome='canceled'),0),COALESCE(SUM(outcome='pending'),0),COALESCE(SUM(outcome='unconfirmed'),0),COALESCE(SUM(outcome='skipped'),0),
COALESCE(SUM(reason='global_concurrency'),0),COALESCE(SUM(reason='channel_concurrency'),0),COALESCE(SUM(reason='pending_evidence'),0),
AVG(CASE WHEN outcome IN ('success','slow','error') THEN duration_seconds END),AVG(CASE WHEN outcome='success' THEN duration_seconds END),MAX(CASE WHEN outcome IN ('success','slow','error') THEN duration_seconds END),MAX(started_at),
COUNT(CASE WHEN outcome IN ('success','slow','error') THEN duration_seconds END),COUNT(CASE WHEN outcome='success' THEN duration_seconds END)`

type metricScan struct {
	mean, successMean, maximum          sql.NullFloat64
	last                                sql.NullTime
	durationCount, successDurationCount int64
}

func (s *metricScan) args(v *tuning.ProbeMetrics) []any {
	return []any{&v.Due, &v.Sent, &v.Success, &v.Slow, &v.Errors, &v.Unknown, &v.Canceled, &v.Pending, &v.Unconfirmed, &v.Skipped, &v.GlobalLimited, &v.ChannelLimited, &v.EvidenceLimited, &s.mean, &s.successMean, &s.maximum, &s.last, &s.durationCount, &s.successDurationCount}
}

// Reuse the channel aggregates for the overview total instead of scanning the
// same record range a third time. AVG ignores NULL durations, so weight means
// by measured durations rather than by outcome counts or channel counts.
func (s *metricScan) add(total *tuning.ProbeMetrics, v tuning.ProbeMetrics, part metricScan) {
	total.Due += v.Due
	total.Sent += v.Sent
	total.Success += v.Success
	total.Slow += v.Slow
	total.Errors += v.Errors
	total.Unknown += v.Unknown
	total.Canceled += v.Canceled
	total.Pending += v.Pending
	total.Unconfirmed += v.Unconfirmed
	total.Skipped += v.Skipped
	total.GlobalLimited += v.GlobalLimited
	total.ChannelLimited += v.ChannelLimited
	total.EvidenceLimited += v.EvidenceLimited
	if part.durationCount > 0 {
		s.mean.Float64 = (s.mean.Float64*float64(s.durationCount) + part.mean.Float64*float64(part.durationCount)) / float64(s.durationCount+part.durationCount)
		s.durationCount += part.durationCount
		s.mean.Valid = true
	}
	if part.successDurationCount > 0 {
		s.successMean.Float64 = (s.successMean.Float64*float64(s.successDurationCount) + part.successMean.Float64*float64(part.successDurationCount)) / float64(s.successDurationCount+part.successDurationCount)
		s.successDurationCount += part.successDurationCount
		s.successMean.Valid = true
	}
	if part.maximum.Valid && (!s.maximum.Valid || part.maximum.Float64 > s.maximum.Float64) {
		s.maximum = part.maximum
	}
	if part.last.Valid && (!s.last.Valid || part.last.Time.After(s.last.Time)) {
		s.last = part.last
	}
}

func (s metricScan) finish(v *tuning.ProbeMetrics) {
	if n := v.Success + v.Slow + v.Errors; n > 0 {
		rate := float64(v.Success) / float64(n)
		v.SuccessRate = &rate
	}
	if s.mean.Valid {
		v.MeanSeconds = &s.mean.Float64
	}
	if s.successMean.Valid {
		v.MeanSuccessSeconds = &s.successMean.Float64
	}
	if s.maximum.Valid {
		v.MaxSeconds = &s.maximum.Float64
	}
	if s.last.Valid {
		v.LastStartedAt = &s.last.Time
	}
}
func (s Store) QueryProbeOverview(ctx context.Context, q tuning.ProbeRecordQuery) (tuning.ProbeOverview, error) {
	days, err := s.retainChannelHistory(ctx, &q)
	if err != nil {
		return tuning.ProbeOverview{}, err
	}
	out := tuning.ProbeOverview{RetentionDays: days, From: q.From, To: q.To, QueriedAt: q.Now, Channels: []tuning.ProbeChannelStats{}, Trend: []tuning.ProbeTrendPoint{}}
	out.BucketSeconds = max(60, int(q.To.Sub(q.From).Seconds()/120/60)*60)
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true, Isolation: sql.LevelRepeatableRead})
	if err != nil {
		return out, err
	}
	defer tx.Rollback()
	source, args := probeRecordSource(q)
	var totalScan metricScan
	rows, err := tx.QueryContext(ctx, "SELECT channel_id,MAX(channel_name),model_name,"+probeMetricsSQL+" FROM "+source+" GROUP BY channel_id,model_name ORDER BY channel_id,model_name", args...)
	if err != nil {
		return out, err
	}
	for rows.Next() {
		var v tuning.ProbeChannelStats
		var scan metricScan
		dest := append([]any{&v.ChannelID, &v.ChannelName, &v.ModelName}, scan.args(&v.ProbeMetrics)...)
		if err = rows.Scan(dest...); err != nil {
			rows.Close()
			return out, err
		}
		scan.finish(&v.ProbeMetrics)
		totalScan.add(&out.Total, v.ProbeMetrics, scan)
		out.Channels = append(out.Channels, v)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return out, err
	}
	totalScan.finish(&out.Total)
	trendArgs := append([]any{out.BucketSeconds}, args...)
	rows, err = tx.QueryContext(ctx, "SELECT FLOOR(TIMESTAMPDIFF(SECOND,'1970-01-01',started_at)/?) AS bucket,"+probeMetricsSQL+" FROM "+source+" GROUP BY bucket ORDER BY bucket", trendArgs...)
	if err != nil {
		return out, err
	}
	for rows.Next() {
		var v tuning.ProbeTrendPoint
		var bucket int64
		var scan metricScan
		dest := append([]any{&bucket}, scan.args(&v.ProbeMetrics)...)
		if err = rows.Scan(dest...); err != nil {
			rows.Close()
			return out, err
		}
		v.At = time.Unix(bucket*int64(out.BucketSeconds), 0).UTC()
		scan.finish(&v.ProbeMetrics)
		out.Trend = append(out.Trend, v)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return out, err
	}
	var h tuning.ProbeRecorderHealth
	var loss sql.NullTime
	err = tx.QueryRowContext(ctx, `SELECT MIN(started_at),MAX(last_seen_at),MAX(last_loss_at),SUM(dropped_records),SUM(queued_records) FROM tuning_probe_recorders WHERE instance_id=? HAVING COUNT(*)>0`, q.Site).Scan(&h.StartedAt, &h.LastSeenAt, &loss, &h.DroppedRecords, &h.QueuedRecords)
	if err != nil && err != sql.ErrNoRows {
		return out, err
	}
	if err == nil {
		if loss.Valid {
			h.LastLossAt = &loss.Time
		}
		out.Recorder = &h
	}
	return out, tx.Commit()
}

type probePageCursor struct {
	At time.Time `json:"at"`
	ID string    `json:"id"`
}

func decodeProbeCursor(raw string) (probePageCursor, error) {
	var c probePageCursor
	b, err := base64.RawURLEncoding.DecodeString(raw)
	if err == nil {
		err = json.Unmarshal(b, &c)
	}
	if err != nil || c.At.IsZero() || c.ID == "" || len(c.ID) > 128 {
		return c, fmt.Errorf("invalid cursor")
	}
	return c, nil
}
func encodeProbeCursor(at time.Time, id string) string {
	b, _ := json.Marshal(probePageCursor{at, id})
	return base64.RawURLEncoding.EncodeToString(b)
}
func (s Store) QueryProbeRecords(ctx context.Context, q tuning.ProbeRecordQuery) (tuning.ProbeRecordPage, error) {
	out := tuning.ProbeRecordPage{Items: []tuning.ProbeRecord{}}
	if _, err := s.retainChannelHistory(ctx, &q); err != nil {
		return out, err
	}
	source, args := probeRecordSource(q)
	where := " WHERE 1=1"
	if q.Outcome != "" {
		where += " AND outcome=?"
		args = append(args, q.Outcome)
	}
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true, Isolation: sql.LevelRepeatableRead})
	if err != nil {
		return out, err
	}
	defer tx.Rollback()
	if err = tx.QueryRowContext(ctx, "SELECT COUNT(*) FROM "+source+where, args...).Scan(&out.Total); err != nil {
		return out, err
	}
	if q.Cursor != "" {
		c, e := decodeProbeCursor(q.Cursor)
		if e != nil {
			return out, e
		}
		where += " AND (started_at<? OR (started_at=? AND id<?))"
		args = append(args, c.At, c.At, c.ID)
	}
	limit := max(1, min(q.Limit, 200))
	args = append(args, limit+1)
	rows, err := tx.QueryContext(ctx, `SELECT id,instance_id,channel_id,channel_name,model_name,kind,started_at,deadline_at,finished_at,outcome,reason,duration_seconds,round_id,round_index FROM `+source+where+" ORDER BY started_at DESC,id DESC LIMIT ?", args...)
	if err != nil {
		return out, err
	}
	for rows.Next() {
		var v tuning.ProbeRecord
		var finished sql.NullTime
		var duration sql.NullFloat64
		if err = rows.Scan(&v.ID, &v.InstanceID, &v.ChannelID, &v.ChannelName, &v.ModelName, &v.Kind, &v.StartedAt, &v.DeadlineAt, &finished, &v.Outcome, &v.Reason, &duration, &v.RoundID, &v.RoundIndex); err != nil {
			rows.Close()
			return out, err
		}
		if finished.Valid {
			v.FinishedAt = &finished.Time
		}
		if duration.Valid {
			v.DurationSeconds = &duration.Float64
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
		out.NextCursor = encodeProbeCursor(last.StartedAt, last.ID)
	}
	return out, tx.Commit()
}
