package mysqlstore

import (
	"context"
	"controltower/server/internal/aggregator"
	"controltower/server/internal/storage"
	"database/sql"
	"strings"
	"time"
)

const overviewSourcesSQL = `SELECT i.id FROM instances i WHERE i.enabled=1 AND i.deleted=0 AND CASE WHEN i.site_id='' THEN i.id ELSE i.site_id END=?
 AND (EXISTS(SELECT 1 FROM agents a WHERE a.instance_id=i.id AND (a.source_latest_log_id>0 OR a.last_log_id>0))
 OR EXISTS(SELECT 1 FROM log_offsets o WHERE o.instance_id=i.id AND o.last_log_id>0)
 OR EXISTS(SELECT 1 FROM channel_rate_seconds r WHERE r.instance_id=i.id AND r.channel_id=0)
 OR EXISTS(SELECT 1 FROM overview_metric_sources s WHERE s.instance_id=i.id)) ORDER BY i.id`

func (s Store) OverviewCoverage(ctx context.Context, site string, from, to time.Time) ([]string, []storage.OverviewCoverage, error) {
	rows, err := s.db.QueryContext(ctx, overviewSourcesSQL, site)
	if err != nil {
		return nil, nil, err
	}
	ids := []string{}
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			rows.Close()
			return nil, nil, err
		}
		ids = append(ids, id)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, nil, err
	}
	if len(ids) == 0 {
		return ids, []storage.OverviewCoverage{}, nil
	}
	placeholders, args := overviewIDs(ids)
	args = append(args, from, to)
	rows, err = s.db.QueryContext(ctx, `SELECT instance_id,bucket_time,confirmed_at FROM overview_metric_coverage WHERE instance_id IN (`+placeholders+`) AND bucket_time>=? AND bucket_time<?`, args...)
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()
	items := []storage.OverviewCoverage{}
	for rows.Next() {
		var x storage.OverviewCoverage
		if err = rows.Scan(&x.InstanceID, &x.BucketTime, &x.ConfirmedAt); err != nil {
			return nil, nil, err
		}
		items = append(items, x)
	}
	return ids, items, rows.Err()
}
func overviewIDs(ids []string) (string, []any) {
	args := make([]any, len(ids))
	for i, id := range ids {
		args[i] = id
	}
	return strings.TrimSuffix(strings.Repeat("?,", len(ids)), ","), args
}

