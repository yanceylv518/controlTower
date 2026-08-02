package mysqlstore

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
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
	r.Policy, err = tuning.DecodePolicyJSON([]byte(raw))
	return r, true, err
}
func (s Store) PutPolicy(r tuning.PolicyRecord) error {
	b, _ := json.Marshal(r.Policy)
	_, e := s.db.ExecContext(context.Background(), `INSERT INTO tuning_policies(instance_id,policy_json,mode,updated_at,updated_by) VALUES(?,?,?,?,?) ON DUPLICATE KEY UPDATE policy_json=VALUES(policy_json),mode=VALUES(mode),updated_at=VALUES(updated_at),updated_by=VALUES(updated_by)`, r.InstanceID, string(b), r.Mode, r.UpdatedAt, r.UpdatedBy)
	return e
}

func (s Store) ListChannelBaseValues(instanceID, model string) ([]tuning.ChannelBaseValue, error) {
	args := []any{instanceID, instanceID, instanceID}
	filter := ""
	if model != "" {
		filter = " AND b.model_name=?"
		args = append(args, model)
	}
	rows, err := s.db.QueryContext(context.Background(), `SELECT b.instance_id,b.channel_id,c.channel_name,b.model_name,b.base_weight,b.base_priority,c.weight,COALESCE(c.priority,0),c.captured_at,COALESCE(c.models_text,''),b.updated_at,b.updated_by
FROM channel_base_values b
JOIN (SELECT cs.* FROM channel_current cs JOIN instances i ON i.id=cs.instance_id
      JOIN (SELECT cs2.channel_id,MAX(cs2.captured_at) captured_at FROM channel_current cs2 JOIN instances i2 ON i2.id=cs2.instance_id WHERE CASE WHEN i2.site_id='' THEN i2.id ELSE i2.site_id END=? AND i2.enabled=1 GROUP BY cs2.channel_id) x
        ON x.channel_id=cs.channel_id AND x.captured_at=cs.captured_at
      WHERE CASE WHEN i.site_id='' THEN i.id ELSE i.site_id END=? AND i.enabled=1) c ON c.channel_id=b.channel_id
WHERE b.instance_id=? AND LOWER(c.status) IN ('enabled','enable','active','normal','1')`+filter+` ORDER BY b.model_name,c.channel_name`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []tuning.ChannelBaseValue
	for rows.Next() {
		var v tuning.ChannelBaseValue
		var modelsText string
		if err = rows.Scan(&v.InstanceID, &v.ChannelID, &v.ChannelName, &v.ModelName, &v.BaseWeight, &v.BasePriority, &v.CurrentWeight, &v.CurrentPriority, &v.SnapshotAt, &modelsText, &v.UpdatedAt, &v.UpdatedBy); err != nil {
			return nil, err
		}
		v.Models = parseChannelModels(modelsText)
		out = append(out, v)
	}
	return out, rows.Err()
}

func (s Store) SaveChannelBaseValues(instanceID, actor string, values []tuning.ChannelBaseValue, now time.Time) error {
	tx, err := s.db.BeginTx(context.Background(), nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, v := range values {
		var beforeWeight, beforePriority sql.NullInt64
		_ = tx.QueryRow(`SELECT base_weight,base_priority FROM channel_base_values WHERE instance_id=? AND channel_id=?`, instanceID, v.ChannelID).Scan(&beforeWeight, &beforePriority)
		if _, err = tx.Exec(`INSERT INTO channel_base_values(instance_id,channel_id,model_name,base_weight,base_priority,updated_at,updated_by) VALUES(?,?,?,?,?,?,?) ON DUPLICATE KEY UPDATE model_name=VALUES(model_name),base_weight=VALUES(base_weight),base_priority=VALUES(base_priority),updated_at=VALUES(updated_at),updated_by=VALUES(updated_by)`, instanceID, v.ChannelID, v.ModelName, v.BaseWeight, v.BasePriority, now, actor); err != nil {
			return err
		}
		var beforeWeightValue, beforePriorityValue any
		if beforeWeight.Valid {
			beforeWeightValue = beforeWeight.Int64
		}
		if beforePriority.Valid {
			beforePriorityValue = beforePriority.Int64
		}
		before, _ := json.Marshal(map[string]any{"weight": beforeWeightValue, "priority": beforePriorityValue})
		after, _ := json.Marshal(map[string]any{"weight": v.BaseWeight, "priority": v.BasePriority, "model": v.ModelName})
		id := fmt.Sprintf("tbase-%d-%d", now.UnixNano(), v.ChannelID)
		if _, err = tx.Exec(`INSERT INTO operation_audits(id,instance_id,operation_type,target_type,target_id,actor_id,before_summary,after_summary,status,created_at) VALUES(?,?,?,?,?,?,?,?,?,?)`, id, instanceID, "tuning.base_update", "channel", fmt.Sprint(v.ChannelID), actor, string(before), string(after), "success", now); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (s Store) SyncChannelBaseValues(instanceID string, models []string) ([]tuning.ChannelBaseValue, error) {
	channels, err := s.LatestChannels(instanceID)
	if err != nil {
		return nil, err
	}
	wanted := map[string]bool{}
	for _, m := range models {
		wanted[m] = true
	}
	var out []tuning.ChannelBaseValue
	for _, c := range channels {
		// v3.0-B1 stores one model anchor per channel. Mixed-model channels are
		// deliberately excluded until the later dispatch engine defines their
		// ownership semantics; otherwise the (instance, channel) key would make
		// the last model silently overwrite the others.
		if len(c.Models) != 1 {
			continue
		}
		m := c.Models[0]
		if len(wanted) > 0 && !wanted[m] {
			continue
		}
		out = append(out, tuning.ChannelBaseValue{InstanceID: instanceID, ChannelID: c.ID, ChannelName: c.Name, ModelName: m, BaseWeight: c.Weight, BasePriority: c.Priority, CurrentWeight: c.Weight, CurrentPriority: c.Priority})
	}
	return out, nil
}
func (s Store) ListEnabledSites() ([]string, error) {
	rows, e := s.db.QueryContext(context.Background(), `SELECT DISTINCT CASE WHEN site_id='' THEN id ELSE site_id END FROM instances WHERE enabled=1`)
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
const tuningChannelMetricsSQL = `SELECT
CAST(SUBSTRING_INDEX(dimension_key,':',-1) AS SIGNED),
SUM(request_count),SUM(error_count),SUM(user_error_count),
COALESCE(MAX(p95_use_time),0),
COALESCE(MAX(ttft_p50_ms),0)/1000,
COALESCE(MAX(ttft_p90_ms),0)/1000,
COALESCE(MAX(ttft_p95_ms),0)/1000,
CASE WHEN SUM(cache_prompt_tokens)>0 THEN SUM(cache_tokens_total)/SUM(cache_prompt_tokens) ELSE 0 END,
CASE WHEN SUM(otps_duration_seconds)>0 THEN SUM(otps_output_tokens)/SUM(otps_duration_seconds) ELSE 0 END
FROM metric_1m
WHERE instance_id IN (SELECT id FROM instances WHERE enabled=1 AND CASE WHEN site_id='' THEN id ELSE site_id END=?) AND dimension_type='instance_channel' AND bucket_time>=? AND bucket_time<?
GROUP BY CAST(SUBSTRING_INDEX(dimension_key,':',-1) AS SIGNED)`

func (s Store) QueryMetrics(id string, start, end time.Time) ([]tuning.ChannelMetric, error) {
	rows, e := s.db.QueryContext(context.Background(), tuningChannelMetricsSQL, id, start, end)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	var out []tuning.ChannelMetric
	for rows.Next() {
		var m tuning.ChannelMetric
		if e = rows.Scan(&m.ChannelID, &m.RequestCount, &m.ErrorCount, &m.UserErrorCount, &m.P95, &m.TTFTP50, &m.TTFTP90, &m.TTFTP95, &m.CacheHitRate, &m.OTPS); e != nil {
			return nil, e
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

func (s Store) ListContinuousStates(id string) ([]tuning.ContinuousState, error) {
	rows, err := s.db.QueryContext(context.Background(), `SELECT instance_id,channel_id,model_name,k_error,k_speed,k_cache,k_otps,multiplier,proposed_weight,last_written_weight,last_write_at,last_observed_requests,last_observed_errors,metric_ready,baseline_ready,last_bucket_at,paused_reason,phase,circuit_opened_at,next_probe_at,probe_command_id,probe_attempts,probe_successes,probe_duration_sum,original_priority,soft_start_pending,updated_at FROM tuning_continuous_states WHERE instance_id=?`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []tuning.ContinuousState
	for rows.Next() {
		var v tuning.ContinuousState
		var written sql.NullInt64
		var writeAt, bucketAt, openedAt, nextProbeAt sql.NullTime
		var probeID sql.NullString
		var originalPriority sql.NullInt64
		if err = rows.Scan(&v.InstanceID, &v.ChannelID, &v.ModelName, &v.KError, &v.KSpeed, &v.KCache, &v.KOTPS, &v.Multiplier, &v.ProposedWeight, &written, &writeAt, &v.LastObservedRequests, &v.LastObservedErrors, &v.MetricReady, &v.BaselineReady, &bucketAt, &v.PausedReason, &v.Phase, &openedAt, &nextProbeAt, &probeID, &v.ProbeAttempts, &v.ProbeSuccesses, &v.ProbeDurationSum, &originalPriority, &v.SoftStartPending, &v.UpdatedAt); err != nil {
			return nil, err
		}
		if written.Valid {
			x := written.Int64
			v.LastWrittenWeight = &x
		}
		if writeAt.Valid {
			x := writeAt.Time
			v.LastWriteAt = &x
		}
		if bucketAt.Valid {
			x := bucketAt.Time
			v.LastBucketAt = &x
		}
		if openedAt.Valid {
			x := openedAt.Time
			v.CircuitOpenedAt = &x
		}
		if nextProbeAt.Valid {
			x := nextProbeAt.Time
			v.NextProbeAt = &x
		}
		if probeID.Valid {
			x := probeID.String
			v.ProbeCommandID = &x
		}
		if originalPriority.Valid {
			x := originalPriority.Int64
			v.OriginalPriority = &x
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

func (s Store) PutContinuousState(v tuning.ContinuousState) error {
	_, err := s.db.ExecContext(context.Background(), `INSERT INTO tuning_continuous_states(instance_id,channel_id,model_name,k_error,k_speed,k_cache,k_otps,multiplier,proposed_weight,last_written_weight,last_write_at,last_observed_requests,last_observed_errors,metric_ready,baseline_ready,last_bucket_at,paused_reason,phase,circuit_opened_at,next_probe_at,probe_command_id,probe_attempts,probe_successes,probe_duration_sum,original_priority,soft_start_pending,updated_at) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?) ON DUPLICATE KEY UPDATE model_name=VALUES(model_name),k_error=VALUES(k_error),k_speed=VALUES(k_speed),k_cache=VALUES(k_cache),k_otps=VALUES(k_otps),multiplier=VALUES(multiplier),proposed_weight=VALUES(proposed_weight),last_written_weight=VALUES(last_written_weight),last_write_at=VALUES(last_write_at),last_observed_requests=VALUES(last_observed_requests),last_observed_errors=VALUES(last_observed_errors),metric_ready=VALUES(metric_ready),baseline_ready=VALUES(baseline_ready),last_bucket_at=VALUES(last_bucket_at),paused_reason=VALUES(paused_reason),phase=VALUES(phase),circuit_opened_at=VALUES(circuit_opened_at),next_probe_at=VALUES(next_probe_at),probe_command_id=VALUES(probe_command_id),probe_attempts=VALUES(probe_attempts),probe_successes=VALUES(probe_successes),probe_duration_sum=VALUES(probe_duration_sum),original_priority=VALUES(original_priority),soft_start_pending=VALUES(soft_start_pending),updated_at=VALUES(updated_at)`, v.InstanceID, v.ChannelID, v.ModelName, v.KError, v.KSpeed, v.KCache, v.KOTPS, v.Multiplier, v.ProposedWeight, v.LastWrittenWeight, v.LastWriteAt, v.LastObservedRequests, v.LastObservedErrors, v.MetricReady, v.BaselineReady, v.LastBucketAt, v.PausedReason, v.Phase, v.CircuitOpenedAt, v.NextProbeAt, v.ProbeCommandID, v.ProbeAttempts, v.ProbeSuccesses, v.ProbeDurationSum, v.OriginalPriority, v.SoftStartPending, v.UpdatedAt)
	return err
}

func (s Store) CreateContinuousWeightChange(v tuning.Recommendation, actor string, now time.Time) (string, error) {
	tx, err := s.db.BeginTx(context.Background(), nil)
	if err != nil {
		return "", err
	}
	defer tx.Rollback()
	controlInstanceID, err := controlInstanceForSite(tx, v.InstanceID)
	if err != nil {
		return "", err
	}
	ev, _ := json.Marshal(v.Evidence)
	commandID := randomCommandID()
	payloadValues := map[string]any{"weight": v.ProposedWeight}
	if v.ProposedPriority != nil && (v.Rule == "circuit_opened" || v.Rule == "circuit_recovered") {
		payloadValues["priority"] = *v.ProposedPriority
	}
	payload, _ := json.Marshal(payloadValues)
	if _, err = tx.Exec(`INSERT INTO tuning_recommendations(id,instance_id,channel_id,channel_name,created_at,rule,evidence_json,current_weight,proposed_weight,current_priority,proposed_priority,mode_at_creation,status,command_id,acted_by,acted_at) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`, v.ID, v.InstanceID, v.ChannelID, v.ChannelName, v.CreatedAt, v.Rule, string(ev), v.CurrentWeight, v.ProposedWeight, v.CurrentPriority, v.ProposedPriority, v.ModeAtCreation, "auto_executed", commandID, actor, now); err != nil {
		return "", err
	}
	if _, err = tx.Exec(`INSERT INTO channel_commands(id,instance_id,channel_id,command_type,payload_json,status,created_by,error_summary,created_at,updated_at) VALUES(?,?,?,?,?,'pending',?,'',?,?)`, commandID, controlInstanceID, v.ChannelID, "channel.update", string(payload), actor, now, now); err != nil {
		return "", err
	}
	before := fmt.Sprintf(`{"weight":%d}`, v.CurrentWeight)
	after := fmt.Sprintf(`{"weight":%d,"command_id":%q}`, v.ProposedWeight, commandID)
	if _, err = tx.Exec(`INSERT INTO operation_audits(id,instance_id,operation_type,target_type,target_id,actor_id,before_summary,after_summary,status,created_at) VALUES(?,?,?,?,?,?,?,?,?,?)`, commandID, v.InstanceID, "tuning.auto_execute", "channel", fmt.Sprint(v.ChannelID), actor, before, after, "success", now); err != nil {
		return "", err
	}
	return commandID, tx.Commit()
}

func (s Store) CreateContinuousProbe(v tuning.Recommendation, model string, count, interval int, now time.Time) (string, error) {
	tx, err := s.db.BeginTx(context.Background(), nil)
	if err != nil {
		return "", err
	}
	defer tx.Rollback()
	controlInstanceID, err := controlInstanceForSite(tx, v.InstanceID)
	if err != nil {
		return "", err
	}
	commandID := randomCommandID()
	payload, _ := json.Marshal(map[string]any{"model": model, "probe_count": count, "probe_interval_seconds": interval})
	_, err = tx.Exec(`INSERT INTO channel_commands(id,instance_id,channel_id,command_type,payload_json,status,created_by,error_summary,created_at,updated_at) VALUES(?,?,?,?,?,'pending','system:auto','',?,?)`, commandID, controlInstanceID, v.ChannelID, "channel.probe", string(payload), now, now)
	if err != nil {
		return "", err
	}
	v.Rule, v.Status, v.CommandID = "probe_started", "auto_executed", &commandID
	ev, _ := json.Marshal(v.Evidence)
	if _, err = tx.Exec(`INSERT INTO tuning_recommendations(id,instance_id,channel_id,channel_name,created_at,rule,evidence_json,current_weight,proposed_weight,current_priority,proposed_priority,mode_at_creation,status,command_id,acted_by,acted_at) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`, v.ID, v.InstanceID, v.ChannelID, v.ChannelName, v.CreatedAt, v.Rule, string(ev), v.CurrentWeight, v.ProposedWeight, v.CurrentPriority, v.ProposedPriority, v.ModeAtCreation, v.Status, commandID, "system:auto", now); err != nil {
		return "", err
	}
	if err = tx.Commit(); err != nil {
		return "", err
	}
	return commandID, nil
}

func (s Store) RecordContinuousProbeResult(instanceID string, channelID int64, commandID string, attempts, successes int, duration float64, now time.Time) error {
	_, err := s.db.ExecContext(context.Background(), `UPDATE tuning_continuous_states SET probe_command_id=NULL,probe_attempts=?,probe_successes=?,probe_duration_sum=?,updated_at=? WHERE channel_id=? AND probe_command_id=?`, attempts, successes, duration, now, channelID, commandID)
	return err
}

type tuningCommandTx interface {
	QueryRow(query string, args ...any) *sql.Row
}

func controlInstanceForSite(tx tuningCommandTx, siteID string) (string, error) {
	var id string
	err := tx.QueryRow(`SELECT i.id
FROM instances i
WHERE i.enabled=1 AND CASE WHEN i.site_id='' THEN i.id ELSE i.site_id END=?
ORDER BY (SELECT MAX(cs.captured_at) FROM channel_current cs WHERE cs.instance_id=i.id) DESC,
         (SELECT MAX(a.last_seen_at) FROM agents a WHERE a.instance_id=i.id) DESC,
         i.id ASC
LIMIT 1`, siteID).Scan(&id)
	if err == sql.ErrNoRows {
		return "", fmt.Errorf("no enabled control instance for site %s", siteID)
	}
	return id, err
}

func randomCommandID() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

const tuningRecentChannelBucketsSQL = `SELECT
bucket_time,SUM(request_count),SUM(error_count),SUM(user_error_count)
FROM metric_1m
WHERE instance_id IN (SELECT id FROM instances WHERE enabled=1 AND CASE WHEN site_id='' THEN id ELSE site_id END=?) AND dimension_type='instance_channel'
  AND CAST(SUBSTRING_INDEX(dimension_key,':',-1) AS SIGNED)=? AND bucket_time>=? AND request_count>0
GROUP BY bucket_time
ORDER BY bucket_time DESC
LIMIT ?`

func (s Store) QueryRecentChannelBuckets(id string, channelID int64, since time.Time, limit int) ([]tuning.RecentChannelBucket, error) {
	rows, err := s.db.QueryContext(context.Background(), tuningRecentChannelBucketsSQL, id, channelID, since, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []tuning.RecentChannelBucket
	for rows.Next() {
		var bucket tuning.RecentChannelBucket
		if err = rows.Scan(&bucket.BucketTime, &bucket.RequestCount, &bucket.ErrorCount, &bucket.UserErrorCount); err != nil {
			return nil, err
		}
		out = append(out, bucket)
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
FROM channel_current c
JOIN (
  SELECT channel_id,MAX(captured_at) AS captured_at
  FROM channel_current cs2 JOIN instances i2 ON i2.id=cs2.instance_id
  WHERE i2.enabled=1 AND CASE WHEN i2.site_id='' THEN i2.id ELSE i2.site_id END=?
  GROUP BY channel_id
) latest
  ON latest.channel_id=c.channel_id AND latest.captured_at=c.captured_at
JOIN instances i ON i.id=c.instance_id
WHERE i.enabled=1 AND CASE WHEN i.site_id='' THEN i.id ELSE i.site_id END=?`

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

func (s Store) HasExpiredAutoCommands(id string, since time.Time) (bool, error) {
	var found int
	err := s.db.QueryRowContext(context.Background(), `SELECT EXISTS(SELECT 1 FROM channel_commands WHERE instance_id IN (SELECT id FROM instances WHERE CASE WHEN site_id='' THEN id ELSE site_id END=?) AND created_by='system:auto' AND status='expired' AND updated_at>=?)`, id, since).Scan(&found)
	return found == 1, err
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
		var actedAt sql.NullTime
		if e := rows.Scan(&r.ID, &r.InstanceID, &r.ChannelID, &r.ChannelName, &r.CreatedAt, &r.Rule, &ev, &r.CurrentWeight, &r.ProposedWeight, &cp, &pp, &r.ModeAtCreation, &r.Status, &command, &outcome, &outcomeAt, &hit, &r.ActedBy, &actedAt); e != nil {
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
		if actedAt.Valid {
			r.ActedAt = &actedAt.Time
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

const recommendationColumns = `id,instance_id,channel_id,channel_name,created_at,rule,evidence_json,current_weight,proposed_weight,current_priority,proposed_priority,mode_at_creation,status,command_id,outcome_json,outcome_at,hit,acted_by,acted_at`

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
	query := `SELECT ` + recommendationColumns + ` FROM tuning_recommendations WHERE instance_id=? AND created_at<?`
	args := []any{q.InstanceID, before}
	if q.Rule != "" {
		query += ` AND rule=?`
		args = append(args, q.Rule)
	}
	query += ` ORDER BY created_at DESC LIMIT ?`
	args = append(args, q.Limit)
	rows, e := s.db.QueryContext(context.Background(), query, args...)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	return scanRecommendation(rows)
}
func (s Store) RecommendationReport(q tuning.RecommendationQuery) (tuning.Report, error) {
	r := tuning.Report{ByRule: map[string]int64{}}
	since := time.Now().UTC().Add(-time.Duration(q.Days) * 24 * time.Hour)
	rows, e := s.db.QueryContext(context.Background(), `SELECT rule,COUNT(*) FROM tuning_recommendations WHERE instance_id=? AND created_at>=? GROUP BY rule`, q.InstanceID, since)
	if e != nil {
		return r, e
	}
	defer rows.Close()
	for rows.Next() {
		var rule string
		var count int64
		if e = rows.Scan(&rule, &count); e != nil {
			return r, e
		}
		r.ByRule[rule] = count
		r.Total += count
	}
	return r, rows.Err()
}
