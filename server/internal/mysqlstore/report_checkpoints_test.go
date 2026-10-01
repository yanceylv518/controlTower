package mysqlstore

import (
	"context"
	"errors"
	"fmt"
	"os"
	"testing"
	"time"

	"controltower/server/internal/billing"
)

func TestReportCheckpointLifecycle(t *testing.T) {
	dsn := os.Getenv("CT_MYSQL_TEST_DSN")
	if dsn == "" {
		t.Skip("requires isolated CT_MYSQL_TEST_DSN")
	}
	db, err := Open(dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ctx := context.Background()
	if err = ApplyDir(ctx, db, "../../migrations"); err != nil {
		t.Fatal(err)
	}
	s := New(db)
	site := fmt.Sprintf("report-resume-%d", time.Now().UnixNano())
	defer db.Exec(`DELETE FROM settlement_report_tasks WHERE instance_id=?`, site)
	defer db.Exec(`DELETE FROM settlement_report_days WHERE instance_id=?`, site)
	day := billing.ReportTaskDay{Day: "2025-01-01"}
	from, _ := time.ParseInLocation("2006-01-02", day.Day, billing.BusinessLocation)
	create := func(suffix string) billing.ReportTask {
		v, e := s.CreateReportTask(ctx, billing.ReportTask{ID: site + suffix, Site: site, From: day.Day, To: "2025-01-02", Overwrite: true})
		if e != nil {
			t.Fatal(e)
		}
		return v
	}
	claim := func() {
		_, _, e := s.NextReportTaskDay(ctx)
		if e != nil {
			t.Fatal(e)
		}
	}
	original := create("-original")
	claim()
	cp := &billing.ReportCheckpoint{Version: 1, Site: site, From: from, To: from.AddDate(0, 0, 1), Processed: 20000, Cursor: billing.LogCursor{CreatedUnix: from.Unix() + 1, ID: 20000}, Groups: map[string]billing.ReportAccumulator{"a": {Amount: "1/3", Cost: "2/7", Raw: "1/2"}}}
	if err = s.SaveReportCheckpoint(ctx, original.ID, day.Day, cp); err != nil {
		t.Fatal(err)
	}
	if err = s.ReportTaskProgress(ctx, original.ID, day.Day, 22000); err != nil {
		t.Fatal(err)
	}
	if err = s.RecoverReportTasks(ctx); err != nil {
		t.Fatal(err)
	}
	_, claimed, err := s.NextReportTaskDay(ctx)
	if err != nil || claimed.Processed != 20000 {
		t.Fatal("recovery ignored durable count", claimed, err)
	}
	saved, err := s.LoadReportCheckpoint(ctx, original.ID, day.Day)
	if err != nil || saved.Cursor != cp.Cursor || saved.Groups["a"].Amount != "1/3" {
		t.Fatal(saved, err)
	}
	if err = s.FinishReportTaskDay(ctx, original, day, nil, "interrupted"); err != nil {
		t.Fatal(err)
	}
	if err = s.RetryReportTask(ctx, site+"other", original.ID); err == nil {
		t.Fatal("cross-site retry allowed")
	}
	busy := create("-busy")
	if err = s.RetryReportTask(ctx, site, original.ID); !errors.Is(err, billing.ErrReportBusy) {
		t.Fatal("retry bypassed site lock", err)
	}
	if err = s.CancelReportTask(ctx, site, busy.ID); err != nil {
		t.Fatal(err)
	}
	if err = s.RetryReportTask(ctx, site, original.ID); err != nil {
		t.Fatal(err)
	}
	claim()
	if err = s.CancelReportTask(ctx, site, original.ID); err != nil {
		t.Fatal(err)
	}
	if err = s.SaveReportCheckpoint(ctx, original.ID, day.Day, cp); !errors.Is(err, billing.ErrReportCancelled) {
		t.Fatal("cancelled checkpoint written", err)
	}
	if err = s.RetryReportTask(ctx, site, original.ID); err != nil {
		t.Fatal(err)
	}
	claim()
	doc := &billing.ReportDocument{From: from, To: from.AddDate(0, 0, 1), Items: []billing.ReportRow{{Amount: "1.250000"}}}
	if err = s.FinishReportTaskDay(ctx, original, day, doc, ""); err != nil {
		t.Fatal(err)
	}
	if saved, err = s.LoadReportCheckpoint(ctx, original.ID, day.Day); err != nil || saved != nil {
		t.Fatal("published checkpoint not removed", err)
	}
	old := create("-old")
	claim()
	if err = s.SaveReportCheckpoint(ctx, old.ID, day.Day, cp); err != nil {
		t.Fatal(err)
	}
	if err = s.FinishReportTaskDay(ctx, old, day, nil, "failed"); err != nil {
		t.Fatal(err)
	}
	docs, err := s.ReadReportDays(ctx, site, day.Day, "2025-01-02")
	if err != nil || len(docs) != 1 || docs[0].Items[0].Amount != "1.250000" {
		t.Fatal("failed overwrite changed visible report", err)
	}
	newer := create("-newer")
	claim()
	if err = s.FinishReportTaskDay(ctx, newer, day, doc, ""); err != nil {
		t.Fatal(err)
	}
	if err = s.RetryReportTask(ctx, site, old.ID); err == nil || err.Error() != "report_retry_obsolete" {
		t.Fatal("obsolete task allowed to overwrite newer report", err)
	}
}
