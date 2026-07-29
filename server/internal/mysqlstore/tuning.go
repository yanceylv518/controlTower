package mysqlstore

import (
	"context"
	"database/sql"
	"encoding/json"
	"strings"
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
	// Old observe policies remain in production. Decode on top of the new
	// defaults so removed fields are ignored and newly introduced fields never
	// become unsafe zero values during the v2.9 schema transition.
	r.Policy = tuning.DefaultPolicy()
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
// instance_channel dimension keys are "<instance>:channel:<id>"; the channel
// id must be split out of the suffix — CAST on the whole key yields 0 for
// every row and no channel ever matches.
const tuningChannelMetricsSQL = `SELECT CAST(SUBSTRING_INDEX(dimension_key,':',-1) AS SIGNED),SUM(request_count),SUM(error_count),COALESCE(MAX(p95_use_time),0) FROM metric_1m WHERE instance_id=? AND dimension_type='instance_channel' AND bucket_time>=? AND bucket_time<? GROUP BY dimension_key`

func (s Store) QueryMetrics(id string, start, end time.Time) ([]tuning.ChannelMetric, error) {
	rows, e := s.db.QueryContext(context.Background(), tuningChannelMetricsSQL, id, start, end)
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
	rows, e := s.db.QueryContext(context.Background(), latestChannelsSQL, id, id)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	var out []tuning.Channel
	for rows.Next() {
		var c tuning.Channel
		var models string
		if e = rows.Scan(&c.ID, &c.Name, &c.Status, &c.Weight, &models, &c.Priority); e != nil {
			return nil, e
		}
		c.Models = parseChannelModels(models)
		out = append(out, c)
	}
	return out, rows.Err()
}

const latestChannelsSQL = `
SELECT c.channel_id,c.channel_name,c.status,c.weight,c.models_text,COALESCE(c.priority,0)
FROM channel_snapshots c
JOIN (
  SELECT channel_id,MAX(captured_at) AS captured_at
  FROM channel_snapshots
  WHERE instance_id=?
  GROUP BY channel_id
) latest
  ON latest.channel_id=c.channel_id AND latest.captured_at=c.captured_at
WHERE c.instance_id=?`

func (s Store) InsertRecommendation(r tuning.Recommendation) error {
	ev, _ := json.Marshal(r.Evidence)
	_, e := s.db.ExecContext(context.Background(), `INSERT INTO tuning_recommendations(id,instance_id,channel_id,channel_name,created_at,rule,evidence_json,current_weight,proposed_weight,current_priority,proposed_priority,mode_at_creation,status,command_id) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?)`, r.ID, r.InstanceID, r.ChannelID, r.ChannelName, r.CreatedAt, r.Rule, string(ev), r.CurrentWeight, r.ProposedWeight, r.CurrentPriority, r.ProposedPriority, r.ModeAtCreation, r.Status, r.CommandID)
	return e
}

func parseChannelModels(raw string) []string {
	var models []string
	if json.Unmarshal([]byte(raw), &models) != nil {
		models = strings.FieldsFunc(raw, func(r rune) bool { return r == ',' })
	}
	seen := map[string]bool{}
	out := make([]string, 0, len(models))
	for _, model := range models {
		model = strings.TrimSpace(model)
		if model != "" && !seen[model] {
			seen[model] = true
			out = append(out, model)
		}
	}
	return out
}

const tuningP95BucketsSQL = `SELECT p95_use_time FROM metric_1m WHERE instance_id=? AND dimension_type='instance_channel' AND dimension_key=CONCAT(?,':channel:',?) AND bucket_time>=? AND bucket_time<? AND request_count>=? AND p95_use_time IS NOT NULL ORDER BY bucket_time`

func (s Store) QueryP95Buckets(id string, channelID int64, start, end time.Time, minSamples int64) ([]float64, error) {
	rows, err := s.db.QueryContext(context.Background(), tuningP95BucketsSQL, id, id, channelID, start, end, minSamples)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []float64
	for rows.Next() {
		var value float64
		if err = rows.Scan(&value); err != nil {
			return nil, err
		}
		out = append(out, value)
	}
	return out, rows.Err()
}

func (s Store) HasRecentRecommendation(id string, channelID int64, rule string, since time.Time) (bool, error) {
	var found int
	err := s.db.QueryRowContext(context.Background(), `SELECT EXISTS(SELECT 1 FROM tuning_recommendations WHERE instance_id=? AND channel_id=? AND rule=? AND created_at>=?)`, id, channelID, rule, since).Scan(&found)
	return found == 1, err
}

func (s Store) CountActionRecommendations(id string, channelID int64, since time.Time) (int, error) {
	var count int
	err := s.db.QueryRowContext(context.Background(), `SELECT COUNT(*) FROM tuning_recommendations WHERE instance_id=? AND channel_id=? AND rule IN ('demote','trial') AND created_at>=?`, id, channelID, since).Scan(&count)
	return count, err
}

func (s Store) LastActionRecommendationAt(id string, channelID int64) (time.Time, bool, error) {
	var value sql.NullTime
	err := s.db.QueryRowContext(context.Background(), `SELECT MAX(created_at) FROM tuning_recommendations WHERE instance_id=? AND channel_id=? AND rule IN ('demote','trial')`, id, channelID).Scan(&value)
	if err != nil || !value.Valid {
		return time.Time{}, false, err
	}
	return value.Time, true, nil
}

func (s Store) ListDispatchStates(id string) ([]tuning.DispatchState, error) {
	rows, err := s.db.QueryContext(context.Background(), `SELECT instance_id,channel_id,model_name,original_priority,demoted_at,trial_attempts,next_trial_at,updated_at FROM tuning_dispatch_states WHERE instance_id=?`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []tuning.DispatchState
	for rows.Next() {
		var state tuning.DispatchState
		var next sql.NullTime
		if err = rows.Scan(&state.InstanceID, &state.ChannelID, &state.ModelName, &state.OriginalPriority, &state.DemotedAt, &state.TrialAttempts, &next, &state.UpdatedAt); err != nil {
			return nil, err
		}
		if next.Valid {
			state.NextTrialAt = &next.Time
		}
		out = append(out, state)
	}
	return out, rows.Err()
}

func (s Store) PutDispatchState(state tuning.DispatchState) error {
	_, err := s.db.ExecContext(context.Background(), `INSERT INTO tuning_dispatch_states(instance_id,channel_id,model_name,original_priority,demoted_at,trial_attempts,next_trial_at,updated_at) VALUES(?,?,?,?,?,?,?,?) ON DUPLICATE KEY UPDATE model_name=VALUES(model_name),original_priority=VALUES(original_priority),demoted_at=VALUES(demoted_at),trial_attempts=VALUES(trial_attempts),next_trial_at=VALUES(next_trial_at),updated_at=VALUES(updated_at)`, state.InstanceID, state.ChannelID, state.ModelName, state.OriginalPriority, state.DemotedAt, state.TrialAttempts, state.NextTrialAt, state.UpdatedAt)
	return err
}

func (s Store) DeleteDispatchState(id string, channelID int64) error {
	_, err := s.db.ExecContext(context.Background(), `DELETE FROM tuning_dispatch_states WHERE instance_id=? AND channel_id=?`, id, channelID)
	return err
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
	rows, e := s.db.QueryContext(context.Background(), `SELECT `+recommendationColumns+` FROM tuning_recommendations WHERE outcome_at IS NULL AND rule IN ('demote','trial') AND created_at<=? ORDER BY created_at LIMIT ?`, before, limit)
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
	rows, e := s.db.QueryContext(context.Background(), `SELECT rule,COUNT(*),SUM(outcome_at IS NOT NULL),SUM(hit IS NOT NULL),SUM(hit=1) FROM tuning_recommendations WHERE instance_id=? AND rule IN ('demote','trial') AND created_at>=? GROUP BY rule`, q.InstanceID, since)
	if e != nil {
		return r, e
	}
	defer rows.Close()
	for rows.Next() {
		var rule string
		var count, filled, judged, hits sql.NullInt64
		if e = rows.Scan(&rule, &count, &filled, &judged, &hits); e != nil {
			return r, e
		}
		r.ByRule[rule] = count.Int64
		r.Total += count.Int64
		r.Filled += filled.Int64
		r.Judged += judged.Int64
		r.Hits += hits.Int64
	}
	return r, rows.Err()
}
