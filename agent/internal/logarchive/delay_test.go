package logarchive

import (
	"context"
	"database/sql/driver"
	"testing"
)

func TestDelayStopsAtFirstRecentIDAndResumes(t *testing.T) {
	w, src, dst := worker(t)
	src.sourceNow = 1800000000
	row := func(id, ts int64) []driver.Value {
		return []driver.Value{id, "body", ts, int64(2), int64(1), int64(2), int64(3)}
	}
	// ID 3 is older but must not be processed by jumping over recent ID 2.
	src.sourceRows = [][]driver.Value{row(1, src.sourceNow-300), row(2, src.sourceNow-20), row(3, src.sourceNow-600)}
	n, err := w.Pass(context.Background())
	if err != nil || n != 1 {
		t.Fatalf("first batch: %d %v", n, err)
	}
	cp, _ := w.load()
	if cp.AfterID != 1 {
		t.Fatalf("skipped a recent row: %+v", cp)
	}
	n, err = w.Pass(context.Background())
	if err != nil || n != 0 || dst.commits != 1 {
		t.Fatalf("waiting: %d %v", n, err)
	}
	src.sourceNow += 300
	n, err = w.Pass(context.Background())
	if err != nil || n != 2 {
		t.Fatalf("resume: %d %v", n, err)
	}
	cp, _ = w.load()
	if cp.AfterID != 3 {
		t.Fatalf("cursor: %+v", cp)
	}
}
