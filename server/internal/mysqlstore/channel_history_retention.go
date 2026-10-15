package mysqlstore

import (
	"context"
	"errors"
	"fmt"
	"log"
	"strings"
	"sync"
	"time"
)

type channelHistoryCursor struct {
	at time.Time
	id string
}
type channelHistoryScan struct {
	sync.Mutex
	cursors   map[string]channelHistoryCursor
	nextOwner string
	pending   bool
}

const channelHistoryBatchSize = 100
const channelHistoryPassBatches = 50

func (s Store) ChannelHistoryCleanupPending() bool {
	if s.historyScan == nil {
		return false
	}
	s.historyScan.Lock()
	defer s.historyScan.Unlock()
	return s.historyScan.pending
}

type historyBatchStats struct {
	candidates, deleted, protected                        int
	candidateTime, protectionTime, deleteTime, commitTime time.Duration
}

// Resolve the small ownership set first, so history queries use index ranges
// instead of an OR with a dependent instance-membership subquery.
func (s Store) channelHistoryOwners(ctx context.Context, site string) ([]any, error) {
	return []any{site}, nil
}

// Each invocation does bounded work. Terminal records must be old both by
// creation and completion time; late results receive a full retention window.
// Unknown command states and non-channel audits are deliberately preserved.
func (s Store) pruneChannelHistory(ctx context.Context, now time.Time) error {
	started := time.Now()
	deadline := started.Add(4 * time.Second)
	if d, ok := ctx.Deadline(); ok && d.Before(deadline) {
		deadline = d
	}
	ctx, cancel := context.WithDeadline(ctx, deadline)
	defer cancel()
	var totals historyBatchStats
	batches := 0
	workBatches := 0
	pending := false
	defer func() {
		if s.historyScan != nil {
			s.historyScan.Lock()
			s.historyScan.pending = pending
			s.historyScan.Unlock()
		}
		log.Printf("channel history cleanup batches=%d candidates=%d deleted=%d protected=%d candidate_time=%s protection_time=%s delete_time=%s commit_time=%s elapsed=%s pending=%t", batches, totals.candidates, totals.deleted, totals.protected, totals.candidateTime, totals.protectionTime, totals.deleteTime, totals.commitTime, time.Since(started), pending)
	}()
	var failures error
	if err := s.backfillChannelHistory(ctx); err != nil {
		failures = fmt.Errorf("history ownership backfill: %w", err)
	}
	// A database upgraded from legacy history may have no owned rows yet.
	// Keep the bounded evidence backfill on the catch-up cadence too.
	if err := s.db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM channel_commands WHERE ownership_evidence='' LIMIT 1) OR EXISTS(SELECT 1 FROM operation_audits WHERE ownership_evidence='' AND target_type='channel' LIMIT 1)`).Scan(&pending); err != nil {
		return errors.Join(failures, err)
	}
	rows, err := s.db.QueryContext(ctx, `SELECT site_id,site_id FROM (
SELECT DISTINCT site_id FROM channel_commands WHERE site_id<>''
UNION SELECT DISTINCT site_id FROM operation_audits WHERE site_id<>'' AND target_type='channel'
) h ORDER BY site_id`)
	if err != nil {
		return err
	}
	type owner struct{ id, site string }
	var owners []owner
	for rows.Next() {
		var o owner
		if err := rows.Scan(&o.id, &o.site); err != nil {
			rows.Close()
			return err
		}
		owners = append(owners, o)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	// Rotate ownership so a slow owner cannot consume every pass's shared
	// deadline and permanently starve owners later in lexical order.
	if s.historyScan != nil && len(owners) > 0 {
		s.historyScan.Lock()
		next := s.historyScan.nextOwner
		s.historyScan.Unlock()
		for i, o := range owners {
			if o.id >= next {
				owners = append(owners[i:], owners[:i]...)
				break
			}
		}
	}
	done := make(map[string]bool)
	policies := make(map[string]int)
	for {
		allDone := true
		for i, o := range owners {
			if workBatches >= channelHistoryPassBatches || time.Until(deadline) < 500*time.Millisecond {
				pending = true
				return failures
			}
			if ctx.Err() != nil {
				return errors.Join(failures, ctx.Err())
			}
			if s.historyScan != nil {
				s.historyScan.Lock()
				s.historyScan.nextOwner = owners[(i+1)%len(owners)].id
				s.historyScan.Unlock()
			}
			days, known := policies[o.site]
			var err error
			if !known {
				days, err = s.channelHistoryDays(ctx, o.site)
				if err == nil {
					policies[o.site] = days
				}
			}
			if err != nil {
				failures = errors.Join(failures, fmt.Errorf("history policy (%s): %w", o.id, err))
				continue
			}
			before := now.Add(-time.Duration(days) * 24 * time.Hour)
			// One status per range preserves index order and bounded work.
			for _, status := range []string{"succeeded", "failed", "expired"} {
				key := o.id + "/" + status
				if done[key] {
					continue
				}
				if workBatches >= channelHistoryPassBatches || time.Until(deadline) < 500*time.Millisecond {
					pending = true
					return failures
				}
				stats, batchErr := s.pruneChannelHistoryBatch(ctx, now, "channel_commands", "idx_command_site_retention", "site_id=? AND status=? AND created_at<?", 3,
					`site_id=? AND status=? AND created_at<? AND updated_at<?`, o.id, status, before, before)
				batches++
				if stats.candidates > 0 {
					workBatches++
				}
				totals.add(stats)
				done[key] = batchErr != nil || stats.candidates < channelHistoryBatchSize
				if !done[key] {
					allDone = false
				}
				if batchErr != nil {
					failures = errors.Join(failures, fmt.Errorf("channel command retention (%s/%s): %w", o.id, status, batchErr))
				}
			}
			// A linked audit becomes eligible only after its safely expired command
			// has been removed. Commands with current references keep their audits.
			key := o.id + "/audits"
			if done[key] {
				continue
			}
			if workBatches >= channelHistoryPassBatches || time.Until(deadline) < 500*time.Millisecond {
				pending = true
				return failures
			}
			stats, batchErr := s.pruneChannelHistoryBatch(ctx, now, "operation_audits", "idx_audit_site_retention", "site_id=? AND target_type='channel' AND created_at<?", 2,
				`site_id=? AND target_type='channel' AND created_at<? AND updated_at<? AND status IN ('succeeded','success','failed','expired','canceled','cancelled','rejected')`, o.id, before, before)
			batches++
			if stats.candidates > 0 {
				workBatches++
			}
			totals.add(stats)
			done[key] = batchErr != nil || stats.candidates < channelHistoryBatchSize
			if !done[key] {
				allDone = false
			}
			if batchErr != nil {
				failures = errors.Join(failures, fmt.Errorf("channel audit retention (%s): %w", o.id, batchErr))
			}
		}
		if allDone {
			break
		}
	}
	return failures
}

// Lock an index-ordered bounded candidate set, then delete only those primary
// keys. A direct DELETE with correlated guards can choose an unordered scan and
// evaluate every old row before applying LIMIT. Never concatenate caller input.
func (s Store) pruneChannelHistoryBatch(ctx context.Context, now time.Time, table, index, candidate string, rangeArgs int, where string, args ...any) (stats historyBatchStats, resultErr error) {
	// LIMIT must precede protection checks. Otherwise thousands of protected
	// rows can be examined to find just one deletable row. Rotate through all
	// candidates, including protected ones; restart safely rescans from the start.
	scan := s.historyScan
	if scan == nil {
		scan = &channelHistoryScan{cursors: make(map[string]channelHistoryCursor)}
	}
	scan.Lock()
	defer scan.Unlock()
	key := fmt.Sprintf("%s/%v", table, args[:rangeArgs-1])
	cursor := scan.cursors[key]
	queryArgs := append([]any{}, args[:rangeArgs]...)
	if cursor.id != "" {
		candidate += " AND (created_at>? OR (created_at=? AND id>?))"
		queryArgs = append(queryArgs, cursor.at, cursor.at, cursor.id)
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return stats, err
	}
	defer tx.Rollback()
	stage := time.Now()
	rows, err := tx.QueryContext(ctx, "SELECT /*+ MAX_EXECUTION_TIME(1000) */ id,created_at FROM "+table+" FORCE INDEX ("+index+") WHERE "+candidate+fmt.Sprintf(" ORDER BY created_at,id LIMIT %d FOR UPDATE SKIP LOCKED", channelHistoryBatchSize), queryArgs...)
	if err != nil {
		return stats, fmt.Errorf("select candidates: %w", err)
	}
	var ids []any
	for rows.Next() {
		var id string
		if err := rows.Scan(&id, &cursor.at); err != nil {
			rows.Close()
			return stats, err
		}
		ids = append(ids, id)
		cursor.id = id
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return stats, fmt.Errorf("read candidates: %w", err)
	}
	stats.candidates = len(ids)
	stats.candidateTime = time.Since(stage)
	if len(ids) > 0 {
		stage = time.Now()
		deletable, err := channelHistoryDeletable(ctx, tx, table, ids, where, args, now)
		stats.protectionTime = time.Since(stage)
		if err != nil {
			return stats, fmt.Errorf("check protections: %w", err)
		}
		stage = time.Now()
		if len(deletable) > 0 {
			if _, err = tx.ExecContext(ctx, "DELETE FROM "+table+" WHERE id IN ("+strings.TrimSuffix(strings.Repeat("?,", len(deletable)), ",")+")", deletable...); err != nil {
				return stats, err
			}
		}
		stats.deleteTime = time.Since(stage)
		stats.deleted = len(deletable)
		stats.protected = len(ids) - len(deletable)
	}
	stage = time.Now()
	if err := tx.Commit(); err != nil {
		stats.deleted = 0
		return stats, err
	}
	stats.commitTime = time.Since(stage)
	if len(ids) < channelHistoryBatchSize {
		delete(scan.cursors, key)
	} else {
		scan.cursors[key] = cursor
	}
	return stats, nil
}

func (s *historyBatchStats) add(b historyBatchStats) {
	s.candidates += b.candidates
	s.deleted += b.deleted
	s.protected += b.protected
	s.candidateTime += b.candidateTime
	s.protectionTime += b.protectionTime
	s.deleteTime += b.deleteTime
	s.commitTime += b.commitTime
}
