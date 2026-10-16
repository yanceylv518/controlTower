package dashboard

import (
	"context"
	"controltower/server/internal/billing"
	"database/sql"
	"errors"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/go-sql-driver/mysql"
)

// A sparse channel/model filter must not scan an entire busy day to find a
// page. Keep the public keyset contract: a short result means the WHOLE range
// is exhausted, not merely that one time window contained no matching rows.
func billingWindowPage(ctx context.Context, start, end time.Time, cursor billing.LogCursor, limit int, read func(time.Time, time.Time, billing.LogCursor, int) ([]billing.PagedLogRecord, error)) ([]billing.PagedLogRecord, error) {
	span := int64(15 * 60)
	lower := max(start.Unix(), cursor.CreatedUnix)
	out := make([]billing.PagedLogRecord, 0, limit)
	for lower < end.Unix() && len(out) < limit {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		upper := lower + span
		if upper > end.Unix() {
			upper = end.Unix()
		}
		started := time.Now()
		rows, err := read(time.Unix(lower, 0), time.Unix(upper, 0), cursor, limit-len(out))
		if err != nil {
			var sourceErr *mysql.MySQLError
			if errors.As(err, &sourceErr) && sourceErr.Number == 3024 {
				if upper-lower > 1 {
					span = max(1, (upper-lower)/2)
					continue // Retry the same lower bound; never advance on failure.
				}
				// Repeating the same one-second query for ten minutes cannot
				// improve its plan. Let the durable job retry release the worker.
				return nil, billing.PermanentPageError{Err: fmt.Errorf("billing source query timeout in one-second window: %w", err)}
			}
			return nil, err
		}
		out = append(out, rows...)
		lower = upper
		// Recover after a dense/slow segment: retaining a one-second window
		// for the rest of an empty day would issue tens of thousands of reads.
		if time.Since(started) < time.Second && span < 15*60 {
			span *= 2
			if span > 15*60 {
				span = 15 * 60
			}
		}
	}
	return out, nil
}

func (h *PassthroughHandler) readBillingWindowPage(ctx context.Context, db *sql.DB, site, query string, filters []any, start, end time.Time, cursor billing.LogCursor, limit int) ([]billing.PagedLogRecord, error) {
	h.mu.Lock()
	index := h.billingTimeIndexes[db]
	h.mu.Unlock()
	if index != "" {
		// Index names come only from information_schema, still quote identifiers.
		query = strings.Replace(query, "FROM logs l WHERE", "FROM logs l FORCE INDEX (`"+strings.ReplaceAll(index, "`", "``")+"`) WHERE", 1)
	}
	return billingWindowPage(ctx, start, end, cursor, limit, func(from, to time.Time, current billing.LogCursor, remaining int) ([]billing.PagedLogRecord, error) {
		release, err := sourceBillingBudget.acquire(ctx, site)
		if err != nil {
			return nil, err
		}
		defer release()
		queryCtx, cancel := context.WithTimeout(ctx, billingLogsPageTimeout)
		defer cancel()
		args := append(billingPageRangeArgs(from, to, current), filters...)
		args = append(args, remaining)
		rows, err := db.QueryContext(queryCtx, query, args...)
		if err != nil {
			var timeout *mysql.MySQLError
			if errors.As(err, &timeout) && timeout.Number == 3024 {
				log.Printf("billing source window timeout site=%s from=%d to=%d cursor=%d/%d; reducing scan window", site, from.Unix(), to.Unix(), current.CreatedUnix, current.ID)
			}
			return nil, err
		}
		defer rows.Close()
		return scanBillingLogRows(rows, make([]billing.PagedLogRecord, 0, remaining))
	})
}
