package mysqlstore

import (
	"context"
	"database/sql"
	"strings"
	"time"

	"controltower/server/internal/storage"
)

func (s Store) ChannelNames(instanceID string) (map[int64]string, error) {
	// Grouped-join on the latest snapshot per channel: the previous
	// GROUP_CONCAT ordered the entire snapshot history on every cache miss.
	rows, err := s.db.QueryContext(context.Background(), `SELECT c.channel_id, c.channel_name
FROM channel_snapshots c
JOIN (
  SELECT channel_id, MAX(captured_at) AS captured_at
  FROM channel_snapshots
  WHERE instance_id = ?
  GROUP BY channel_id
) latest ON latest.channel_id = c.channel_id AND latest.captured_at = c.captured_at
WHERE c.instance_id = ?`, instanceID, instanceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make(map[int64]string)
	for rows.Next() {
		var id int64
		var name sql.NullString
		if err := rows.Scan(&id, &name); err != nil {
			return nil, err
		}
		if name.Valid {
			result[id] = name.String
		}
	}
	return result, rows.Err()
}

func (s Store) QueryChannelSnapshots(query storage.ChannelSnapshotQuery) ([]storage.ChannelSnapshot, error) {
	sqlText, args := buildChannelSnapshotQuery(query)
	rows, err := s.db.QueryContext(context.Background(), sqlText, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var items []storage.ChannelSnapshot
	for rows.Next() {
		var item storage.ChannelSnapshot
		if err := rows.Scan(
			&item.ID,
			&item.InstanceID,
			&item.ChannelID,
			&item.ChannelName,
			&item.Status,
			&item.Weight,
			&item.ModelsText,
			&item.GroupName,
			&item.Priority,
			&item.CapturedAt,
		); err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func buildChannelSnapshotQuery(query storage.ChannelSnapshotQuery) (string, []any) {
	limit, offset := storage.NormalizeRuntimePagination(query.Limit, query.Offset)
	where := ""
	args := []any{}
	if query.InstanceID != "" {
		where, args = appendWhere(where, args, "instance_id = ?", query.InstanceID)
	}
	if query.ChannelID > 0 {
		where, args = appendWhere(where, args, "channel_id = ?", query.ChannelID)
	}
	if !query.StartTime.IsZero() {
		where, args = appendWhere(where, args, "captured_at >= ?", query.StartTime)
	}
	if !query.EndTime.IsZero() {
		where, args = appendWhere(where, args, "captured_at <= ?", query.EndTime)
	}
	args = append(args, limit, offset)
	builder := strings.Builder{}
	if query.LatestOnly {
		// 先按 (instance_id, channel_id) 分组取最新 captured_at，再连接取整行，
		// 复用 idx_channel_snapshots_instance_channel，避免对全量历史行
		// （含较大的 models_text）做 filesort。
		builder.WriteString(`SELECT s.id, s.instance_id, s.channel_id, s.channel_name, s.status, s.weight, s.models_text, s.group_name, s.priority, s.captured_at
FROM channel_snapshots s
JOIN (
  SELECT instance_id, channel_id, MAX(captured_at) AS captured_at
  FROM channel_snapshots`)
		builder.WriteString(where)
		builder.WriteString(`
  GROUP BY instance_id, channel_id
) latest
  ON latest.instance_id = s.instance_id
 AND latest.channel_id = s.channel_id
 AND latest.captured_at = s.captured_at
ORDER BY s.instance_id ASC, s.channel_id ASC
LIMIT ? OFFSET ?`)
		return builder.String(), args
	}
	builder.WriteString(`SELECT id, instance_id, channel_id, channel_name, status, weight, models_text, group_name, priority, captured_at
FROM channel_snapshots`)
	builder.WriteString(where)
	builder.WriteString(`
ORDER BY captured_at DESC, channel_id ASC
LIMIT ? OFFSET ?`)
	return builder.String(), args
}
func (s Store) PruneChannelSnapshots(before time.Time) error {
	_, err := s.db.ExecContext(context.Background(), "DELETE FROM channel_snapshots WHERE captured_at < ?", before)
	return err
}
