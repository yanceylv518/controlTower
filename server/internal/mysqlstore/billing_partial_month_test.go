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

func TestPartialMonthRefreshUsesCompletedDailySnapshots(t *testing.T) {
	dsn := os.Getenv("CT_MYSQL_TEST_DSN")
	if dsn == "" {
		t.Skip("requires local CT_MYSQL_TEST_DSN")
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
	site := fmt.Sprintf("partial-month-%d", time.Now().UnixNano())
	defer func() {
		for _, table := range []string{"billing_generation_tasks", "billing_generation_ranges", "billing_compact_daily_totals", "billing_jobs", "billing_money_snapshots"} {
			if _, e := db.Exec("DELETE FROM "+table+" WHERE instance_id=?", site); e != nil {
				t.Error(e)
			}
		}
	}()
	// Both fixture days must be closed, including when the test runs on the
	// first or second day of a month. The explicit boundary simulates a partial month.
	from := time.Date(2025, 9, 1, 0, 0, 0, 0, billing.BusinessLocation)
	to := from.AddDate(0, 1, 0)
	money, _ := billing.NewMoneySnapshot(site, `{"QuotaPerUnit":500000}`, time.Now())
	makeDay := func(day int) billing.Job {
		j, _, e := billing.NewJob(site, from.AddDate(0, 0, day), from.AddDate(0, 0, day+1), "test")
		if e != nil {
			t.Fatal(e)
		}
		j.JobType = "user_statement"
		j.UserID = 7
		j.UsageVersion = 3
		j.BillPeriod = "daily"
		j.DataSource = "source"
		j.TotalSteps = 0
		j.RequestKey = j.ID
		j.MoneySnapshot = money
		if e = createStatementAndPublishTestMonth(t, s, ctx, j, nil, ""); e != nil {
			t.Fatal(e)
		}
		return j
	}
	finish := func(j billing.Job, amount string) {
		if _, e := db.Exec(`INSERT INTO billing_compact_daily_totals(job_id,instance_id,bill_day,user_id,username,model_name,request_count,total_amount,updated_at) VALUES(?,?,?,?,?,?,1,?,UTC_TIMESTAMP(6))`, j.ID, site, j.From.Format("2006-01-02"), 7, "test", "m", amount); e != nil {
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
	d1, d2 := makeDay(0), makeDay(1) // both queued before the first month snapshot
	finish(d1, "1.25")
	target := billing.AutomaticTarget{InstanceID: site, Kind: "user_statement", SubjectID: 7, From: from, To: to}
	months, e := s.MissingBillingMonths(ctx, target, from.AddDate(0, 0, 2))
	if e != nil || len(months) != 1 {
		t.Fatal("open month omitted", months, e)
	}
	makeMonth := func() billing.Job {
		j, _, _ := billing.NewJob(site, from, to, "test")
		j.JobType = "user_statement"
		j.UserID = 7
		j.UsageVersion = 3
		j.BillPeriod = "monthly"
		j.RequestKey = "month:" + site
		return j
	}
	m1 := makeMonth()
	if e = createStatementAndPublishTestMonth(t, s, ctx, m1, nil, ""); e != nil {
		t.Fatal(e)
	}
	check := func(id, want string) {
		var amount string
		var n int
		if e = db.QueryRow(`SELECT CAST(SUM(total_amount) AS CHAR),COUNT(*) FROM billing_compact_daily_totals WHERE job_id=?`, id).Scan(&amount, &n); e != nil || amount != want {
			t.Fatal("wrong monthly amount", amount, n, e)
		}
	}
	check(m1.ID, "1.250000000000")
	finish(d2, "2.5")
	old, e := s.BillingJob(ctx, m1.ID)
	if e != nil || old.Status != "superseded" {
		t.Fatal("stale month retained", old.Status, e)
	}
	m2 := makeMonth()
	if e = createStatementAndPublishTestMonth(t, s, ctx, m2, nil, ""); e != nil {
		t.Fatal(e)
	}
	check(m2.ID, "3.750000000000")
	check(m1.ID, "1.250000000000")
	if e = createStatementAndPublishTestMonth(t, s, ctx, makeMonth(), nil, ""); !errors.Is(e, billing.ErrStatementDuplicate) {
		t.Fatal("duplicate refresh", e)
	}
	months, e = s.MissingBillingMonths(ctx, target, from.AddDate(0, 0, 2))
	if e != nil || len(months) != 0 {
		t.Fatal("unchanged month not reused", months, e)
	}
}
