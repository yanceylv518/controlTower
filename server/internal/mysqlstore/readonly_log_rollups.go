package mysqlstore

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"controltower/server/internal/storage"
)

func (s Store) ReadonlyLogRollupCursor(ctx context.Context, siteID string) (storage.ReadonlyLogRollupCursor, error) {
	var cursor storage.ReadonlyLogRollupCursor
	var initialized bool
	var coverage, synced, caughtUp sql.NullTime
	err := s.db.QueryRowContext(ctx, `SELECT site_id,last_log_id,initialized,coverage_from,last_synced_at,caught_up_at,COALESCE(last_error,'') FROM readonly_log_rollup_cursors WHERE site_id=?`, siteID).
		Scan(&cursor.SiteID, &cursor.LastLogID, &initialized, &coverage, &synced, &caughtUp, &cursor.LastError)
	if errors.Is(err, sql.ErrNoRows) {
		return storage.ReadonlyLogRollupCursor{SiteID: siteID}, nil
	}
	if err != nil {
		return cursor, err
	}
	cursor.Initialized = initialized
	if coverage.Valid {
		value := coverage.Time.UTC()
		cursor.CoverageFrom = &value
	}
	if synced.Valid {
		value := synced.Time.UTC()
		cursor.LastSyncedAt = &value
	}
	if caughtUp.Valid {
		value := caughtUp.Time.UTC()
		cursor.CaughtUpAt = &value
	}
	return cursor, nil
}

func (s Store) MarkReadonlyLogRollupCaughtUp(ctx context.Context, siteID string, now time.Time) error {
	_, err := s.db.ExecContext(ctx, `UPDATE readonly_log_rollup_cursors SET caught_up_at=?,last_error=NULL,updated_at=? WHERE site_id=?`, now.UTC(), now.UTC(), siteID)
	return err
}

func (s Store) InitializeReadonlyLogRollupCursor(ctx context.Context, siteID string, lastLogID int64, coverageFrom, now time.Time) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO readonly_log_rollup_cursors(site_id,last_log_id,initialized,coverage_from,last_synced_at,last_error,updated_at) VALUES(?,?,1,?,NULL,NULL,?) ON DUPLICATE KEY UPDATE last_log_id=IF(initialized=0,VALUES(last_log_id),last_log_id),coverage_from=IF(initialized=0,VALUES(coverage_from),coverage_from),initialized=1,last_error=NULL,updated_at=VALUES(updated_at)`, siteID, lastLogID, coverageFrom.UTC(), now.UTC())
	return err
}

func (s Store) ApplyReadonlyLogRollups(ctx context.Context, siteID string, lastLogID int64, values []storage.ReadonlyLogRollup, now time.Time) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var current int64
	if err = tx.QueryRowContext(ctx, `SELECT last_log_id FROM readonly_log_rollup_cursors WHERE site_id=? FOR UPDATE`, siteID).Scan(&current); err != nil {
		return err
	}
	if current >= lastLogID {
		return tx.Commit()
	}
	for _, value := range values {
		_, err = tx.ExecContext(ctx, `INSERT INTO readonly_log_stats_hourly(dimension_hash,site_id,hour_start,log_type,user_id,username,channel_id,model_name,token_name,group_name,request_count,prompt_tokens,completion_tokens,quota_sum,updated_at) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?) ON DUPLICATE KEY UPDATE request_count=request_count+VALUES(request_count),prompt_tokens=prompt_tokens+VALUES(prompt_tokens),completion_tokens=completion_tokens+VALUES(completion_tokens),quota_sum=quota_sum+VALUES(quota_sum),updated_at=VALUES(updated_at)`, value.DimensionHash, value.SiteID, value.HourStart.UTC(), value.LogType, value.UserID, value.Username, value.ChannelID, value.ModelName, value.TokenName, value.GroupName, value.RequestCount, value.PromptTokens, value.CompletionTokens, value.QuotaSum, now.UTC())
		if err != nil {
			return err
		}
	}
	_, err = tx.ExecContext(ctx, `UPDATE readonly_log_rollup_cursors SET last_log_id=?,last_synced_at=?,last_error=NULL,updated_at=? WHERE site_id=?`, lastLogID, now.UTC(), now.UTC(), siteID)
	if err != nil {
		return err
	}
	return tx.Commit()
}

func (s Store) RecordReadonlyLogRollupError(ctx context.Context, siteID, message string, now time.Time) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO readonly_log_rollup_cursors(site_id,last_log_id,initialized,last_error,updated_at) VALUES(?,0,0,?,?) ON DUPLICATE KEY UPDATE last_error=VALUES(last_error),updated_at=VALUES(updated_at)`, siteID, message, now.UTC())
	return err
}

func (s Store) QueryReadonlyLogRollup(ctx context.Context, filter storage.ReadonlyLogRollupFilter) (storage.ReadonlyLogRollupSummary, error) {
	query := `SELECT COALESCE(SUM(request_count),0),COALESCE(SUM(prompt_tokens),0),COALESCE(SUM(completion_tokens),0),COALESCE(SUM(quota_sum),0) FROM readonly_log_stats_hourly WHERE site_id=? AND hour_start>=? AND hour_start<?`
	args := []any{filter.SiteID, filter.Start.UTC(), filter.End.UTC()}
	if filter.LogType != nil {
		query += ` AND log_type=?`
		args = append(args, *filter.LogType)
	}
	if len(filter.UserIDs) > 0 {
		query += ` AND user_id IN (` + placeholdersSQL(len(filter.UserIDs)) + `)`
		for _, id := range filter.UserIDs {
			args = append(args, id)
		}
	}
	for _, field := range []struct {
		column string
		value  string
	}{{"username", filter.Username}, {"model_name", filter.ModelName}, {"token_name", filter.TokenName}, {"group_name", filter.GroupName}} {
		if field.value != "" {
			query += ` AND ` + field.column + `=?`
			args = append(args, field.value)
		}
	}
	if filter.ChannelID != nil {
		query += ` AND channel_id=?`
		args = append(args, *filter.ChannelID)
	}
	var summary storage.ReadonlyLogRollupSummary
	err := s.db.QueryRowContext(ctx, query, args...).Scan(&summary.RequestCount, &summary.PromptTokens, &summary.CompletionTokens, &summary.QuotaSum)
	return summary, err
}

func placeholdersSQL(count int) string {
	result := "?"
	for i := 1; i < count; i++ {
		result += ",?"
	}
	return result
}
