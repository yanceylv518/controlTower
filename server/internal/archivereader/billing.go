package archivereader

import (
	"context"
	"controltower/server/internal/billing"
	"database/sql"
	"fmt"
	"strings"
	"time"
)

type BillingVersionStore interface {
	BindBillingArchiveVersion(context.Context, string, string, string, string) error
}

type BillingRawLog struct {
	Log   billing.PagedLogRecord
	Other string
}

// BillingPage reads a sealed day's original records, not today's price config
// or daily aggregates. The fixed projection excludes keys and client IPs.
func (r Reader) BillingPage(ctx context.Context, site string, jobID string, from, to time.Time, cursor billing.LogCursor, user int64, channels []int64, limit int) ([]BillingRawLog, error) {
	if site == "" || !to.After(from) || to.Sub(from) > 24*time.Hour || limit < 1 || limit > 5000 {
		return nil, ErrQuery
	}
	day := from.In(billing.BusinessLocation)
	midnight := time.Date(day.Year(), day.Month(), day.Day(), 0, 0, 0, 0, billing.BusinessLocation)
	if to.After(midnight.AddDate(0, 0, 1)) {
		return nil, ErrQuery
	}
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	db, expected, err := r.openJob(ctx, site)
	if err != nil {
		return nil, err
	}
	defer db.Close()
	tx, err := db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true, Isolation: sql.LevelRepeatableRead})
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	var source string
	var schema int
	if err = tx.QueryRowContext(ctx, `SELECT source_hash,schema_version FROM log_archive_meta WHERE singleton_id=1`).Scan(&source, &schema); err != nil {
		return nil, err
	}
	if source != expected || schema != 1 {
		return nil, ErrIdentity
	}
	var state string
	if err = tx.QueryRowContext(ctx, `SELECT state FROM log_archive_days WHERE log_date=?`, day.Format("2006-01-02")).Scan(&state); err != nil || state != "sealed" {
		return nil, ErrVersion
	}
	var version string
	if err = tx.QueryRowContext(ctx, `SELECT version_id FROM log_archive_days WHERE log_date=?`, day.Format("2006-01-02")).Scan(&version); err != nil || version == "" {
		return nil, ErrVersion
	}
	if jobID != "" {
		if r.BillingVersions == nil {
			return nil, fmt.Errorf("archive billing version store unavailable")
		}
		if err = r.BillingVersions.BindBillingArchiveVersion(ctx, jobID, day.Format("2006-01-02"), source, version); err != nil {
			return nil, err
		}
	}
	table := "logs_" + day.Format("200601")
	var timeIndex string
	if err=tx.QueryRowContext(ctx,`SELECT index_name FROM information_schema.statistics WHERE table_schema=DATABASE() AND table_name=? AND seq_in_index=1 AND column_name='created_at' AND index_type='BTREE' LIMIT 1`,table).Scan(&timeIndex);err!=nil{return nil,ErrIndex}
	cols, err := tx.QueryContext(ctx, `SELECT column_name FROM information_schema.columns WHERE table_schema=DATABASE() AND table_name=?`, table)
	if err != nil {
		return nil, err
	}
	known := map[string]bool{}
	for cols.Next() {
		var c string
		if err = cols.Scan(&c); err != nil {
			cols.Close()
			return nil, err
		}
		known[c] = true
	}
	err = cols.Err()
	cols.Close()
	if err != nil {
		return nil, err
	}
	for _, c := range []string{"id", "created_at", "type", "user_id", "model_name", "prompt_tokens", "completion_tokens", "quota", "other"} {
		if !known[c] {
			return nil, fmt.Errorf("archive billing missing column %s", c)
		}
	}
	channel := archiveChannelColumn(known["channel_id"], known["channel"])
	if channel == "NULL" && len(channels) > 0 {
		return nil, ErrChannelColumn
	}
	optional := func(c, fallback string) string {
		if known[c] {
			return "COALESCE(`" + c + "`," + fallback + ")"
		}
		return fallback
	}
	projection := []string{"id", "created_at", optional("request_id", "''"), optional("upstream_request_id", "''"), "user_id", optional("username", "''"), optional("token_id", "0"), optional("token_name", "''"), "COALESCE(" + channel + ",0)", "''", "model_name", optional("group", "''"), "prompt_tokens", "completion_tokens", "quota", "COALESCE(LEFT(other,1048577),'')"}
	query := "SELECT " + strings.Join(projection, ",") + " FROM `" + table + "` WHERE type=2 AND created_at>=? AND created_at<? AND (created_at>? OR (created_at=? AND id>?))"
	args := []any{from.Unix(), to.Unix(), cursor.CreatedUnix, cursor.CreatedUnix, cursor.ID}
	if user > 0 {
		query += " AND user_id=?"
		args = append(args, user)
	}
	if len(channels) > 0 {
		query += " AND " + channel + " IN (" + strings.TrimSuffix(strings.Repeat("?,", len(channels)), ",") + ")"
		for _, id := range channels {
			args = append(args, id)
		}
	}
	query += " ORDER BY created_at,id LIMIT ?"
	args = append(args, min(limit, 200))
	rows, err := tx.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	out := []BillingRawLog{}
	for rows.Next() {
		var raw BillingRawLog
		v := &raw.Log
		if err = rows.Scan(&v.ID, &v.CreatedUnix, &v.RequestID, &v.UpstreamRequestID, &v.UserID, &v.Username, &v.TokenID, &v.TokenName, &v.ChannelID, &v.ChannelName, &v.ModelName, &v.GroupName, &v.PromptTokens, &v.CompletionTokens, &v.Quota, &raw.Other); err != nil {
			rows.Close()
			return nil, err
		}
		if len(raw.Other) > 1048576 {
			rows.Close()
			return nil, ErrPageSize
		}
		out = append(out, raw)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	if err = tx.Commit(); err != nil {
		return nil, err
	}
	return out, nil
}

