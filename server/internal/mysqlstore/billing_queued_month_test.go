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

func TestQueuedMonthInvalidatedByNewDailySnapshot(t *testing.T) {
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
	site := fmt.Sprintf("queued-month-%d", time.Now().UnixNano())
	defer func() {
		for _, table := range []string{"billing_generation_tasks", "billing_compact_daily_totals", "billing_jobs", "billing_money_snapshots"} {
			if _, e := db.Exec("DELETE FROM "+table+" WHERE instance_id=?", site); e != nil {
				t.Error(e)
			}
		}
	}()
	from := time.Date(2025, 9, 1, 0, 0, 0, 0, billing.BusinessLocation)
	money, _ := billing.NewMoneySnapshot(site, `{"QuotaPerUnit":500000}`, time.Now())
	day := func(offset int) {
		j, _, _ := billing.NewJob(site, from.AddDate(0, 0, offset), from.AddDate(0, 0, offset+1), "test")
		j.JobType = "user_statement"
		j.UserID = 7
		j.UsageVersion = 3
		j.BillPeriod = "daily"
		j.RequestKey = j.ID
		j.MoneySnapshot = money
		j.TotalSteps = 0
		if e := s.CreateBillingStatementJob(ctx, j, nil, ""); e != nil {
			t.Fatal(e)
		}
		if _, e := db.Exec(`INSERT INTO billing_compact_daily_totals(job_id,instance_id,bill_day,user_id,model_name,request_count,total_amount,updated_at) VALUES(?,?,?,?,?,1,1.25,UTC_TIMESTAMP(6))`, j.ID, site, j.From.Format("2006-01-02"), 7, "m"); e != nil {
			t.Fatal(e)
		}
		tx, e := db.BeginTx(ctx, nil)
		if e != nil {
			t.Fatal(e)
		}
		defer tx.Rollback()
		if e = s.finalizeBillingStatement(ctx, tx, j, time.Now().UTC()); e != nil {
			t.Fatal(e)
		}
		if e = tx.Commit(); e != nil {
			t.Fatal(e)
		}
	}
	enqueueCtx := ctx
	month := func() billing.Job {
		j, _, _ := billing.NewJob(site, from, from.AddDate(0, 1, 0), "test")
		j.JobType = "user_statement"
		j.UserID = 7
		j.UsageVersion = 3
		j.BillPeriod = "monthly"
		j.RequestKey = "month:" + site
		if e := s.CreateBillingStatementJob(enqueueCtx, j, nil, ""); e != nil {
			t.Fatal(e)
		}
		return j
	}
	day(0)
	lease, release, err := s.LockGenerationSite(ctx, site)
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	if !s.ownsGenerationLease(lease, site) || s.ownsGenerationLease(ctx, site) || s.ownsGenerationLease(lease, site+"-other") {
		t.Fatal("lease ownership scope")
	}
	blocked, _, _ := billing.NewJob(site, from, from.AddDate(0, 1, 0), "test")
	blocked.JobType, blocked.BillPeriod, blocked.UserID, blocked.UsageVersion, blocked.RequestKey = "user_statement", "monthly", 7, 3, blocked.ID
	if err = s.CreateBillingStatementJob(ctx, blocked, nil, ""); !errors.Is(err, billing.ErrStatementQueueFull) {
		t.Fatal("external enqueue bypassed lease", err)
	}
	enqueueCtx = lease
	busy, steps, _ := billing.NewJob(site, from, from.AddDate(0, 0, 1), "test")
	if err = s.CreateBillingJob(lease, busy, steps); err != nil {
		t.Fatal(err)
	}
	claimed, step, ok, err := s.ForGeneration(site, busy.ID).ClaimBillingStep(lease)
	if err != nil || !ok {
		t.Fatal(err, ok)
	}
	if err = s.YieldBillingStep(lease, claimed, step); err != nil {
		t.Fatal(err)
	}
	var attempts int
	if err = db.QueryRowContext(lease, `SELECT attempts FROM billing_job_steps WHERE job_id=?`, busy.ID).Scan(&attempts); err != nil || attempts != 0 {
		t.Fatal("voluntary yield consumed retry", attempts, err)
	}
	old := month()
	if next, e := s.NextGeneration(lease, site); e != nil || next.ID != old.ID {
		t.Fatal("running day starved monthly publication", next, e)
	}
	if _, err = db.ExecContext(lease, `UPDATE billing_jobs SET status='cancelled' WHERE id=?`, busy.ID); err != nil {
		t.Fatal(err)
	}
	if j, e := s.BillingJob(ctx, old.ID); e != nil || j.Status != "pending" {
		t.Fatal(j.Status, e)
	}
	day(1)
	if j, e := s.BillingJob(ctx, old.ID); e != nil || j.Status != "superseded" {
		t.Fatal("stale pending month remained runnable", j.Status, e)
	}
	fresh := month()
	publishQueuedMonthForTest(t, s, ctx, fresh)
	var amount string
	if e := db.QueryRow(`SELECT CAST(SUM(total_amount) AS CHAR) FROM billing_compact_daily_totals WHERE job_id=?`, fresh.ID).Scan(&amount); e != nil || amount != "2.500000000000" {
		t.Fatal(amount, e)
	}
}
