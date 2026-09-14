package mysqlstore

import (
	"context"
	"crypto/sha1"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"time"

	"controltower/server/internal/storage"
)

type circuitEvent struct {
	ID, SiteID, InstanceID, Name, Rule string
	ChannelID                          int64
	At                                 time.Time
	Evidence                           string
}

func circuitEventAlert(e circuitEvent) storage.Alert {
	hash := sha1.Sum([]byte("circuit:" + e.ID))
	a := storage.Alert{ID: hex.EncodeToString(hash[:]), InstanceID: e.InstanceID,
		RuleKey: "channel_" + e.Rule, Status: "firing", Severity: "critical",
		Title: "渠道已熔断", FirstSeenAt: e.At, LastSeenAt: e.At}
	action := "自动熔断已执行，渠道权重和优先级已置零。"
	if e.Rule == "circuit_recovered" {
		a.Status, a.Severity, a.Title = "resolved", "info", "渠道熔断已恢复"
		a.ResolvedAt = &e.At
		action = "恢复写入已执行，渠道进入恢复/软启动流程。"
	}
	a.Summary = fmt.Sprintf("站点：%s\n渠道：%s（ID %d）\n%s", e.SiteID, e.Name, e.ChannelID, action)
	// Only known diagnostic fields are included, never raw error payloads or credentials.
	var evidence map[string]any
	if json.Unmarshal([]byte(e.Evidence), &evidence) == nil {
		if model, ok := evidence["model"].(string); ok {
			a.Summary += "\n模型：" + model
		}
		if trigger, ok := evidence["trigger"].(string); ok && trigger == "agent_report_batch" {
			a.Summary += "\n触发方式：上报批次快速熔断"
		}
		for _, field := range []struct{ key, label string }{{"error_rate", "错误率"}, {"smoothed_error_rate", "平滑错误率"}, {"threshold", "阈值"}} {
			if value, ok := evidence[field.key].(float64); ok {
				a.Summary += fmt.Sprintf("\n%s：%.1f%%", field.label, value*100)
			}
		}
	}
	return a
}

