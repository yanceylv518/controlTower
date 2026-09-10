package logarchive

import (
	"context"
	"database/sql/driver"
	"encoding/json"
	"os"
	"testing"
	"time"
)

func TestDailyReceiptSurvivesReplayAndFailedCommit(t *testing.T) {
	w, src, dst := worker(t)
	ts := time.Date(2026, 9, 8, 2, 0, 0, 0, time.UTC).Unix()
	src.sourceNow = ts + 3600
	row := func(id int64) []driver.Value {
		return []driver.Value{id, "body", ts, int64(2), int64(1), int64(2), int64(3)}
	}
	src.sourceRows = [][]driver.Value{row(1)}
	if _, err := w.Pass(context.Background()); err != nil {
		t.Fatal(err)
	}
	check := func(count string, id int64) {
		t.Helper()
		days, err := w.Days()
		if err != nil || len(days) != 1 {
			t.Fatalf("receipt: %+v %v", days, err)
		}
		d := days[0]
		if d.Date != "2026-09-08" || d.ArchivedRows != count || d.RequestRows != count || d.ErrorRows != "0" || d.LastID != id || d.VerifiedAt.IsZero() {
			t.Fatalf("receipt: %+v", d)
		}
	}
	check("1", 1)
	accepted, _ := w.Days()
	// Simulate a replay after a committed batch: the absolute daily count stays one.
	cp, _ := w.load()
	cp.AfterID = 0
	b, _ := json.Marshal(cp)
	if err := os.WriteFile(w.path, b, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := w.Pass(context.Background()); err != nil {
		t.Fatal(err)
	}
	check("1", 1)
	src.sourceRows = append(src.sourceRows, row(2))
	dst.failCommit = true
	if _, err := w.Pass(context.Background()); err == nil {
		t.Fatal("expected commit failure")
	}
	check("1", 1)
	dst.failCommit = false
	if _, err := w.Pass(context.Background()); err != nil {
		t.Fatal(err)
	}
	check("2", 2)
	if err := w.AcknowledgeDays(accepted); err != nil {
		t.Fatal(err)
	}
	check("2", 2) // An acknowledgement of the older batch cannot erase new progress.
	latest, _ := w.Days()
	if err := w.AcknowledgeDays(latest); err != nil {
		t.Fatal(err)
	}
	if days, err := w.Days(); err != nil || len(days) != 0 {
		t.Fatal("accepted receipt retained", days, err)
	}
}