func (s Store) OverviewMetrics(ctx context.Context, ids []string, from, to time.Time) ([]aggregator.Metric, error) {
	if len(ids) == 0 {
		return []aggregator.Metric{}, nil
	}
	// Reuse the canonical scanner/projection; constrain site and time before reading.
	text := metricHistoryPrefixInstancesSQL("metric_1m", len(ids))
	text = strings.Replace(text, "AND bucket_time >= ?", "AND bucket_time >= ? AND bucket_time < ?", 1)
	args := []any{"instance"}
	for _, id := range ids {
		args = append(args, id)
	}
	args = append(args, "", from, to)
	rows, err := s.db.QueryContext(ctx, text, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanMetrics(rows)
}

func (s Store) OverviewRuntime(ctx context.Context, site string) ([]storage.OverviewInstance, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT i.id,i.name,i.enabled,(SELECT MAX(last_seen_at) FROM agents a WHERE a.instance_id=i.id) FROM instances i WHERE i.deleted=0 AND CASE WHEN i.site_id='' THEN i.id ELSE i.site_id END=? ORDER BY i.id`, site)
	if err != nil {
		return nil, err
	}
	out := []storage.OverviewInstance{}
	for rows.Next() {
		var x storage.OverviewInstance
		var at sql.NullTime
		if err = rows.Scan(&x.ID, &x.Name, &x.Enabled, &at); err != nil {
			rows.Close()
			return nil, err
		}
		if at.Valid {
			x.LastSeenAt = &at.Time
		}
		out = append(out, x)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	byID := map[string]int{}
	ids := []string{}
	for i, x := range out {
		byID[x.ID] = i
		ids = append(ids, x.ID)
	}
	if len(ids) == 0 {
		return out, nil
	}
	p, args := overviewIDs(ids)
	// Latest per object, scoped before grouping: history from fast reporters cannot
	// push a slow instance out of a global LIMIT. Each identity uses its latest row.
	rows, err = s.db.QueryContext(ctx, `SELECT h.instance_id,h.checked_at,h.target,h.status,h.http_status_code,h.latency_ms,h.error_summary FROM health_checks h JOIN (SELECT instance_id,target,MAX(checked_at) at FROM health_checks WHERE instance_id IN (`+p+`) GROUP BY instance_id,target) t ON h.instance_id=t.instance_id AND h.target=t.target AND h.checked_at=t.at`, args...)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var x storage.HealthCheck
		if err = rows.Scan(&x.InstanceID, &x.CheckedAt, &x.Target, &x.Status, &x.HTTPStatusCode, &x.LatencyMS, &x.ErrorSummary); err != nil {
			rows.Close()
			return nil, err
		}
		i := byID[x.InstanceID]
		out[i].Health = append(out[i].Health, x)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	rows, err = s.db.QueryContext(ctx, `SELECT d.instance_id,d.collected_at,d.container_name,d.status,d.running FROM docker_statuses d JOIN (SELECT instance_id,container_name,MAX(collected_at) at FROM docker_statuses WHERE instance_id IN (`+p+`) GROUP BY instance_id,container_name) t ON d.instance_id=t.instance_id AND d.container_name=t.container_name AND d.collected_at=t.at`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var x storage.DockerStatus
		if err = rows.Scan(&x.InstanceID, &x.CollectedAt, &x.ContainerName, &x.Status, &x.Running); err != nil {
			return nil, err
		}
		i := byID[x.InstanceID]
		out[i].Docker = append(out[i].Docker, x)
	}
	return out, rows.Err()
}

func (s Store) MarkOverviewAlertEvaluation(now time.Time) error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, err := s.db.ExecContext(ctx, `INSERT INTO overview_alert_evaluation(id,completed_at) VALUES(1,?) ON DUPLICATE KEY UPDATE completed_at=GREATEST(completed_at,VALUES(completed_at))`, now)
	return err
}
func (s Store) OverviewAlerts(ctx context.Context, site string, now time.Time) (storage.OverviewAlertSnapshot, error) {
	out := storage.OverviewAlertSnapshot{Items: []storage.Alert{}}
	var at sql.NullTime
	err := s.db.QueryRowContext(ctx, `SELECT MAX(completed_at) FROM overview_alert_evaluation`).Scan(&at)
	if err != nil {
		return out, err
	}
	if at.Valid {
		out.EvaluatedAt = &at.Time
	}
	where := ` FROM alerts a WHERE a.status IN ('firing','acknowledged','silenced') AND (a.instance_id=? OR a.instance_id IN (SELECT id FROM instances WHERE CASE WHEN site_id='' THEN id ELSE site_id END=?))`
	cutoff := now.Add(-2 * time.Minute)
	err = s.db.QueryRowContext(ctx, `SELECT COUNT(*),COALESCE(SUM(confirmed_at IS NULL OR confirmed_at<? OR confirmed_at>?),0)`+where, cutoff, now, site, site).Scan(&out.Total, &out.Unconfirmed)
	if err != nil {
		return out, err
	}
	rows, err := s.db.QueryContext(ctx, `SELECT id,instance_id,rule_key,severity,status,title,summary,first_seen_at,last_seen_at,confirmed_at,dimension_type,dimension_key`+where+` AND confirmed_at>=? AND confirmed_at<=? ORDER BY FIELD(severity,'critical','warning','info'),confirmed_at DESC,id LIMIT 5`, site, site, cutoff, now)
	if err != nil {
		return out, err
	}
	defer rows.Close()
	for rows.Next() {
		var a storage.Alert
		if err = rows.Scan(&a.ID, &a.InstanceID, &a.RuleKey, &a.Severity, &a.Status, &a.Title, &a.Summary, &a.FirstSeenAt, &a.LastSeenAt, &a.ConfirmedAt, &a.DimensionType, &a.DimensionKey); err != nil {
			return out, err
		}
		out.Items = append(out.Items, a)
	}
	return out, rows.Err()
}
