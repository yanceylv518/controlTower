package logarchive

import (
	"context"
	"database/sql/driver"
	"strings"
	"testing"
	"time"
)

func TestDailyReconciliation(t *testing.T) {
	for _, mode := range []string{"equal", "changed", "missing", "extra", "no_index"} {
		t.Run(mode, func(t *testing.T) {
			w, src, dst := worker(t)
			w.SetBatchSize(1)
			w.WithDelay(300 * time.Second)
			ts := time.Date(2026, 9, 8, 2, 0, 0, 0, time.UTC).Unix()
			src.sourceNow = ts + 86400
			row := func(id int64, content driver.Value) []driver.Value {
				return []driver.Value{id, content, ts, int64(2), int64(1), int64(2), int64(3)}
			}
			src.sourceRows = [][]driver.Value{row(1, nil), row(2, []byte{0xff, 0x00})}
			dst.sourceRows = [][]driver.Value{row(1, nil), row(2, []byte{0xff, 0x00})}
			switch mode {
			case "changed":
				dst.sourceRows[0][1] = ""
			case "missing":
				dst.sourceRows = dst.sourceRows[:1]
			case "extra":
				dst.sourceRows = append(dst.sourceRows, row(3, "extra"))
			case "no_index":
				src.noDateIndex = true
			}
			r, err := w.NewReconciler(strings.Repeat("a", 32), "2026-09-08")
			if err != nil {
				t.Fatal(err)
			}
			for i := 0; i < 5 && r.Result().State == "running"; i++ {
				_ = w.ReconcileStep(context.Background(), r)
			}
			want := "mismatched"
			if mode == "equal" {
				want = "matched"
			}
			if mode == "no_index" {
				want = "failed"
			}
			if r.Result().State != want {
				t.Fatalf("result %+v", r.Result())
			}
			if src.commits != 0 || dst.commits != 0 {
				t.Fatal("reconciliation wrote database")
			}
			restored, e := w.NewReconciler(strings.Repeat("a", 32), "2026-09-08")
			if e != nil || restored.Result().State != want {
				t.Fatal("completed result not recovered", e)
			}
		})
	}
}
