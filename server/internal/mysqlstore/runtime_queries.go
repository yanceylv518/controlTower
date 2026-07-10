package mysqlstore

import (
	"context"
	"strings"

	"controltower/server/internal/storage"
)

func (s Store) QueryAgents(query storage.AgentQuery) ([]storage.Agent, error) {
	limit, offset := storage.NormalizeRuntimePagination(query.Limit, query.Offset)
	where := ""
	args := []any{}
	if query.InstanceID != "" {
		where, args = appendWhere(where, args, "instance_id = ?", query.InstanceID)
	}
	if query.Status != "" {
		where, args = appendWhere(where, args, "status = ?", query.Status)
	}
	args = append(args, limit, offset)
	rows, err := s.db.QueryContext(context.Background(), `SELECT id, instance_id, version, last_seen_at, last_sequence, last_log_id,
  source_latest_log_id, backlog_estimate, status, report_delay_ms
FROM agents`+where+`
ORDER BY last_seen_at DESC, id ASC
LIMIT ? OFFSET ?`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var items []storage.Agent
	for rows.Next() {
		var item storage.Agent
		if err := rows.Scan(&item.ID, &item.InstanceID, &item.Version, &item.LastSeenAt, &item.LastSequence, &item.LastLogID, &item.SourceLatestLogID, &item.BacklogEstimate, &item.Status, &item.ReportDelayMS); err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}
func (s Store) QueryServerMetrics(query storage.ServerMetricQuery) ([]storage.ServerMetric, error) {
	sqlText, args := buildServerMetricQuery(query)
	rows, err := s.db.QueryContext(context.Background(), sqlText, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var items []storage.ServerMetric
	for rows.Next() {
		var item storage.ServerMetric
		if err := rows.Scan(
			&item.InstanceID,
			&item.CollectedAt,
			&item.CPUPercent,
			&item.MemoryUsedPercent,
			&item.DiskUsedPercent,
			&item.NetworkRxBytesPerSecond,
			&item.NetworkTxBytesPerSecond,
			&item.Load1m,
		); err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (s Store) QueryHealthChecks(query storage.HealthCheckQuery) ([]storage.HealthCheck, error) {
	sqlText, args := buildHealthCheckQuery(query)
	rows, err := s.db.QueryContext(context.Background(), sqlText, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var items []storage.HealthCheck
	for rows.Next() {
		var item storage.HealthCheck
		if err := rows.Scan(
			&item.InstanceID,
			&item.CheckedAt,
			&item.Target,
			&item.Status,
			&item.HTTPStatusCode,
			&item.LatencyMS,
			&item.ErrorSummary,
		); err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (s Store) QueryDockerStatuses(query storage.DockerStatusQuery) ([]storage.DockerStatus, error) {
	sqlText, args := buildDockerStatusQuery(query)
	rows, err := s.db.QueryContext(context.Background(), sqlText, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var items []storage.DockerStatus
	for rows.Next() {
		var item storage.DockerStatus
		if err := rows.Scan(
			&item.InstanceID,
			&item.CollectedAt,
			&item.ContainerName,
			&item.Status,
			&item.Running,
		); err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func buildServerMetricQuery(query storage.ServerMetricQuery) (string, []any) {
	limit, offset := storage.NormalizeRuntimePagination(query.Limit, query.Offset)
	where, args := runtimeWhere(query.InstanceID, query.StartTime, query.EndTime, "collected_at")
	args = append(args, limit, offset)
	return `SELECT instance_id, collected_at, cpu_percent, memory_used_percent, disk_used_percent,
  network_rx_bytes_per_second, network_tx_bytes_per_second, load_1m
FROM server_metrics_10s` + where + `
ORDER BY collected_at DESC
LIMIT ? OFFSET ?`, args
}

func buildHealthCheckQuery(query storage.HealthCheckQuery) (string, []any) {
	limit, offset := storage.NormalizeRuntimePagination(query.Limit, query.Offset)
	where, args := runtimeWhere(query.InstanceID, query.StartTime, query.EndTime, "checked_at")
	if query.Target != "" {
		where, args = appendWhere(where, args, "target = ?", query.Target)
	}
	if query.Status != "" {
		where, args = appendWhere(where, args, "status = ?", query.Status)
	}
	args = append(args, limit, offset)
	return `SELECT instance_id, checked_at, target, status, http_status_code, latency_ms, error_summary
FROM health_checks` + where + `
ORDER BY checked_at DESC, target ASC
LIMIT ? OFFSET ?`, args
}

func buildDockerStatusQuery(query storage.DockerStatusQuery) (string, []any) {
	limit, offset := storage.NormalizeRuntimePagination(query.Limit, query.Offset)
	where, args := runtimeWhere(query.InstanceID, query.StartTime, query.EndTime, "collected_at")
	if query.ContainerName != "" {
		where, args = appendWhere(where, args, "container_name = ?", query.ContainerName)
	}
	if query.Running != nil {
		where, args = appendWhere(where, args, "running = ?", *query.Running)
	}
	args = append(args, limit, offset)
	return `SELECT instance_id, collected_at, container_name, status, running
FROM docker_statuses` + where + `
ORDER BY collected_at DESC, container_name ASC
LIMIT ? OFFSET ?`, args
}

func runtimeWhere(instanceID string, start interface{ IsZero() bool }, end interface{ IsZero() bool }, timeColumn string) (string, []any) {
	where := ""
	args := []any{}
	if instanceID != "" {
		where, args = appendWhere(where, args, "instance_id = ?", instanceID)
	}
	if !start.IsZero() {
		where, args = appendWhere(where, args, timeColumn+" >= ?", start)
	}
	if !end.IsZero() {
		where, args = appendWhere(where, args, timeColumn+" <= ?", end)
	}
	return where, args
}

func appendWhere(where string, args []any, condition string, value any) (string, []any) {
	if strings.TrimSpace(where) == "" {
		where = " WHERE " + condition
	} else {
		where += " AND " + condition
	}
	args = append(args, value)
	return where, args
}
