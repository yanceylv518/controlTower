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
		return out, err
	}
	parts := []string{}
	args := []any{}
	if source == "all" || source == "important" || source == "probes" {
		sqlSource, a := probeRecordSource(q)
		part := `SELECT CONCAT('probe:',id) id,'probe' source,started_at at,finished_at,CONCAT('probe.',kind) operation,outcome,'system:ct' actor,'' command_id,'' recommendation_id,JSON_OBJECT('model',model_name,'duration_seconds',duration_seconds,'reason',reason,'deadline_at',DATE_FORMAT(deadline_at,'%Y-%m-%dT%H:%i:%s.%fZ')) details FROM ` + sqlSource
		if source == "important" {
			part += " WHERE outcome NOT IN ('success','pending')"
		}
		parts = append(parts, part)
		args = append(args, a...)
	}
	if source == "all" || source == "important" || source == "operations" {
		parts = append(parts, `SELECT CONCAT('request:',j.id) id,'operation' source,j.started_at at,j.finished_at,j.operation,CASE WHEN j.outcome='pending' AND j.started_at<? THEN 'unconfirmed' ELSE j.outcome END outcome,j.actor,COALESCE(NULLIF(j.command_id,''),(SELECT command_id FROM tuning_recommendations WHERE id=j.recommendation_id),'') command_id,j.recommendation_id,JSON_OBJECT('target',JSON_EXTRACT(j.payload_json,'$.target'),'before',JSON_EXTRACT(j.payload_json,'$.before'),'reason',j.reason,'duration_seconds',j.duration_seconds,'execution','ct_direct') details FROM channel_control_requests j WHERE j.instance_id=? AND j.channel_id=? AND j.started_at>=? AND j.started_at<?`)
		args = append(args, q.Now.Add(-15*time.Second), q.Site, q.ChannelID, q.From, q.To)
		parts = append(parts, `SELECT CONCAT('command:',c.id) id,'operation' source,c.created_at at,CASE WHEN c.status IN ('succeeded','failed','expired') THEN c.updated_at ELSE NULL END finished_at,c.command_type operation,c.status outcome,c.created_by actor,c.id command_id,COALESCE(r.id,'') recommendation_id,JSON_OBJECT('target',JSON_OBJECT('weight',JSON_EXTRACT(c.payload_json,'$.weight'),'priority',JSON_EXTRACT(c.payload_json,'$.priority'),'status',JSON_EXTRACT(c.payload_json,'$.status'),'group',JSON_EXTRACT(c.payload_json,'$.group'),'model',JSON_EXTRACT(c.payload_json,'$.model'),'count',JSON_EXTRACT(c.payload_json,'$.count')),'before',JSON_OBJECT('weight',r.current_weight,'priority',r.current_priority),'rule',r.rule,'mode',r.mode_at_creation,'execution','command','reason',IF(c.error_summary<>'','command_reported_error','')) details FROM channel_commands c LEFT JOIN tuning_recommendations r ON r.id=(SELECT r2.id FROM tuning_recommendations r2 WHERE r2.command_id=c.id ORDER BY r2.created_at DESC,r2.id DESC LIMIT 1) WHERE (c.instance_id=? OR c.instance_id IN (SELECT id FROM instances WHERE site_id=?)) AND c.channel_id=? AND c.created_at>=? AND c.created_at<? AND NOT EXISTS (SELECT 1 FROM channel_control_requests j WHERE j.instance_id=? AND (j.command_id=c.id OR (j.recommendation_id<>'' AND j.recommendation_id=r.id)))`)
		args = append(args, q.Site, q.Site, q.ChannelID, q.From, q.To, q.Site)
		// Unlinked channel audits cover edits with no channel command (e.g. base
		// values). Audit and command IDs normally share the same correlation.
		parts = append(parts, `SELECT CONCAT('audit:',a.id) id,'audit' source,a.created_at at,a.updated_at finished_at,a.operation_type operation,a.status outcome,a.actor_id actor,'' command_id,'' recommendation_id,JSON_OBJECT('before',JSON_OBJECT('weight',JSON_EXTRACT(IF(JSON_VALID(a.before_summary),a.before_summary,'{}'),'$.weight'),'priority',JSON_EXTRACT(IF(JSON_VALID(a.before_summary),a.before_summary,'{}'),'$.priority'),'group',JSON_EXTRACT(IF(JSON_VALID(a.before_summary),a.before_summary,'{}'),'$.group')),'target',JSON_OBJECT('weight',JSON_EXTRACT(IF(JSON_VALID(a.after_summary),a.after_summary,'{}'),'$.weight'),'priority',JSON_EXTRACT(IF(JSON_VALID(a.after_summary),a.after_summary,'{}'),'$.priority'),'group',JSON_EXTRACT(IF(JSON_VALID(a.after_summary),a.after_summary,'{}'),'$.group')),'execution','audit') details FROM operation_audits a WHERE (a.instance_id=? OR a.instance_id IN (SELECT id FROM instances WHERE site_id=?)) AND a.target_type='channel' AND a.target_id=? AND a.created_at>=? AND a.created_at<? AND NOT EXISTS (SELECT 1 FROM channel_commands c WHERE c.id=a.id OR (a.correlation_id<>'' AND c.id=a.correlation_id))`)
		args = append(args, q.Site, q.Site, fmt.Sprint(q.ChannelID), q.From, q.To)
	}
	if source == "all" || source == "important" || source == "decisions" {
		parts = append(parts, `SELECT CONCAT('decision:',r.id) id,'decision' source,r.created_at at,r.outcome_at finished_at,r.rule operation,r.status outcome,COALESCE(NULLIF(r.acted_by,''),'system:ct') actor,COALESCE(r.command_id,'') command_id,r.id recommendation_id,JSON_OBJECT('before',JSON_OBJECT('weight',r.current_weight,'priority',r.current_priority),'target',JSON_OBJECT('weight',r.proposed_weight,'priority',r.proposed_priority),'rule',r.rule,'mode',r.mode_at_creation) details FROM tuning_recommendations r WHERE r.instance_id=? AND r.channel_id=? AND r.created_at>=? AND r.created_at<? AND (r.command_id IS NULL OR r.command_id='') AND NOT EXISTS(SELECT 1 FROM channel_control_requests j WHERE j.recommendation_id=r.id)`)
		args = append(args, q.Site, q.ChannelID, q.From, q.To)
	}
	if len(parts) == 0 {
		return out, nil
	}
	where := ""
	if q.Cursor != "" {
		c, err := decodeProbeCursor(q.Cursor)
		if err != nil {
			return out, err
		}
		where = " WHERE (at<? OR (at=? AND id<?))"
		args = append(args, c.At, c.At, c.ID)
	}
	limit := max(1, min(q.Limit, 200))
	args = append(args, limit+1)
	rows, err := s.db.QueryContext(ctx, "SELECT * FROM ("+strings.Join(parts, " UNION ALL ")+") events"+where+" ORDER BY at DESC,id DESC LIMIT ?", args...)
	if err != nil {
		return out, err
	}
	defer rows.Close()
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
