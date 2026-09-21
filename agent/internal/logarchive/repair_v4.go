package logarchive

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"time"

	af "controltower/internal/archivecontract"
)

type repairObservedRow struct {
	exists bool
	hash   [32]byte
	date   string
}

type rawRepair struct {
	id        int64
	month     string
	before    repairObservedRow
	afterHash [32]byte
	afterDate string
	kind      string
}

type repairSnapshot struct {
	ids     map[string][]any
	rows    map[string]map[int64]repairObservedRow
	repairs map[int64]rawRepair
}

// Only an explicit date_backfill may repair target damage. An ordinary poll or
// recent-window replay continues to fail raw verification instead of silently
// overwriting out-of-band changes. No target-only source ID is enumerated here.
func inspectRawRepairs(ctx context.Context, tx *sql.Tx, batch writerBatch, previous map[int64]writerState, current map[int64]contribution, hashes map[int64][32]byte) (*repairSnapshot, error) {
	if (batch.WorkflowID == "" && (batch.Scan == nil || batch.Scan.Type != "date_backfill")) || len(batch.Rows) == 0 {
		return nil, nil
	}
	s := &repairSnapshot{ids: map[string][]any{}, rows: map[string]map[int64]repairObservedRow{}, repairs: map[int64]rawRepair{}}
	for id, next := range current {
		s.ids[next.month()] = append(s.ids[next.month()], id)
		if old, ok := previous[id]; ok && old.month() != next.month() {
			s.ids[old.month()] = append(s.ids[old.month()], id)
		}
	}
	for month := range s.ids {
		sort.Slice(s.ids[month], func(i, j int) bool { return s.ids[month][i].(int64) < s.ids[month][j].(int64) })
	}
	var err error
	s.rows, err = readRepairRows(ctx, tx, batch.Columns, s.ids, false)
	if err != nil {
		return nil, err
	}
	for id, next := range current {
		old, exists := previous[id]
		if !exists {
			// An untracked target row has no reliable statistics predecessor.
			// Never guess a delta or adopt it as a newly inserted source row.
			if s.rows[next.month()][id].exists {
				return nil, ErrWriterCheckpoint
			}
			continue
		}
		if old.month() != next.month() && s.rows[next.month()][id].exists {
			// A second copy in the destination is target-extra evidence. Keep
			// both copies for explicit assessment rather than overwrite it.
			return nil, ErrWriterCheckpoint
		}
		before := s.rows[old.month()][id]
		afterHash := hashes[id]
		if !before.exists || before.hash != afterHash {
			kind := "source_changed"
			if !before.exists {
				kind = "target_missing"
			} else if !bytes.Equal(before.hash[:], old.hash) {
				kind = "target_mismatch"
			}
			s.repairs[id] = rawRepair{id: id, month: old.month(), before: before, afterHash: hashes[id], afterDate: next.Day, kind: kind}
		}
	}
	return s, nil
}

func readRepairRows(ctx context.Context, tx *sql.Tx, columns []string, ids map[string][]any, lock bool) (map[string]map[int64]repairObservedRow, error) {
	quoted := make([]string, len(columns))
	for i, column := range columns {
		quoted[i] = quote(column)
	}
	months := make([]string, 0, len(ids))
	for month := range ids {
		months = append(months, month)
	}
	sort.Strings(months)
	result := make(map[string]map[int64]repairObservedRow, len(months))
	for _, month := range months {
		query := "SELECT " + strings.Join(quoted, ",") + " FROM " + quote("logs_"+month) + " WHERE id IN (" + strings.TrimSuffix(strings.Repeat("?,", len(ids[month])), ",") + ") ORDER BY id"
		if lock {
			query += " FOR UPDATE"
		}
		rows, err := tx.QueryContext(ctx, query, ids[month]...)
		if err != nil {
			return nil, errors.New("archive repair target read failed")
		}
		found := map[int64]repairObservedRow{}
		for rows.Next() {
			raw := make([]sql.RawBytes, len(columns))
			args := make([]any, len(columns))
			for i := range raw {
				args[i] = &raw[i]
			}
			if rows.Scan(args...) != nil {
				rows.Close()
				return nil, errors.New("archive repair target scan failed")
			}
			row := make([]any, len(columns))
			var id int64
			var created *int64
			for i, name := range columns {
				if raw[i] != nil {
					row[i] = string(raw[i])
				}
				if name == "id" {
					id, _ = strconv.ParseInt(string(raw[i]), 10, 64)
				}
				if name == "created_at" && raw[i] != nil {
					if n, e := strconv.ParseInt(string(raw[i]), 10, 64); e == nil {
						created = &n
					}
				}
			}
			date := ""
			if created != nil && *created > 0 {
				day := time.Unix(*created, 0).In(archiveLocation)
				if day.Year() >= 1970 && day.Year() <= 9999 {
					date = day.Format("2006-01-02")
				}
			}
			if date == "" {
				rows.Close()
				return nil, &scanError{code: "repair_unscoped_target", sourceID: id}
			}
			hash, err := writerRawHash(columns, row)
			if err != nil || id <= 0 {
				rows.Close()
				return nil, ErrWriterCheckpoint
			}
			found[id] = repairObservedRow{exists: true, hash: hash, date: date}
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return nil, err
		}
		var engine string
		if tx.QueryRowContext(ctx, `SELECT ENGINE FROM information_schema.TABLES WHERE TABLE_SCHEMA=DATABASE() AND TABLE_NAME=?`, "logs_"+month).Scan(&engine) != nil || !strings.EqualFold(engine, "InnoDB") {
			return nil, errors.New("archive repair target requires InnoDB")
		}
		result[month] = found
	}
	return result, nil
}

// The first read collects every actual date before date locks. Recheck under
// row locks after the date/state locks to reject concurrent external changes.
func (s *repairSnapshot) lockAndCheck(ctx context.Context, tx *sql.Tx, columns []string) error {
	if s == nil {
		return nil
	}
	locked, err := readRepairRows(ctx, tx, columns, s.ids, true)
	if err != nil {
		return err
	}
	if !reflect.DeepEqual(s.rows, locked) {
		return errors.New("archive repair target changed outside writer fence")
	}
	return nil
}

func (s *repairSnapshot) writeAudit(ctx context.Context, tx *sql.Tx, grant af.WriterGrant, batch writerBatch) error {
	if s == nil {
		return nil
	}
	batchID, _ := af.IDBytes(batch.ID)
	identity, attempt := batch.WorkflowID, 1
	if batch.Scan != nil { identity, attempt = batch.Scan.TaskID, batch.Scan.Attempt }
	taskID, _ := af.IDBytes(identity)
	ids := make([]int64, 0, len(s.repairs))
	for id := range s.repairs {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	for _, id := range ids {
		r := s.repairs[id]
		var beforeHash, beforeDate any
		if r.before.exists {
			beforeHash = r.before.hash[:]
			beforeDate = r.before.date
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO archive_raw_repairs(batch_id,source_id,target_table,task_id,attempt,writer_epoch,repair_kind,before_date,after_date,before_row_hash,after_row_hash,committed_at) VALUES(?,?,?,?,?,?,?,?,?,?,?,UTC_TIMESTAMP(6))`, batchID, id, "logs_"+r.month, taskID, attempt, grant.WriterEpoch, r.kind, beforeDate, r.afterDate, beforeHash, r.afterHash[:]); err != nil {
			return errors.New("archive repair audit write failed")
		}
	}
	return nil
}
