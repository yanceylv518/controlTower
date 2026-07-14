package mysqlstore

import (
	"context"
	"database/sql"
	"encoding/json"
	"time"

	"controltower/server/internal/tuning"
)

func (s Store) GetPolicy(id string) (tuning.PolicyRecord, bool, error) {
	var r tuning.PolicyRecord
	var raw string
	err := s.db.QueryRowContext(context.Background(), `SELECT policy_json,mode,updated_at,updated_by FROM tuning_policies WHERE instance_id=?`, id).Scan(&raw, &r.Mode, &r.UpdatedAt, &r.UpdatedBy)
	if err == sql.ErrNoRows {
		return r, false, nil
	}
	if err != nil {
		return r, false, err
	}
	r.InstanceID = id
	err = json.Unmarshal([]byte(raw), &r.Policy)
	return r, true, err
}
func (s Store) PutPolicy(r tuning.PolicyRecord) error {
	b, _ := json.Marshal(r.Policy)
	_, e := s.db.ExecContext(context.Background(), `INSERT INTO tuning_policies(instance_id,policy_json,mode,updated_at,updated_by) VALUES(?,?,?,?,?) ON DUPLICATE KEY UPDATE policy_json=VALUES(policy_json),mode=VALUES(mode),updated_at=VALUES(updated_at),updated_by=VALUES(updated_by)`, r.InstanceID, string(b), r.Mode, r.UpdatedAt, r.UpdatedBy)
	return e
}
func (s Store) ListEnabledInstances() ([]string, error) {
	rows, e := s.db.QueryContext(context.Background(), `SELECT id FROM instances WHERE enabled=1`)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var id string
		if e = rows.Scan(&id); e != nil {
			return nil, e
		}
		out = append(out, id)
	}
	return out, rows.Err()
}
func (s Store) QueryMetrics(id string, start, end time.Time) ([]tuning.ChannelMetric, error) {
	rows, e := s.db.QueryContext(context.Background(), `SELECT CAST(dimension_key AS SIGNED),SUM(request_count),SUM(error_count),COALESCE(MAX(p95_use_time),0) FROM metric_1m WHERE instance_id=? AND dimension_type='instance_channel' AND bucket_time>=? AND bucket_time<? GROUP BY dimension_key`, id, start, end)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	var out []tuning.ChannelMetric
	for rows.Next() {
		var m tuning.ChannelMetric
		if e = rows.Scan(&m.ChannelID, &m.RequestCount, &m.ErrorCount, &m.P95); e != nil {
			return nil, e
		}
		out = append(out, m)
	}
	return out, rows.Err()
}
func (s Store) LatestChannels(id string) ([]tuning.Channel, error) {
	rows, e := s.db.QueryContext(context.Background(), `SELECT channel_id,channel_name,status,weight FROM channel_snapshots c WHERE instance_id=? AND captured_at=(SELECT MAX(c2.captured_at) FROM channel_snapshots c2 WHERE c2.instance_id=c.instance_id AND c2.channel_id=c.channel_id)`, id)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	var out []tuning.Channel
	for rows.Next() {
		var c tuning.Channel
		if e = rows.Scan(&c.ID, &c.Name, &c.Status, &c.Weight); e != nil {
			return nil, e
		}
		out = append(out, c)
	}
	return out, rows.Err()
}
func (s Store) InsertRecommendation(r tuning.Recommendation) error {
	ev, _ := json.Marshal(r.Evidence)
	_, e := s.db.ExecContext(context.Background(), `INSERT INTO tuning_recommendations(id,instance_id,channel_id,channel_name,created_at,rule,evidence_json,current_weight,proposed_weight,current_priority,proposed_priority,mode_at_creation,status,command_id) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?)`, r.ID, r.InstanceID, r.ChannelID, r.ChannelName, r.CreatedAt, r.Rule, string(ev), r.CurrentWeight, r.ProposedWeight, r.CurrentPriority, r.ProposedPriority, r.ModeAtCreation, r.Status, r.CommandID)
	return e
}
func (s Store) OriginalDegrade(id string, ch int64) (int64, bool, error) {
	var w int64
	e := s.db.QueryRowContext(context.Background(), `SELECT current_weight FROM tuning_recommendations WHERE instance_id=? AND channel_id=? AND rule='degrade' ORDER BY created_at ASC LIMIT 1`, id, ch).Scan(&w)
	if e == sql.ErrNoRows {
		return 0, false, nil
	}
	return w, e == nil, e
}
func (s Store) HasUnrecoveredDegrade(id string, ch int64) (bool, error) {
	var rule string
	e := s.db.QueryRowContext(context.Background(), `SELECT rule FROM tuning_recommendations WHERE instance_id=? AND channel_id=? AND rule IN ('degrade','recover') ORDER BY created_at DESC LIMIT 1`, id, ch).Scan(&rule)
	if e == sql.ErrNoRows {
		return false, nil
	}
	return rule == "degrade", e
}
func scanRecommendation(rows *sql.Rows) ([]tuning.Recommendation, error) {
	var out []tuning.Recommendation
	for rows.Next() {
		var r tuning.Recommendation
		var ev string
		var outcome sql.NullString
		var outcomeAt sql.NullTime
		var hit sql.NullBool
		var cp, pp sql.NullInt64
		var command sql.NullString
		if e := rows.Scan(&r.ID, &r.InstanceID, &r.ChannelID, &r.ChannelName, &r.CreatedAt, &r.Rule, &ev, &r.CurrentWeight, &r.ProposedWeight, &cp, &pp, &r.ModeAtCreation, &r.Status, &command, &outcome, &outcomeAt, &hit); e != nil {
			return nil, e
		}
		_ = json.Unmarshal([]byte(ev), &r.Evidence)
		if outcome.Valid {
			_ = json.Unmarshal([]byte(outcome.String), &r.Outcome)
		}
		if cp.Valid {
			r.CurrentPriority = &cp.Int64
		}
		if pp.Valid {
			r.ProposedPriority = &pp.Int64
		}
		if command.Valid {
			r.CommandID = &command.String
		}
		if outcomeAt.Valid {
			r.OutcomeAt = &outcomeAt.Time
		}
		if hit.Valid {
			r.Hit = &hit.Bool
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

const recommendationColumns = `id,instance_id,channel_id,channel_name,created_at,rule,evidence_json,current_weight,proposed_weight,current_priority,proposed_priority,mode_at_creation,status,command_id,outcome_json,outcome_at,hit`

func (s Store) PendingOutcomes(before time.Time, limit int) ([]tuning.Recommendation, error) {
	rows, e := s.db.QueryContext(context.Background(), `SELECT `+recommendationColumns+` FROM tuning_recommendations WHERE outcome_at IS NULL AND created_at<=? ORDER BY created_at LIMIT ?`, before, limit)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	return scanRecommendation(rows)
}
func (s Store) UpdateOutcome(id string, out map[string]any, at time.Time, hit *bool) error {
	b, _ := json.Marshal(out)
	_, e := s.db.ExecContext(context.Background(), `UPDATE tuning_recommendations SET outcome_json=?,outcome_at=?,hit=? WHERE id=?`, string(b), at, hit, id)
	return e
}
func (s Store) ListRecommendations(q tuning.RecommendationQuery) ([]tuning.Recommendation, error) {
	if q.Limit <= 0 {
		q.Limit = 50
	}
	if q.Limit > 200 {
		q.Limit = 200
	}
	before := q.Before
	if before.IsZero() {
		before = time.Now().UTC().Add(time.Hour)
	}
	rows, e := s.db.QueryContext(context.Background(), `SELECT `+recommendationColumns+` FROM tuning_recommendations WHERE instance_id=? AND created_at<? ORDER BY created_at DESC LIMIT ?`, q.InstanceID, before, q.Limit)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	return scanRecommendation(rows)
}
func (s Store) RecommendationReport(q tuning.RecommendationQuery) (tuning.Report, error) {
	r := tuning.Report{ByRule: map[string]int64{}}
	since := time.Now().UTC().Add(-time.Duration(q.Days) * 24 * time.Hour)
	rows, e := s.db.QueryContext(context.Background(), `SELECT rule,COUNT(*),SUM(outcome_at IS NOT NULL),SUM(hit=1) FROM tuning_recommendations WHERE instance_id=? AND created_at>=? GROUP BY rule`, q.InstanceID, since)
	if e != nil {
		return r, e
	}
	defer rows.Close()
	for rows.Next() {
		var rule string
		var count, filled, hits sql.NullInt64
		if e = rows.Scan(&rule, &count, &filled, &hits); e != nil {
			return r, e
		}
		r.ByRule[rule] = count.Int64
		r.Total += count.Int64
		r.Filled += filled.Int64
		r.Hits += hits.Int64
	}
	return r, rows.Err()
}
