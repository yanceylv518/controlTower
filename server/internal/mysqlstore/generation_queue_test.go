package mysqlstore

import (
	"context"
	"controltower/server/internal/billing"
	"crypto/sha256"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"testing"
	"time"
)

func TestGenerationSiteQueueMySQL(t *testing.T) {
	dsn := os.Getenv("CT_MYSQL_TEST_DSN")
	if dsn == "" {
		t.Skip("requires isolated CT_MYSQL_TEST_DSN")
	}
	db, err := Open(dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err = ApplyDir(ctx, db, "../../migrations"); err != nil {
		t.Fatal(err)
	}
	s := New(db)
	a := fmt.Sprintf("gen-a-%d", time.Now().UnixNano())
	b := a + "-b"
	defer func() {
		for _, table := range []string{"settlement_report_days", "settlement_report_tasks", "billing_jobs", "billing_money_snapshots"} {
			if _, e := db.Exec("DELETE FROM "+table+" WHERE instance_id IN (?,?)", a, b); e != nil {
				t.Error(e)
			}
		}
	}()
	leaseA, unlockA, err := s.LockGenerationSite(ctx, a)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if unlockA != nil {
			unlockA()
		}
	}()
	if _, _, err = s.LockGenerationSite(ctx, a); !errors.Is(err, billing.ErrGenerationInProgress) {
		t.Fatal("same site acquired twice", err)
	}
	_, unlockB, err := s.LockGenerationSite(ctx, b)
	if err != nil {
		t.Fatal("other site blocked", err)
	}
	unlockB()
	from := time.Date(2025, 1, 1, 0, 0, 0, 0, billing.BusinessLocation)
	report, e := s.CreateReportTask(ctx, billing.ReportTask{ID: a + "-report", Site: a, From: "2025-01-01", To: "2025-01-03", Overwrite: true})
	if e != nil {
		t.Fatal(e)
	}
	newBill := func(site string) billing.Job {
		j, steps, e := billing.NewJob(site, from, from.AddDate(0, 0, 1), "test")
		if e != nil {
			t.Fatal(e)
		}
		if e = s.CreateBillingJob(ctx, j, steps); e != nil {
			t.Fatal(e)
		}
		return j
	}
	ja, jb := newBill(a), newBill(b)
	check := func(site, kind, id string) {
		t.Helper()
		v, e := s.NextGeneration(ctx, site)
		if e != nil || v.Kind != kind || v.ID != id {
			t.Fatal("queue selection", v, e, kind, id)
		}
	}
	check(a, "billing", ja.ID)
	check(b, "billing", jb.ID)
	if list, e := s.ListReportTasks(ctx, a, 10); e != nil || len(list) != 1 || list[0].WaitingFor != "billing" {
		t.Fatal("report wait reason", list, e)
	}
	if _, _, ok, e := s.ForGeneration(b, ja.ID).ClaimBillingStep(ctx); e != nil || ok {
		t.Fatal("cross-site claim", ok, e)
	}
	if _, _, ok, e := s.ForGeneration(b, jb.ID).ClaimBillingStep(ctx); e != nil || !ok {
		t.Fatal("independent billing claim", ok, e)
	}
	// Upgrade may encounter both kinds marked running by the old workers.
	// Waiting explanations must follow the single selected owner.
	legacy, e := s.CreateReportTask(ctx, billing.ReportTask{ID: b + "-legacy", Site: b, From: "2025-01-01", To: "2025-01-02", Overwrite: true})
	if e != nil {
		t.Fatal(e)
	}
	if _, e = db.ExecContext(ctx, `UPDATE settlement_report_tasks SET status='running' WHERE id=?`, legacy.ID); e != nil {
		t.Fatal(e)
	}
	if waiting, e := s.GenerationWaitingFor(ctx, b, "report"); e != nil || waiting != "billing" {
		t.Fatal("legacy state ignored queue owner", waiting, e)
	}
	if e = s.CancelBillingJob(ctx, ja.ID); e != nil {
		t.Fatal(e)
	}
	check(a, "report", report.ID)
	running, day, e := s.ForGeneration(a, report.ID).NextReportTaskDay(ctx)
	if e != nil {
		t.Fatal(e)
	}
	ja = newBill(a)
	check(a, "report", report.ID) // a new bill cannot interrupt an active report
	if waiting, e := s.GenerationWaitingFor(ctx, a, "billing"); e != nil || waiting != "report" {
		t.Fatal(waiting, e)
	}
	if waiting, e := s.GenerationWaitingFor(ctx, b, "billing"); e != nil || waiting != "" {
		t.Fatal("cross-site wait", waiting, e)
	}
	if e = s.ForGeneration(b, "").RecoverReportTasks(ctx); e != nil {
		t.Fatal(e)
	}
	var state string
	if e = db.QueryRowContext(ctx, `SELECT status FROM settlement_report_task_days WHERE task_id=? AND bill_day=?`, report.ID, day.Day).Scan(&state); e != nil || state != "running" {
		t.Fatal("recovered another site", state, e)
	}
	if e = s.FinishReportTaskDay(ctx, running, day, &billing.ReportDocument{From: from, To: from.AddDate(0, 0, 1)}, ""); e != nil {
		t.Fatal(e)
	}
	check(a, "report", report.ID) // whole range stays together
	if e = s.CancelReportTask(ctx, a, report.ID); e != nil {
		t.Fatal(e)
	}
	check(a, "billing", ja.ID)
	if e = s.RetryReportTask(ctx, a, report.ID); e != nil {
		t.Fatal(e)
	}
	check(a, "billing", ja.ID) // resumed pending report yields to queued billing
	// Losing the exact database session cancels execution before reacquisition.
	key := fmt.Sprintf("ct:generation:%x", sha256.Sum256([]byte(a)))[:64]
	var connection int64
	if e = db.QueryRowContext(ctx, `SELECT IS_USED_LOCK(?)`, key).Scan(&connection); e != nil {
		t.Fatal(e)
	}
	if _, e = db.ExecContext(ctx, fmt.Sprintf("KILL CONNECTION %d", connection)); e != nil {
		t.Fatal(e)
	}
	select {
	case <-leaseA.Done():
	case <-time.After(4 * time.Second):
		t.Fatal("lost site lease did not cancel")
	}
	unlockA()
	unlockA = nil
	_, again, e := s.LockGenerationSite(ctx, a)
	if e != nil {
		t.Fatal("site not recoverable", e)
	}
	again()
	if _, e = s.NextGeneration(ctx, a+"-absent"); !errors.Is(e, sql.ErrNoRows) {
		t.Fatal(e)
	}
	for i := 0; i < 5; i++ {
		j := newBill(a)
		if _, e = db.ExecContext(ctx, `UPDATE billing_jobs SET job_type='user_statement' WHERE id=?`, j.ID); e != nil {
			t.Fatal(e)
		}
	}
	if full, e := s.BillingStatementQueueFull(ctx, a); e != nil || !full {
		t.Fatal("site a capacity", full, e)
	}
	if full, e := s.BillingStatementQueueFull(ctx, b); e != nil || full {
		t.Fatal("site b capacity leaked", full, e)
	}
	j, steps, _ := billing.NewJob(b, from, from.AddDate(0, 0, 1), "test")
	j.JobType = "user_statement"
	j.UserID = 7
	j.BillPeriod = "daily"
	j.UsageVersion = 3
	j.RequestKey = j.ID
	j.MoneySnapshot, _ = billing.NewMoneySnapshot(b, `{"QuotaPerUnit":500000}`, time.Now())
	if e = s.CreateBillingStatementJob(ctx, j, steps, ""); e != nil {
		t.Fatal("site a full blocked site b", e)
	}
}

