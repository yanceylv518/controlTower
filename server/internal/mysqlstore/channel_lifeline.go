package mysqlstore

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"controltower/server/internal/tuning"
)

// Union a request with its existing command/decision, rather than interpreting
// recommendations as executed writes. Only whitelisted channel fields leave SQL.
func (s Store) QueryChannelLifeline(ctx context.Context, q tuning.ProbeRecordQuery, source string) (tuning.ChannelLifePage, error) {
	out := tuning.ChannelLifePage{Items: []tuning.ChannelLifeEvent{}}
	if _, err := s.retainChannelHistory(ctx, &q); err != nil {
		return out, fmt.Errorf("lifeline retention: %w", err)
	}
	owners, err := s.channelHistoryOwners(ctx, q.Site)
	if err != nil {
		return out, fmt.Errorf("lifeline instances: %w", err)
	}
	query, args, err := channelLifelineSQL(q, source, owners)
	if err != nil {
		return out, err
	}
	if query == "" {
		return out, nil
	}
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return out, fmt.Errorf("lifeline events query: %w", err)
	}
	defer rows.Close()
	limit := max(1, min(q.Limit, 200))
	for rows.Next() {
		var v tuning.ChannelLifeEvent
		var ended sql.NullTime
		var details []byte
		if err = rows.Scan(&v.ID, &v.Source, &v.At, &ended, &v.Operation, &v.Outcome, &v.Actor, &v.CommandID, &v.RecommendationID, &details); err != nil {
			return out, err
		}
		if ended.Valid {
			v.FinishedAt = &ended.Time
		}
		if err = json.Unmarshal(details, &v.Details); err != nil {
			return out, err
		}
		out.Items = append(out.Items, v)
	}
	if len(out.Items) > limit {
		out.Items = out.Items[:limit]
		v := out.Items[limit-1]
		out.NextCursor = encodeProbeCursor(v.At, v.ID)
	}
	return out, rows.Err()
}

