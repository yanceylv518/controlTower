package mysqlstore

import (
	"context"
	"controltower/server/internal/billing"
	"database/sql"
	"fmt"
	"os"
	"testing"
	"time"
)

// Reproduces an enqueue transaction whose read view predates daily completion.
// Daily invalidation cannot see the month because enqueue has not inserted it.
func TestQueuedMonthRejectsStaleEnqueueSnapshot(t *testing.T) {
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
	for _, tc := range []struct {
		name        string
		replace     bool
		empty       bool
		amount      string
		coveredDays int
		emptyDays   int
	}{
		{name: "new complete day", amount: "3.750000000000", coveredDays: 2},
		{name: "new empty day", empty: true, amount: "1.250000000000", coveredDays: 2, emptyDays: 1},
		{name: "replacement day", replace: true, amount: "2.500000000000", coveredDays: 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			site := fmt.Sprintf("month-race-%d", time.Now().UnixNano())
			defer func() {
				for _, table := range []string{"billing_generation_tasks", "billing_compact_daily_totals", "billing_jobs", "billing_money_snapshots"} {
					if _, e := db.Exec("DELETE FROM "+table+" WHERE instance_id=?", site); e != nil {
						t.Error(e)
					}
				}
			}()
			from := time.Date(2025, 9, 1, 0, 0, 0, 0, billing.BusinessLocation)
			money, _ := billing.NewMoneySnapshot(site, `{"QuotaPerUnit":500000}`, time.Now())
			newDay := func(offset int) billing.Job {
				j, _, e := billing.NewJob(site, from.AddDate(0, 0, offset), from.AddDate(0, 0, offset+1), "test")
				if e != nil {
					t.Fatal(e)
				}
				j.JobType, j.BillPeriod, j.UserID = "user_statement", "daily", 7
				j.UsageVersion, j.TotalSteps, j.MoneySnapshot = 3, 0, money
				j.RequestKey = j.ID
				tx, e := db.BeginTx(ctx, nil)
				if e != nil {
					t.Fatal(e)
				}
				defer tx.Rollback()
				if e = createBillingJobTx(ctx, tx, j, nil); e != nil {
					t.Fatal(e)
				}
				if _, e = tx.ExecContext(ctx, `INSERT INTO billing_statement_jobs(job_id,statement_type,subject_id,subject_name,created_at) VALUES(?,'user_statement',7,'',?)`, j.ID, j.CreatedAt); e != nil {
					t.Fatal(e)
				}
				if e = tx.Commit(); e != nil {
					t.Fatal(e)
				}
				return j
			}
			finishDay := func(j billing.Job, amount string, empty bool) {
				tx, e := db.BeginTx(ctx, nil)
				if e != nil {
					t.Fatal(e)
				}
				defer tx.Rollback()
				if empty {
					_, e = tx.ExecContext(ctx, `UPDATE billing_jobs SET status='no_data' WHERE id=?`, j.ID)
					if e == nil {
						e = supersedeBillingStatement(ctx, tx, j)
					}
				} else {
					_, e = tx.ExecContext(ctx, `INSERT INTO billing_compact_daily_totals(job_id,instance_id,bill_day,user_id,model_name,request_count,total_amount,updated_at) VALUES(?,?,?,?,?,1,?,UTC_TIMESTAMP(6))`, j.ID, site, j.From.Format("2006-01-02"), 7, "m", amount)
					if e == nil {
						e = s.finalizeBillingStatement(ctx, tx, j, time.Now().UTC())
					}
				}
				if e != nil {
					t.Fatal(e)
				}
				if e = tx.Commit(); e != nil {
					t.Fatal(e)
				}
			}
			first := newDay(0)
			finishDay(first, "1.25", false)
			offset := 1
			if tc.replace {
				offset = 0
			}
			next := newDay(offset)
			month := func() billing.Job {
				j, _, _ := billing.NewJob(site, from, from.AddDate(0, 1, 0), "test")
				j.JobType, j.BillPeriod, j.UserID = "user_statement", "monthly", 7
				j.UsageVersion, j.RequestKey = 3, "month:"+site
				return j
			}
			stale := month()
			enqueue, e := db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead})
			if e != nil {
				t.Fatal(e)
			}
			defer enqueue.Rollback()
			var before string
			if e = enqueue.QueryRowContext(ctx, `SELECT status FROM billing_jobs WHERE id=?`, first.ID).Scan(&before); e != nil || before != "complete" {
				t.Fatal("establish enqueue read view", before, e)
			}
			finishDay(next, "2.5", tc.empty)
			if e = s.createBillingMonthFromDays(ctx, enqueue, stale, ""); e != nil {
				t.Fatal(e)
			}
			if e = enqueue.Commit(); e != nil {
				t.Fatal(e)
			}
			var lineage string
			if e = db.QueryRowContext(ctx, `SELECT daily_job_id FROM billing_month_daily_sources WHERE month_job_id=?`, stale.ID).Scan(&lineage); e != nil || lineage != first.ID {
				t.Fatal("fixture did not freeze stale daily source", lineage, e)
			}
			publishQueuedMonthForTest(t, s, ctx, stale)
			old, e := s.BillingJob(ctx, stale.ID)
			if e != nil || old.Status != "superseded" {
				t.Fatal("stale month was published", old.Status, e)
			}
			var published int
			if e = db.QueryRowContext(ctx, `SELECT COUNT(*) FROM billing_statements WHERE job_id=?`, stale.ID).Scan(&published); e != nil || published != 0 {
				t.Fatal("stale result visible", published, e)
			}
			fresh := month()
			if e = s.CreateBillingStatementJob(ctx, fresh, nil, ""); e != nil {
				t.Fatal("stale request key prevented rebuilding", e)
			}
			publishQueuedMonthForTest(t, s, ctx, fresh)
			result, e := s.BillingJob(ctx, fresh.ID)
			if e != nil || result.Status != "complete" || result.MonthlyCoverage == nil || result.MonthlyCoverage.CoveredDays != tc.coveredDays || result.MonthlyCoverage.EmptyDays != tc.emptyDays {
				t.Fatal("fresh month coverage", result.Status, result.MonthlyCoverage, e)
			}
			var amount string
			if e = db.QueryRowContext(ctx, `SELECT CAST(SUM(total_amount) AS CHAR) FROM billing_compact_daily_totals WHERE job_id=?`, fresh.ID).Scan(&amount); e != nil || amount != tc.amount {
				t.Fatal("fresh month amount", amount, e)
			}
		})
	}
}