// Import confirmed execution events atomically. The event ledger survives alert
// cleanup and process restarts, so an old event can never recreate a notification.
func (s Store) SyncCircuitAlerts(now time.Time) error {
	ctx := context.Background()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var activated time.Time
	if err = tx.QueryRowContext(ctx, "SELECT occurred_at FROM circuit_alert_events WHERE event_id='__activation__' FOR UPDATE").Scan(&activated); err != nil {
		return err
	}
	rows, err := tx.QueryContext(ctx, `SELECT r.id,r.instance_id,c.instance_id,r.channel_id,r.channel_name,r.rule,r.created_at,r.evidence_json
 FROM tuning_recommendations r
 JOIN channel_commands c ON c.id=r.command_id AND c.status='succeeded'
 JOIN instances i ON i.id=c.instance_id AND i.deleted=0
 LEFT JOIN circuit_alert_events e ON e.event_id=r.id
 WHERE e.event_id IS NULL AND r.created_at>=? AND r.created_at<=?
 AND r.mode_at_creation='auto' AND r.status='auto_executed'
 AND r.rule IN ('circuit_opened','circuit_recovered')
 AND (CASE WHEN i.site_id='' THEN i.id ELSE i.site_id END)=r.instance_id
 ORDER BY r.created_at,r.id LIMIT 500`, activated, now)
	if err != nil {
		return err
	}
	var events []circuitEvent
	for rows.Next() {
		var e circuitEvent
		if err = rows.Scan(&e.ID, &e.SiteID, &e.InstanceID, &e.ChannelID, &e.Name, &e.Rule, &e.At, &e.Evidence); err != nil {
			rows.Close()
			return err
		}
		events = append(events, e)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	for _, e := range events {
		a := circuitEventAlert(e)
		if e.Rule == "circuit_opened" {
			// A delayed command acknowledgement may arrive after its recovery event.
			var recovered sql.NullTime
			if err = tx.QueryRowContext(ctx, `SELECT MIN(occurred_at) FROM circuit_alert_events
    WHERE site_id=? AND channel_id=? AND rule_key='channel_circuit_recovered' AND occurred_at>=?`, e.SiteID, e.ChannelID, e.At).Scan(&recovered); err != nil {
				return err
			}
			if recovered.Valid {
				a.Status = "resolved"
				a.ResolvedAt = &recovered.Time
			}
		}
		if _, err = tx.ExecContext(ctx, `INSERT INTO alerts
   (id,instance_id,rule_key,severity,status,title,summary,first_seen_at,last_seen_at,resolved_at)
   VALUES(?,?,?,?,?,?,?,?,?,?)`, a.ID, a.InstanceID, a.RuleKey, a.Severity, a.Status, a.Title, a.Summary, a.FirstSeenAt, a.LastSeenAt, a.ResolvedAt); err != nil {
			return err
		}
		if _, err = tx.ExecContext(ctx, `INSERT INTO circuit_alert_events(event_id,alert_id,site_id,channel_id,rule_key,occurred_at) VALUES(?,?,?,?,?,?)`, e.ID, a.ID, e.SiteID, e.ChannelID, a.RuleKey, e.At); err != nil {
			return err
		}
		if _, err = tx.ExecContext(ctx, `INSERT INTO alert_events(alert_id,event_type,actor,note,created_at) VALUES(?,?,'system',?,?)`, a.ID, a.Status, "调权执行事件 "+e.ID, e.At); err != nil {
			return err
		}
		if e.Rule == "circuit_recovered" {
			if _, err = tx.ExecContext(ctx, `INSERT INTO alert_events(alert_id,event_type,actor,note,created_at)
    SELECT a.id,'resolved','system','渠道恢复写入成功',? FROM alerts a JOIN circuit_alert_events e ON e.alert_id=a.id
    WHERE e.site_id=? AND e.channel_id=? AND e.rule_key='channel_circuit_opened' AND e.occurred_at<=? AND a.status<>'resolved'`, e.At, e.SiteID, e.ChannelID, e.At); err != nil {
				return err
			}
			if _, err = tx.ExecContext(ctx, `UPDATE alerts a JOIN circuit_alert_events e ON e.alert_id=a.id
    SET a.status='resolved',a.resolved_at=?,a.silence_until=NULL
    WHERE e.site_id=? AND e.channel_id=? AND e.rule_key='channel_circuit_opened' AND e.occurred_at<=? AND a.status<>'resolved'`, e.At, e.SiteID, e.ChannelID, e.At); err != nil {
				return err
			}
		}
	}
	return tx.Commit()
}

// Only events with at least one matching, due recipient are returned. Successful
// events cannot starve older retries, and recovery has its own delivery identity.
func (s Store) CircuitNotificationAlerts(now time.Time) ([]storage.Alert, error) {
	rows, err := s.db.QueryContext(context.Background(), `SELECT a.id,a.instance_id,a.rule_key,a.severity,a.status,a.title,a.summary,a.first_seen_at,a.last_seen_at,a.resolved_at
 FROM alerts a JOIN circuit_alert_events e ON e.alert_id=a.id
 JOIN instances i ON i.id=a.instance_id AND i.deleted=0
 WHERE a.status IN ('firing','resolved')
 AND (CASE WHEN i.site_id='' THEN i.id ELSE i.site_id END)=e.site_id AND EXISTS (
  SELECT 1 FROM notification_channels n WHERE n.enabled=1 AND n.site_id=e.site_id
  AND (n.rule_keys IS NULL OR JSON_LENGTH(n.rule_keys)=0 OR JSON_CONTAINS(n.rule_keys,JSON_QUOTE(a.rule_key)))
  AND NOT EXISTS (SELECT 1 FROM notification_deliveries d WHERE d.alert_id=a.id AND d.channel_id=n.id
   AND (d.status IN ('sent','exhausted') OR d.next_attempt_at>?))
 ) ORDER BY e.occurred_at,e.event_id LIMIT 500`, now)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var alerts []storage.Alert
	for rows.Next() {
		var a storage.Alert
		var resolved sql.NullTime
		if err = rows.Scan(&a.ID, &a.InstanceID, &a.RuleKey, &a.Severity, &a.Status, &a.Title, &a.Summary, &a.FirstSeenAt, &a.LastSeenAt, &resolved); err != nil {
			return nil, err
		}
		if resolved.Valid {
			a.ResolvedAt = &resolved.Time
		}
		alerts = append(alerts, a)
	}
	return alerts, rows.Err()
}
