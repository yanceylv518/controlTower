package mysqlstore

import (
	"context"
	"strings"
	"time"

	"controltower/server/internal/storage"
)

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
	builder.WriteString(`SELECT id, instance_id, channel_id, channel_name, status, weight, models_text, captured_at
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
