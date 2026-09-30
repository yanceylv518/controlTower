package mysqlstore

import (
	"context"
	"controltower/server/internal/billing"
	"fmt"
	"os"
	"testing"
	"time"
)

func TestBillingOriginalAggregation(t *testing.T) {
	dsn := os.Getenv("CT_MYSQL_TEST_DSN")
	if dsn == "" {
		t.Skip("requires local CT_MYSQL_TEST_DSN")
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
	site := fmt.Sprintf("original-%d", time.Now().UnixNano())
	s := New(db)
	day := time.Date(2025, 1, 1, 0, 0, 0, 0, billing.BusinessLocation)
	job, _, e := billing.NewJob(site, day, day.AddDate(0, 0, 1), "test")
	if e != nil {
		t.Fatal(e)
	}
	job.JobType = "user_statement"
	job.UserID = 7
	job.UsageVersion = 3
	job.BillPeriod = "daily"
	job.Status = "complete"
	job.RequestKey = job.ID
	job.MoneySnapshot, _ = billing.NewMoneySnapshot(site, `{"QuotaPerUnit":500000,"USDExchangeRate":7.2,"DisplayInCurrencyEnabled":true}`, time.Now())
	defer func() {
		db.Exec("DELETE FROM billing_compact_daily_totals WHERE instance_id=?", site)
		db.Exec("DELETE FROM billing_generation_tasks WHERE instance_id=?", site)
		db.Exec("DELETE FROM billing_jobs WHERE instance_id=?", site)
		db.Exec("DELETE FROM billing_money_snapshots WHERE instance_id=?", site)
	}()
	if e = s.CreateBillingStatementJob(ctx, job, nil, ""); e != nil {
		t.Fatal(e)
	}
	write := func(before, discount string) {
		t.Helper()
		e := s.AppendBillingHour(ctx, job, billing.JobStep{}, nil, nil, nil, []billing.RequestDetail{{BillDay: day, UserID: 7, ModelName: "m", MultimediaUsage: billing.MultimediaUsage{ImageInputTokens: 513, ImageOutputTokens: 7, AudioInputTokens: 31, AudioOutputTokens: 11}, Charge: billing.LogCharge{Total: "4", Settlement: &billing.Settlement{BeforeAmount: before, Discount: discount}}}}, nil, nil, billing.LogCursor{}, 1)
		if e != nil {
			t.Fatal(e)
		}
	}
	write("5", "0.8")
	write("5", "0.800000")
	check := func(before, discount string) {
		t.Helper()
		rows, e := s.QueryBillingStatementAggregates(ctx, job.ID)
		if e != nil || len(rows) != 1 || rows[0].BeforeAmount != before || rows[0].SettlementDiscount != discount {
			t.Fatalf("rows=%+v error=%v", rows, e)
		}
	}
	check("10.000000000000", "0.800000")
	write("4", "1")
	check("14.000000000000", "mixed")
	write("", "0.8")
	check("", "mixed")
	workspace, e := s.BillingWorkspace(ctx, site, "user_statement", 7, day, day.AddDate(0, 0, 1))
	if e != nil || len(workspace) != 1 || workspace[0].ImageInputTokens != 2052 || workspace[0].ImageOutputTokens != 28 || workspace[0].AudioInputTokens != 124 || workspace[0].AudioOutputTokens != 44 {
		t.Fatalf("workspace media: %+v %v", workspace, e)
	}
	// Historical bills did not record discount metadata; use full-price fallback.
	if _, e = db.Exec("UPDATE billing_compact_daily_totals SET before_known_count=0,settlement_discount='' WHERE job_id=?", job.ID); e != nil {
		t.Fatal(e)
	}
	check("16.000000000000", "1.000000")
	workspace, e = s.BillingWorkspace(ctx, site, "user_statement", 7, day, day.AddDate(0, 0, 1))
	if e != nil || len(workspace) != 1 || workspace[0].BeforeAmount != "16.000000000000" || workspace[0].Discount != "1.000000" {
		t.Fatalf("workspace fallback: %+v %v", workspace, e)
	}
	tokens, e := s.QueryBillingTokenRows(ctx, job.ID, 7, -1, day, day.AddDate(0, 0, 1))
	if e != nil || len(tokens) != 1 || tokens[0].BeforeAmount != "16.000000000000" || tokens[0].SettlementDiscount != "1.000000" {
		t.Fatalf("token fallback: %+v %v", tokens, e)
	}

}
