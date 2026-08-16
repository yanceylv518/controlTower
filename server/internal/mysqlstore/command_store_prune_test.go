package mysqlstore

import (
	"context"
	"database/sql"
	"errors"
	"testing"
	"time"
)

type pruneResult struct {
	rows int64
	err  error
}

func (r pruneResult) LastInsertId() (int64, error) { return 0, nil }
func (r pruneResult) RowsAffected() (int64, error) { return r.rows, r.err }

type pruneExecutorFake struct {
	results []pruneResult
	execErr error
	calls   int
	query   string
	args    []any
}

func (f *pruneExecutorFake) ExecContext(_ context.Context, query string, args ...any) (sql.Result, error) {
	f.calls++
	f.query = query
	f.args = args
	if f.execErr != nil && f.calls > len(f.results) {
		return nil, f.execErr
	}
	if f.calls > len(f.results) {
		return pruneResult{}, nil
	}
	return f.results[f.calls-1], nil
}

func TestPruneInBatchesAccumulatesUntilShortBatch(t *testing.T) {
	cutoff := time.Date(2026, 8, 16, 0, 0, 0, 0, time.UTC)
	fake := &pruneExecutorFake{results: []pruneResult{{rows: pruneBatchSize}, {rows: pruneBatchSize}, {rows: 7}}}

	total, err := pruneInBatches(fake, "DELETE FROM metric_1m WHERE bucket_time < ? LIMIT ?", cutoff)
	if err != nil {
		t.Fatal(err)
	}
	if total != 2*pruneBatchSize+7 || fake.calls != 3 {
		t.Fatalf("total=%d calls=%d", total, fake.calls)
	}
	if fake.query != "DELETE FROM metric_1m WHERE bucket_time < ? LIMIT ?" || len(fake.args) != 2 || fake.args[0] != cutoff || fake.args[1] != pruneBatchSize {
		t.Fatalf("unexpected query invocation: query=%q args=%#v", fake.query, fake.args)
	}
}

func TestPruneInBatchesRunsFinalEmptyBatchAfterExactBoundary(t *testing.T) {
	fake := &pruneExecutorFake{results: []pruneResult{{rows: pruneBatchSize}, {rows: 0}}}
	total, err := pruneInBatches(fake, "DELETE", time.Time{})
	if err != nil || total != pruneBatchSize || fake.calls != 2 {
		t.Fatalf("total=%d calls=%d err=%v", total, fake.calls, err)
	}
}

func TestPruneInBatchesReturnsProgressAndExecutionError(t *testing.T) {
	wantErr := errors.New("write timeout")
	fake := &pruneExecutorFake{results: []pruneResult{{rows: pruneBatchSize}}, execErr: wantErr}
	total, err := pruneInBatches(fake, "DELETE", time.Time{})
	if !errors.Is(err, wantErr) || total != pruneBatchSize || fake.calls != 2 {
		t.Fatalf("total=%d calls=%d err=%v", total, fake.calls, err)
	}
}

func TestPruneInBatchesReturnsRowsAffectedError(t *testing.T) {
	wantErr := errors.New("rows affected unavailable")
	fake := &pruneExecutorFake{results: []pruneResult{{rows: 9, err: wantErr}}}
	total, err := pruneInBatches(fake, "DELETE", time.Time{})
	if !errors.Is(err, wantErr) || total != 0 || fake.calls != 1 {
		t.Fatalf("total=%d calls=%d err=%v", total, fake.calls, err)
	}
}
