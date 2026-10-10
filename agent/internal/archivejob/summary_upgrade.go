package archivejob

import (
	"context"
	"database/sql"
	"errors"
	"time"

	aj "controltower/internal/archivejob"
)

// Called under the archive writer lock before raw changes can update live stats.
// Old live deltas cannot be subtracted with new grouping/usage rules. Start a new
// version instead; old immutable rows remain available for audit.
func ensureSummaryGeneration(ctx context.Context, c *sql.Conn, s *state, configuredFrom string, now time.Time) error {
	if s.SummaryVersion > summaryParserVersion {
		return errors.New("archive_summary_version_newer")
	}
	from := configuredFrom
	if from == "" {
		from = s.SummaryFromDate
	}
	if from == "" {
		from = now.In(beijing).Format("2006-01-02")
	}
	if s.SummaryVersion == summaryParserVersion && s.SummaryFromDate == from {
		return nil
	}
	next := *s
	tx, err := c.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	// On expansion only newly included dates need resetting; narrowing the
	// range must not disturb already upgraded dates that remain in scope.
	reset := s.SummaryVersion < summaryParserVersion || s.SummaryFromDate == "" || from < s.SummaryFromDate
	if reset {
		query := `UPDATE log_archive_live_stats SET version_id=REPLACE(UUID(),'-',''),after_created=0,after_id=0,upper_created=-1,upper_id=0,ready=0,error_code='',retry_at=NULL,updated_at=UTC_TIMESTAMP(6) WHERE log_date>=?`
		args := []any{from}
		if s.SummaryVersion == summaryParserVersion && s.SummaryFromDate != "" {
			query += " AND log_date<?"
			args = append(args, s.SummaryFromDate)
		}
		if _, err = tx.ExecContext(ctx, query, args...); err != nil {
			return err
		}
	}
	if next.LargeLive != nil && (day(next.LargeLive.Created) < from || reset && (s.SummaryVersion < summaryParserVersion || s.SummaryFromDate == "" || day(next.LargeLive.Created) < s.SummaryFromDate)) {
		if _, err = tx.ExecContext(ctx, "DELETE FROM log_archive_large_chunks WHERE transfer_id=?", next.LargeLive.Token); err != nil {
			return err
		}
		next.LargeLive = nil
	}
	if next.History.Date != "" && next.History.Date < from {
		if next.LargeHistory != nil {
			if _, err = tx.ExecContext(ctx, "DELETE FROM log_archive_large_chunks WHERE transfer_id=?", next.LargeHistory.Token); err != nil {
				return err
			}
			next.LargeHistory = nil
		}
		next.History = history{}
		next.HistoryProgress = aj.Progress{Step: "idle", UpdatedAt: now.UTC()}
	}
	if next.History.Date != "" && next.History.ParserVersion < summaryParserVersion {
		if next.History.Step == "summarize" || next.History.Step == "seal" {
			next.History.Version = id()
			next.History.Step = "summarize"
			next.History.AfterID = 0
			next.History.AfterCreated = 0
			if next.LargeHistory != nil {
				if _, err = tx.ExecContext(ctx, "DELETE FROM log_archive_large_chunks WHERE transfer_id=?", next.LargeHistory.Token); err != nil {
					return err
				}
				next.LargeHistory = nil
			}
		}
		next.History.ParserVersion = summaryParserVersion
	}
	next.SummaryVersion = summaryParserVersion
	next.SummaryFromDate = from
	if err = save(ctx, tx, next); err != nil {
		return err
	}
	if err = tx.Commit(); err != nil {
		return err
	}
	*s = next
	return nil
}

// Rebuild a sealed old summary from archived raw rows, even if upstream history
// has expired. The previously verified row count/hash must match before summary
// generation. Keep the old published version until the normal seal transaction.
func startSummaryRebuild(ctx context.Context, c *sql.Conn, s *state, cutoff string) error {
	h := history{Step: "verify_archive", Version: id(), ParserVersion: summaryParserVersion}
	err := c.QueryRowContext(ctx, `SELECT CAST(d.log_date AS CHAR),d.revision,v.row_count,v.content_hash
 FROM log_archive_days d JOIN log_archive_day_versions v ON v.version_id=d.version_id AND v.log_date=d.log_date AND v.revision=d.revision
 WHERE d.state='sealed' AND d.log_date>=? AND v.parser_version<? AND d.log_date<=? AND d.log_date<? ORDER BY d.log_date LIMIT 1`, s.summaryLowerBound(), summaryParserVersion, cutoff, s.Frontier).Scan(&h.Date, &h.Revision, &h.SourceRows, &h.SourceHash)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	s.History = h
	return nil
}

func (s *state) includesSummary(date string) bool {
	return s.SummaryFromDate == "" || date >= s.SummaryFromDate
}

func (s *state) summaryLowerBound() string {
	if s.SummaryFromDate == "" {
		return "1000-01-01"
	}
	return s.SummaryFromDate
}
