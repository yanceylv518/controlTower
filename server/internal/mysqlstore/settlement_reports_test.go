package mysqlstore

import (
	"context"
	"controltower/server/internal/billing"
	"errors"
	"fmt"
	"os"
	"testing"
	"time"
)

func TestPersistentReportLifecycle(t *testing.T) {
	dsn := os.Getenv("CT_MYSQL_TEST_DSN")
	if dsn == "" {
		t.Skip("requires local database")
	}
	db, e := Open(dsn)
	if e != nil {
		t.Fatal(e)
	}
	defer db.Close()
	ctx := context.Background()
	if e = ApplyDir(ctx, db, "../../migrations"); e != nil {
		t.Fatal(e)
	}
	s := New(db)
	site := fmt.Sprintf("report-test-%d", time.Now().UnixNano())
	defer func() {
		db.Exec(`DELETE FROM settlement_report_days WHERE instance_id=?`, site)
		db.Exec(`DELETE FROM settlement_report_tasks WHERE instance_id=?`, site)
	}()
	makeTask := func(id string, overwrite bool) billing.ReportTask {
		v, e := s.CreateReportTask(ctx, billing.ReportTask{ID: site + id, Site: site, From: "2025-01-01", To: "2025-01-03", Overwrite: overwrite})
		if e != nil {
			t.Fatal(e)
		}
		return v
	}
	claim := func(v billing.ReportTask, day string) {
		_, e := db.Exec(`UPDATE settlement_report_tasks SET status='running' WHERE id=?`, v.ID)
		if e != nil {
			t.Fatal(e)
		}
		_, e = db.Exec(`UPDATE settlement_report_task_days SET status='running' WHERE task_id=? AND bill_day=?`, v.ID, day)
		if e != nil {
			t.Fatal(e)
		}
	}
	task := makeTask("-a", false)
	if _, e = s.CreateReportTask(ctx, billing.ReportTask{ID: site + "-busy", Site: site}); !errors.Is(e, billing.ErrReportBusy) {
		t.Fatalf("busy: %v", e)
	}
	day := billing.ReportTaskDay{Day: "2025-01-01"}
	claim(task, day.Day)
	start, _ := time.ParseInLocation("2006-01-02", day.Day, billing.BusinessLocation)
	zero := int64(0)
	doc := billing.ReportDocument{Items: []billing.ReportRow{}, FailedRequests: &zero, From: start, To: start.AddDate(0, 0, 1), Currency: billing.CurrencyDisplay{Type: "CNY"}}
	if e = s.ReportTaskProgress(ctx, task.ID, day.Day, 7); e != nil {
		t.Fatal(e)
	}
	if e = s.FinishReportTaskDay(ctx, task, day, &doc, ""); e != nil {
		t.Fatal(e)
	}
	if e = s.CancelReportTask(ctx, site, task.ID); e != nil {
		t.Fatal(e)
	}
	docs, e := s.ReadReportDays(ctx, site, "2025-01-01", "2025-01-03")
	if e != nil || len(docs) != 1 {
		t.Fatalf("cancel lost completed day: %v %v", docs, e)
	}
	if e = s.FinishReportTaskDay(ctx, task, billing.ReportTaskDay{Day: "2025-01-02"}, &doc, ""); !errors.Is(e, billing.ErrReportCancelled) {
		t.Fatalf("cancel publish: %v", e)
	}
	reuse := makeTask("-reuse", false)
	if reuse.Days[0].Status != "reused" || reuse.Days[1].Status != "pending" {
		t.Fatalf("reuse: %+v", reuse)
	}
	s.CancelReportTask(ctx, site, reuse.ID)
	over := makeTask("-overwrite", true)
	claim(over, day.Day)
	if e = s.FinishReportTaskDay(ctx, over, day, nil, "source unavailable"); e != nil {
		t.Fatal(e)
	}
	docs, e = s.ReadReportDays(ctx, site, "2025-01-01", "2025-01-03")
	if e != nil || len(docs) != 1 {
		t.Fatal("failed overwrite erased old report")
	}
	s.CancelReportTask(ctx, site, over.ID)
	all, e := s.CreateReportTask(ctx, billing.ReportTask{ID: site + "-all", Site: site, From: "2025-01-01", To: "2025-01-02"})
	if e != nil || all.Status != "complete" {
		t.Fatalf("all reused should complete: %+v %v", all, e)
	}
	tasks, e := s.ListReportTasks(ctx, site, 100)
	if e != nil || len(tasks) != 4 {
		t.Fatalf("history: %d %v", len(tasks), e)
	}
}
