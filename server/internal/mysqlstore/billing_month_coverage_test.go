package mysqlstore

import (
	"context"
	"controltower/server/internal/billing"
	"fmt"
	"os"
	"reflect"
	"testing"
	"time"
)

func TestBillingMonthlyCoverageSnapshotAndHistoricalFallback(t *testing.T) {
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
	site := fmt.Sprintf("month-coverage-%d", time.Now().UnixNano())
	defer func() {
		for _, table := range []string{"billing_generation_tasks", "billing_generation_ranges", "billing_day_checks", "billing_compact_daily_totals", "billing_jobs", "billing_money_snapshots"} {
			if _, e := db.Exec("DELETE FROM "+table+" WHERE instance_id=?", site); e != nil {
				t.Error(e)
			}
		}
	}()
	from := time.Date(2025, 9, 1, 0, 0, 0, 0, billing.BusinessLocation)
	money, _ := billing.NewMoneySnapshot(site, `{"QuotaPerUnit":"500000"}`, time.Now())
	makeJob := func(day time.Time, period, status string) billing.Job {
		to := day.AddDate(0, 0, 1)
		if period == "monthly" {
			to = day.AddDate(0, 1, 0)
		}
		j, _, _ := billing.NewJob(site, day, to, "test")
		j.JobType, j.UserID, j.UsageVersion, j.BillPeriod, j.Status, j.RequestKey, j.MoneySnapshot = "user_statement", 7, 3, period, status, j.ID, money
		if err := createStatementAndPublishTestMonth(t, s, ctx, j, nil, "test"); err != nil {
			t.Fatal(err)
		}
		return j
	}
	for _, offset := range []int{1, 3} {
		day := from.AddDate(0, 0, offset)
		j := makeJob(day, "daily", "complete")
		if _, err = db.Exec(`INSERT INTO billing_compact_daily_totals(job_id,instance_id,bill_day,user_id,model_name,request_count,total_amount,updated_at) VALUES(?,?,?,?,?,1,0.123456789012,UTC_TIMESTAMP(6))`, j.ID, site, day.Format("2006-01-02"), 7, "m"); err != nil {
			t.Fatal(err)
		}
	}
	makeJob(from.AddDate(0, 0, 2), "daily", "no_data")
	// An actual completed empty check covers Sep 1. An early check on Sep 5
	// does not prove that the rest of that day had no consumption.
	for _, offset := range []int{0, 4} {
		day := from.AddDate(0, 0, offset)
		checked := day.AddDate(0, 0, 1)
		if offset == 4 {
			checked = day.Add(time.Hour)
		}
		if _, err = db.Exec(`INSERT INTO billing_day_checks VALUES(?,?,?,?,0,?)`, site, "user_statement", 7, day.Format("2006-01-02"), checked.UTC()); err != nil {
			t.Fatal(err)
		}
	}
	month := makeJob(from, "monthly", "pending")
	loaded, err := s.BillingJob(ctx, month.ID)
	if err != nil {
		t.Fatal(err)
	}
	c := loaded.MonthlyCoverage
	if c == nil || c.Complete || c.CoveredDays != 4 || c.EmptyDays != 2 || c.RangeLabel() != "2025-09-01 至 2025-09-04" || !loaded.From.Equal(from) || !loaded.To.Equal(from.AddDate(0, 1, 0)) {
		t.Fatal(loaded)
	}
	for day := from.AddDate(0, 0, 4); day.Before(month.To); day = day.AddDate(0, 0, 1) {
		if _, err = db.Exec(`INSERT INTO billing_day_checks VALUES(?,?,?,?,0,UTC_TIMESTAMP(6)) ON DUPLICATE KEY UPDATE checked_at=UTC_TIMESTAMP(6)`, site, "user_statement", 7, day.Format("2006-01-02")); err != nil {
			t.Fatal(err)
		}
	}
	unchanged, err := s.BillingJob(ctx, month.ID)
	if err != nil || !reflect.DeepEqual(c, unchanged.MonthlyCoverage) {
		t.Fatal("later checks mutated monthly coverage", err)
	}
	if _, err = db.Exec(`UPDATE billing_jobs SET status='superseded' WHERE id=?`, month.ID); err != nil {
		t.Fatal(err)
	}
	full := makeJob(from, "monthly", "pending")
	fullLoaded, err := s.BillingJob(ctx, full.ID)
	if err != nil || !fullLoaded.MonthlyCoverage.Complete || fullLoaded.MonthlyCoverage.EmptyDays != 28 {
		t.Fatal(fullLoaded, err)
	}
	// A new overwrite invalidates old empty checks. A positive consumption
	// check and a check from after this snapshot cannot fill other gaps either.
	if _, err = db.Exec(`INSERT INTO billing_generation_ranges(instance_id,kind,subject_id,range_from,range_to,created_at,overwrite_existing,generation_started_at) VALUES(?,?,?,'2025-09-05','2025-09-06',UTC_TIMESTAMP(6),1,DATE_ADD(UTC_TIMESTAMP(6),INTERVAL 1 MINUTE))`, site, "user_statement", 7); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(`UPDATE billing_day_checks SET has_consumption=1 WHERE instance_id=? AND bill_day='2025-09-06'`, site); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(`UPDATE billing_day_checks SET checked_at=DATE_ADD(UTC_TIMESTAMP(6),INTERVAL 1 HOUR) WHERE instance_id=? AND bill_day='2025-09-07'`, site); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(`UPDATE billing_jobs SET status='superseded' WHERE id=?`, full.ID); err != nil {
		t.Fatal(err)
	}
	checked := makeJob(from, "monthly", "pending")
	checkedLoaded, err := s.BillingJob(ctx, checked.ID)
	if err != nil || checkedLoaded.MonthlyCoverage.CoveredDays != 27 || checkedLoaded.MonthlyCoverage.Complete || !reflect.DeepEqual(checkedLoaded.MonthlyCoverage.Missing, []billing.CoverageRange{{From: "2025-09-05", To: "2025-09-07"}}) {
		t.Fatal(checkedLoaded, err)
	}
	// Source daily jobs and mutable checks may be retained for less time than
	// the month. Coverage and financial totals must survive their removal.
	if _, err = db.Exec(`DELETE FROM billing_jobs WHERE instance_id=? AND bill_period='daily'`, site); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(`DELETE FROM billing_day_checks WHERE instance_id=?`, site); err != nil {
		t.Fatal(err)
	}
	retained, err := s.BillingJob(ctx, full.ID)
	if err != nil || !reflect.DeepEqual(retained.MonthlyCoverage, fullLoaded.MonthlyCoverage) {
		t.Fatal(retained, err)
	}
	for _, id := range []string{month.ID, full.ID} {
		var amount string
		if err = db.QueryRow(`SELECT CAST(SUM(total_amount) AS CHAR) FROM billing_compact_daily_totals WHERE job_id=?`, id).Scan(&amount); err != nil || amount != "0.246913578024" {
			t.Fatal(amount, err)
		}
	}
	// A pre-upgrade month has no coverage snapshot. Its own compact rows,
	// including separated dates, are the only trustworthy historical evidence.
	if _, err = db.Exec(`DELETE FROM billing_month_coverage WHERE job_id=?`, month.ID); err != nil {
		t.Fatal(err)
	}
	legacy, err := s.BillingJob(ctx, month.ID)
	if err != nil || legacy.MonthlyCoverage.Complete || legacy.MonthlyCoverage.CoveredDays != 2 || legacy.MonthlyCoverage.EmptyDays != 0 || len(legacy.MonthlyCoverage.Ranges) != 2 {
		t.Fatal(legacy, err)
	}
}