// Existing amount/coverage tests now explicitly execute the queued month.
func publishQueuedMonthForTest(t *testing.T, s Store, ctx context.Context, job billing.Job) {
	t.Helper()
	stored, e := s.BillingJob(ctx, job.ID)
	if e != nil {
		t.Fatal(e)
	}
	if stored.Status != "pending" {
		t.Fatal("month was not queued", stored.Status)
	}
	var rows int
	if e = s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM billing_compact_daily_totals WHERE job_id=?`, job.ID).Scan(&rows); e != nil || rows != 0 {
		t.Fatal("month copied during submission", rows, e)
	}
	claimed, ok, e := s.ForGeneration(job.InstanceID, job.ID).ClaimBillingPublish(ctx)
	if e != nil || !ok {
		t.Fatal(claimed, ok, e)
	}
	if e = s.CompleteBillingMonth(ctx, claimed); e != nil {
		t.Fatal(e)
	}
}

func createStatementAndPublishTestMonth(t *testing.T, s Store, ctx context.Context, job billing.Job, steps []billing.JobStep, name string) error {
	t.Helper()
	err := s.CreateBillingStatementJob(ctx, job, steps, name)
	if err == nil && job.BillPeriod == "monthly" && job.UsageVersion >= 3 {
		publishQueuedMonthForTest(t, s, ctx, job)
	}
	return err
}
