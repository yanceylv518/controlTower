package logcollector

import (
	"context"
	"database/sql"
	"time"

	_ "github.com/go-sql-driver/mysql"
)

type MySQLCollector struct {
	db *sql.DB
}

func OpenMySQL(dsn string) (*sql.DB, error) {
	db, err := sql.Open("mysql", dsn)
	if err != nil {
		return nil, err
	}
	// The agent is a read-only observer. Keep its pool deliberately small so
	// it cannot compete with new-api for database connections.
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	db.SetConnMaxLifetime(10 * time.Minute)
	return db, nil
}

type BacklogStats struct {
	SourceLatestLogID int64
	BacklogEstimate   int64
}

func (c MySQLCollector) Backlog(ctx context.Context, afterID int64) (BacklogStats, error) {
	var latest int64
	if err := c.db.QueryRowContext(ctx, "SELECT COALESCE(MAX(id), 0) FROM logs").Scan(&latest); err != nil {
		return BacklogStats{}, err
	}
	backlog := latest - afterID
	if backlog < 0 {
		backlog = 0
	}
	return BacklogStats{SourceLatestLogID: latest, BacklogEstimate: backlog}, nil
}

func NewMySQLCollector(db *sql.DB) MySQLCollector {
	return MySQLCollector{db: db}
}

func (c MySQLCollector) Collect(ctx context.Context, afterID int64, limit int) ([]Event, int64, error) {
	if limit <= 0 || limit > 5000 {
		limit = 1000
	}
	rows, err := c.db.QueryContext(ctx, collectLogsSQL(), afterID, limit)
	if err != nil {
		return nil, afterID, err
	}
	defer rows.Close()

	var events []Event
	lastID := afterID
	for rows.Next() {
		row, err := scanLogRow(rows)
		if err != nil {
			return nil, afterID, err
		}
		if row.ID > lastID {
			lastID = row.ID
		}
		event, ok, err := ConvertRow(row)
		if err != nil {
			return nil, afterID, err
		}
		if ok {
			events = append(events, event)
		}
	}
	if err := rows.Err(); err != nil {
		return nil, afterID, err
	}
	return events, lastID, nil
}

func collectLogsSQL() string {
	return `SELECT id, COALESCE(created_at, 0), type, COALESCE(content, ''),
	  COALESCE(user_id, 0), COALESCE(username, ''), COALESCE(channel_id, 0),
	  COALESCE(model_name, ''), COALESCE(token_id, 0), COALESCE(token_name, ''),
	  COALESCE(prompt_tokens, 0), COALESCE(completion_tokens, 0), COALESCE(quota, 0),
	  COALESCE(use_time, 0), COALESCE(is_stream, 0), COALESCE(` + "`group`" + `, ''),
	  COALESCE(request_id, ''), COALESCE(upstream_request_id, ''), COALESCE(other, '')
FROM logs
WHERE id > ? AND type IN (2, 5)
ORDER BY id ASC
LIMIT ?`
}

func scanLogRow(rows interface{ Scan(dest ...any) error }) (Row, error) {
	var row Row
	var createdAtUnix int64
	var useTimeSeconds int64
	if err := rows.Scan(
		&row.ID,
		&createdAtUnix,
		&row.Type,
		&row.Content,
		&row.UserID,
		&row.Username,
		&row.ChannelID,
		&row.ModelName,
		&row.TokenID,
		&row.TokenName,
		&row.PromptTokens,
		&row.CompletionTokens,
		&row.Quota,
		&useTimeSeconds,
		&row.IsStream,
		&row.Group,
		&row.RequestID,
		&row.UpstreamRequestID,
		&row.Other,
	); err != nil {
		return Row{}, err
	}
	// NULL created_at is coalesced to 0 by the query; collection time is the
	// best available approximation and keeps every downstream consumer
	// (alert windows, metric buckets, uploads) free of epoch timestamps.
	if createdAtUnix <= 0 {
		row.CreatedAt = time.Now().UTC()
	} else {
		row.CreatedAt = time.Unix(createdAtUnix, 0).UTC()
	}
	row.UseTime = float64(useTimeSeconds)
	return row, nil
}