func channelLifelineSQL(q tuning.ProbeRecordQuery, source string, owners []any) (string, []any, error) {
	ownerSQL := strings.TrimSuffix(strings.Repeat("?,", len(owners)), ",")
	parts := []string{}
	keys := []lifelineKey{}
	args := []any{}
	if source == "all" || source == "important" || source == "probes" {
		sqlSource, a := probeRecordSource(q)
		part := `SELECT CONCAT('probe:',id) id,'probe' source,started_at at,finished_at,CONCAT('probe.',kind) operation,outcome,'system:ct' actor,'' command_id,'' recommendation_id,JSON_OBJECT('model',model_name,'duration_seconds',duration_seconds,'reason',reason,'deadline_at',DATE_FORMAT(deadline_at,'%Y-%m-%dT%H:%i:%s.%fZ')) details FROM ` + sqlSource
		if source == "important" {
			part += " WHERE outcome NOT IN ('success','pending')"
		}
		parts = append(parts, part)
		keys = append(keys, lifelineKey{})
		args = append(args, a...)
	}
	if source == "all" || source == "important" || source == "operations" || source == "changes" {
		parts = append(parts, `SELECT CONCAT('request:',j.id) id,'operation' source,j.started_at at,j.finished_at,j.operation,CASE WHEN j.outcome='pending' AND j.started_at<? THEN 'unconfirmed' ELSE j.outcome END outcome,j.actor,COALESCE(NULLIF(j.command_id,''),(SELECT command_id FROM tuning_recommendations WHERE id=j.recommendation_id),'') command_id,j.recommendation_id,JSON_OBJECT('target',JSON_EXTRACT(j.payload_json,'$.target'),'before',JSON_EXTRACT(j.payload_json,'$.before'),'reason',j.reason,'duration_seconds',j.duration_seconds,'execution','ct_direct') details FROM channel_control_requests j FORCE INDEX(idx_control_channel_time) WHERE j.instance_id=? AND j.channel_id=? AND j.started_at>=? AND j.started_at<?`)
		keys = append(keys, lifelineKey{"request:", "j.started_at", "j.id"})
		args = append(args, q.Now.Add(-15*time.Second), q.Site, q.ChannelID, q.From, q.To)
		// Select eligible keys before fetching payloads or recommendation details.
		// CASE guarantees direct-request duplicates never evaluate the expensive
		// latest-recommendation lookup, regardless of the optimizer's join order.
		candidate := `SELECT c.id,c.created_at FROM channel_commands c FORCE INDEX(idx_command_site_channel) WHERE c.site_id IN (` + ownerSQL + `) AND c.channel_id=? AND c.created_at>=? AND c.created_at<? AND CASE
          WHEN EXISTS(SELECT 1 FROM channel_control_requests j WHERE j.instance_id=? AND j.command_id=c.id) THEN 0
          ELSE NOT EXISTS(SELECT 1 FROM channel_control_requests j WHERE j.instance_id=? AND j.recommendation_id<>'' AND j.recommendation_id=(SELECT r2.id FROM tuning_recommendations r2 WHERE r2.command_id=c.id ORDER BY r2.created_at DESC,r2.id DESC LIMIT 1)) END=1`
		parts = append(parts, candidate)
		keys = append(keys, lifelineKey{"command:", "c.created_at", "c.id"})

		args = append(args, owners...)
		args = append(args, q.ChannelID, q.From, q.To, q.Site, q.Site)
		// Unlinked channel audits cover edits with no channel command (e.g. base
		// values). Audit and command IDs normally share the same correlation.
		parts = append(parts, `SELECT CONCAT('audit:',a.id) id,'audit' source,a.created_at at,a.updated_at finished_at,a.operation_type operation,a.status outcome,a.actor_id actor,'' command_id,'' recommendation_id,JSON_OBJECT('before',JSON_OBJECT('weight',JSON_EXTRACT(IF(JSON_VALID(a.before_summary),a.before_summary,'{}'),'$.weight'),'priority',JSON_EXTRACT(IF(JSON_VALID(a.before_summary),a.before_summary,'{}'),'$.priority'),'group',JSON_EXTRACT(IF(JSON_VALID(a.before_summary),a.before_summary,'{}'),'$.group')),'target',JSON_OBJECT('weight',JSON_EXTRACT(IF(JSON_VALID(a.after_summary),a.after_summary,'{}'),'$.weight'),'priority',JSON_EXTRACT(IF(JSON_VALID(a.after_summary),a.after_summary,'{}'),'$.priority'),'group',JSON_EXTRACT(IF(JSON_VALID(a.after_summary),a.after_summary,'{}'),'$.group')),'execution','audit') details FROM operation_audits a FORCE INDEX(idx_audit_site_channel) WHERE a.site_id IN (`+ownerSQL+`) AND a.target_type='channel' AND a.target_id=? AND a.created_at>=? AND a.created_at<? AND NOT EXISTS (SELECT 1 FROM channel_commands c WHERE c.id=a.id) AND NOT EXISTS (SELECT 1 FROM channel_commands c WHERE a.correlation_id<>'' AND c.id=a.correlation_id)`)
		args = append(args, owners...)
		args = append(args, fmt.Sprint(q.ChannelID), q.From, q.To)
		keys = append(keys, lifelineKey{"audit:", "a.created_at", "a.id"})
	}
	if source == "all" || source == "important" || source == "decisions" || source == "changes" {
		parts = append(parts, `SELECT CONCAT('decision:',r.id) id,'decision' source,r.created_at at,r.outcome_at finished_at,r.rule operation,r.status outcome,COALESCE(NULLIF(r.acted_by,''),'system:ct') actor,COALESCE(r.command_id,'') command_id,r.id recommendation_id,JSON_OBJECT('before',JSON_OBJECT('weight',r.current_weight,'priority',r.current_priority),'target',JSON_OBJECT('weight',r.proposed_weight,'priority',r.proposed_priority),'rule',r.rule,'mode',r.mode_at_creation) details FROM tuning_recommendations r FORCE INDEX(idx_tuning_channel_created) WHERE r.instance_id=? AND r.channel_id=? AND r.created_at>=? AND r.created_at<? AND (r.command_id IS NULL OR r.command_id='') AND NOT EXISTS(SELECT 1 FROM channel_control_requests j WHERE j.recommendation_id=r.id)`)
		args = append(args, q.Site, q.ChannelID, q.From, q.To)
		keys = append(keys, lifelineKey{"decision:", "r.created_at", "r.id"})
	}
	if len(parts) == 0 {
		return "", nil, nil
	}
	limit := max(1, min(q.Limit, 200))
	boundedArgs := []any{}
	offset := 0
	for i, part := range parts {
		n := strings.Count(part, "?")
		boundedArgs = append(boundedArgs, args[offset:offset+n]...)
		offset += n
		key := keys[i]
		if key.prefix == "" {
			// Probe sources retain their existing derived-table normalization.
			part = "SELECT * FROM (" + part + ") source_events WHERE 1=1"
			key = lifelineKey{"", "at", "id"}
		}
		predicate, cursorArgs, err := lifelineCursorPredicate(q.Cursor, key)
		if err != nil {
			return "", nil, err
		}
		boundedArgs = append(boundedArgs, cursorArgs...)
		boundedArgs = append(boundedArgs, limit+1)
		part += predicate + " ORDER BY " + key.at + " DESC," + key.id + " DESC LIMIT ?"
		if key.prefix == "command:" {
			part = `SELECT CONCAT('command:',c.id) id,'operation' source,c.created_at at,CASE WHEN c.status IN ('succeeded','failed','expired') THEN c.updated_at ELSE NULL END finished_at,c.command_type operation,c.status outcome,c.created_by actor,c.id command_id,COALESCE(r.id,'') recommendation_id,JSON_OBJECT('target',JSON_OBJECT('weight',JSON_EXTRACT(c.payload_json,'$.weight'),'priority',JSON_EXTRACT(c.payload_json,'$.priority'),'status',JSON_EXTRACT(c.payload_json,'$.status'),'group',JSON_EXTRACT(c.payload_json,'$.group'),'model',JSON_EXTRACT(c.payload_json,'$.model'),'count',JSON_EXTRACT(c.payload_json,'$.count')),'before',JSON_OBJECT('weight',r.current_weight,'priority',r.current_priority),'rule',r.rule,'mode',r.mode_at_creation,'execution',c.execution_kind,'executor_instance_id',IF(c.execution_kind='agent',c.instance_id,''),'ownership_evidence',c.ownership_evidence,'reason',IF(c.error_summary<>'','command_reported_error','')) details FROM (` + part + `) chosen
              JOIN channel_commands c ON c.id=chosen.id
              LEFT JOIN tuning_recommendations r ON r.id=(SELECT r2.id FROM tuning_recommendations r2 WHERE r2.command_id=c.id ORDER BY r2.created_at DESC,r2.id DESC LIMIT 1)`
		}
		parts[i] = "(" + part + ")"
	}
	args = append(boundedArgs, limit+1)
	return "SELECT * FROM (" + strings.Join(parts, " UNION ALL ") + ") events ORDER BY at DESC,id DESC LIMIT ?", args, nil
}

type lifelineKey struct{ prefix, at, id string }

// Inside one source the prefix is constant, so sort and page on its native ID.
// Across sources the prefix decides which rows at the cursor timestamp remain.
func lifelineCursorPredicate(cursor string, key lifelineKey) (string, []any, error) {
	if cursor == "" {
		return "", nil, nil
	}
	c, err := decodeProbeCursor(cursor)
	if err != nil {
		return "", nil, err
	}
	if key.prefix == "" {
		return " AND (" + key.at + "<? OR (" + key.at + "=? AND " + key.id + "<?))", []any{c.At, c.At, c.ID}, nil
	}
	prefix, id, ok := strings.Cut(c.ID, ":")
	if !ok {
		return "", nil, fmt.Errorf("invalid lifeline cursor event ID")
	}
	prefix += ":"
	if key.prefix < prefix {
		return " AND " + key.at + "<=?", []any{c.At}, nil
	}
	if key.prefix > prefix {
		return " AND " + key.at + "<?", []any{c.At}, nil
	}
	return " AND (" + key.at + "<? OR (" + key.at + "=? AND " + key.id + "<?))", []any{c.At, c.At, id}, nil
}