func (r Reader) FirstBillingDay(ctx context.Context, site string) (time.Time, error) {
	db, expected, err := r.openJob(ctx, site)
	if err != nil {
		return time.Time{}, err
	}
	defer db.Close()
	var source string
	if err = db.QueryRowContext(ctx, `SELECT source_hash FROM log_archive_meta WHERE singleton_id=1`).Scan(&source); err != nil || source != expected {
		return time.Time{}, ErrIdentity
	}
	var day string
	if err = db.QueryRowContext(ctx, `SELECT DATE_FORMAT(log_date,'%Y-%m-%d') FROM log_archive_days WHERE state='sealed' ORDER BY log_date LIMIT 1`).Scan(&day); err != nil {
		return time.Time{}, err
	}
	return time.ParseInLocation("2006-01-02", day, billing.BusinessLocation)
}

func (r Reader) BillingFailedRequests(ctx context.Context, site string, from, to time.Time) (int64, error) {
	db, expected, err := r.openJob(ctx, site)
	if err != nil {
		return 0, err
	}
	defer db.Close()
	var source string
	if err = db.QueryRowContext(ctx, `SELECT source_hash FROM log_archive_meta WHERE singleton_id=1`).Scan(&source); err != nil || source != expected {
		return 0, ErrIdentity
	}
	var count int64
	for day := from; day.Before(to); {
		end := billing.CompleteDayBoundary(day).AddDate(0, 0, 1)
		if end.After(to) {
			end = to
		}
		var state string
		if err = db.QueryRowContext(ctx, `SELECT state FROM log_archive_days WHERE log_date=?`, day.In(billing.BusinessLocation).Format("2006-01-02")).Scan(&state); err != nil || state != "sealed" {
			return 0, ErrVersion
		}
		table := "logs_" + day.In(billing.BusinessLocation).Format("200601")
		var n int64
		if err = db.QueryRowContext(ctx, "SELECT COUNT(*) FROM `"+table+"` WHERE type=5 AND created_at>=? AND created_at<?", day.Unix(), end.Unix()).Scan(&n); err != nil {
			return 0, err
		}
		count += n
		day = end
	}
	return count, nil
}
