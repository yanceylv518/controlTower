package logarchive

import (
	"context"
	"controltower/agent/internal/fileatomic"
	ac "controltower/internal/archivecontrol"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"errors"
	"hash"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

type dayScan struct {
	index           string
	unix, id, count int64
	done            bool
	digest          hash.Hash
}
type Reconciler struct {
	result         ac.Reconciliation
	source, target dayScan
	from, to       int64
	table          string
	columns        []string
	initialized    bool
}

func (w *Worker) NewReconciler(id, date string) (*Reconciler, error) {
	d, err := time.ParseInLocation("2006-01-02", date, time.FixedZone("Beijing", 28800))
	if err != nil {
		return nil, errors.New("invalid reconciliation date")
	}
	r := &Reconciler{result: ac.Reconciliation{ID: id, Date: date, State: "running"}, from: d.Unix(), to: d.AddDate(0, 0, 1).Unix(), table: "logs_" + d.Format("200601")}
	r.source = dayScan{unix: r.from, digest: sha256.New()}
	r.target = dayScan{unix: r.from, digest: sha256.New()}
	var saved ac.Reconciliation
	if b, e := os.ReadFile(w.path + ".reconcile.json"); e == nil && json.Unmarshal(b, &saved) == nil && saved.ID == id && saved.Date == date && saved.State != "running" {
		r.result = saved
	}
	return r, nil
}
func (r *Reconciler) Result() ac.Reconciliation { return r.result }

// Step reads at most one bounded page from each database, without a long-lived
// transaction or source writes. Data must remain stable during reconciliation.
func (w *Worker) ReconcileStep(ctx context.Context, r *Reconciler) error {
	if r.result.State != "running" {
		return nil
	}
	err := w.reconcileStep(ctx, r)
	if err != nil {
		r.result.State = "failed"
		r.result.Error = err.Error()
	}
	r.result.SourceRows = r.source.count
	r.result.TargetRows = r.target.count
	if r.result.State != "running" {
		now := time.Now().UTC()
		r.result.FinishedAt = &now
		b, _ := json.Marshal(r.result)
		if e := os.MkdirAll(filepath.Dir(w.path), 0700); e != nil {
			return e
		}
		if e := fileatomic.WriteFile(w.path+".reconcile.json", b, 0600); e != nil {
			return errors.New("cannot save reconciliation result")
		}
	}
	return err
}

func (w *Worker) reconcileStep(ctx context.Context, r *Reconciler) error {
	if !r.initialized {
		if err := w.Check(ctx); err != nil {
			return err
		}
		var now int64
		if w.source.QueryRowContext(ctx, "SELECT UNIX_TIMESTAMP()").Scan(&now) != nil || r.to > now-int64(w.delay/time.Second) {
			return errors.New("reconciliation requires a completed stable day")
		}
		var indexName string
		if w.source.QueryRowContext(ctx, `SELECT INDEX_NAME FROM information_schema.STATISTICS WHERE TABLE_SCHEMA=DATABASE() AND TABLE_NAME='logs' AND COLUMN_NAME='created_at' AND SEQ_IN_INDEX=1 LIMIT 1`).Scan(&indexName) != nil {
			return errors.New("source requires a created_at-leading index for daily reconciliation")
		}
		r.source.index = indexName
		rows, e := w.source.QueryContext(ctx, "SELECT * FROM logs LIMIT 0")
		if e != nil {
			return errors.New("source reconciliation schema unavailable")
		}
		r.columns, e = rows.Columns()
		rows.Close()
		if e != nil {
			return e
		}
		var exists int
		if w.target.QueryRowContext(ctx, "SELECT COUNT(*) FROM information_schema.TABLES WHERE TABLE_SCHEMA=DATABASE() AND TABLE_NAME=?", r.table).Scan(&exists) != nil {
			return errors.New("target reconciliation table lookup failed")
		}
		if exists == 0 {
			r.target.done = true
		}
		r.initialized = true
	}
	if err := scanDay(ctx, w.source, "logs", r.columns, r.from, r.to, w.batchSize, &r.source); err != nil {
		return err
	}
	if err := scanDay(ctx, w.target, r.table, r.columns, r.from, r.to, w.batchSize, &r.target); err != nil {
		return err
	}
	if r.source.done && r.target.done {
		r.result.State = "mismatched"
		if r.source.count == r.target.count && string(r.source.digest.Sum(nil)) == string(r.target.digest.Sum(nil)) {
			r.result.State = "matched"
		}
	}
	return nil
}

func scanDay(ctx context.Context, db *sql.DB, table string, columns []string, from, to int64, limit int, s *dayScan) error {
	if s.done {
		return nil
	}
	names := make([]string, len(columns))
	idIndex, tsIndex := -1, -1
	for i, c := range columns {
		names[i] = quote(c)
		if c == "id" {
			idIndex = i
		}
		if c == "created_at" {
			tsIndex = i
		}
	}
	if idIndex < 0 || tsIndex < 0 {
		return errors.New("reconciliation columns missing")
	}
	fromTable := quote(table)
	if s.index != "" {
		fromTable += " FORCE INDEX (" + quote(s.index) + ")"
	}
	rows, err := db.QueryContext(ctx, "SELECT "+strings.Join(names, ",")+" FROM "+fromTable+" WHERE created_at>=? AND created_at<? AND (created_at>? OR (created_at=? AND id>?)) ORDER BY created_at,id LIMIT ?", from, to, s.unix, s.unix, s.id, limit)
	if err != nil {
		return errors.New("reconciliation page query failed")
	}
	defer rows.Close()
	n := 0
	for rows.Next() {
		raw := make([]sql.RawBytes, len(columns))
		dest := make([]any, len(columns))
		for i := range raw {
			dest[i] = &raw[i]
		}
		if rows.Scan(dest...) != nil {
			return errors.New("reconciliation page scan failed")
		}
		id, e := strconv.ParseInt(string(raw[idIndex]), 10, 64)
		if e != nil {
			return errors.New("invalid reconciliation id")
		}
		ts, e := strconv.ParseInt(string(raw[tsIndex]), 10, 64)
		if e != nil || ts < s.unix || (ts == s.unix && id <= s.id) {
			return errors.New("invalid reconciliation cursor")
		}
		// Encoding []byte as base64 preserves arbitrary bytes and distinguishes NULL.
		if json.NewEncoder(s.digest).Encode(raw) != nil {
			return errors.New("reconciliation digest failed")
		}
		s.id = id
		s.unix = ts
		s.count++
		n++
	}
	if rows.Err() != nil {
		return errors.New("reconciliation page read failed")
	}
	s.done = n < limit
	return nil
}
