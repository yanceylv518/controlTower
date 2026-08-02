package mysqlstore

import (
	"context"
	"database/sql"
	"strings"

	"controltower/server/internal/storage"
)

func (s Store) ChannelNames(instanceID string) (map[int64]string, error) {
	rows, err := s.db.QueryContext(context.Background(), `SELECT channel_id, channel_name
FROM channel_current
WHERE instance_id = ?`, instanceID)
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
	builder.WriteString(`SELECT id, instance_id, channel_id, channel_name, status, weight, models_text, group_name, priority, captured_at
FROM channel_current`)
	builder.WriteString(where)
	if query.LatestOnly {
		builder.WriteString(`
ORDER BY instance_id ASC, channel_id ASC
LIMIT ? OFFSET ?`)
	} else {
		builder.WriteString(`
ORDER BY captured_at DESC, channel_id ASC
LIMIT ? OFFSET ?`)
	}
	return builder.String(), args
}
func (s Store) DeleteChannelSnapshotHistoryBatch(limit int) (int64, error) {
	if limit <= 0 {
		return 0, nil
	}
	result, err := s.db.ExecContext(context.Background(), deleteChannelSnapshotHistoryBatchSQL, limit)
	if err != nil {
		return 0, err
	}
	return result.RowsAffected()
}

const deleteChannelSnapshotHistoryBatchSQL = `DELETE FROM channel_snapshots
WHERE EXISTS (
  SELECT 1
  FROM channel_current
  WHERE channel_current.instance_id = channel_snapshots.instance_id
    AND channel_current.channel_id = channel_snapshots.channel_id
    AND channel_current.captured_at >= channel_snapshots.captured_at
)
ORDER BY captured_at
LIMIT ?`
