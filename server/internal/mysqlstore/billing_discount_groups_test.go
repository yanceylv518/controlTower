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

func TestBillingDiscountGroupsSurvivePagesAndMonthlyCopy(t *testing.T) {
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
	site := fmt.Sprintf("discount-groups-%d", time.Now().UnixNano())
	defer func() {
		for _, table := range []string{"billing_generation_tasks", "billing_compact_daily_totals", "billing_jobs", "billing_money_snapshots"} {
			if _, err := db.Exec("DELETE FROM "+table+" WHERE instance_id=?", site); err != nil {
				t.Error(err)
			}
		}
	}()
	s := New(db)
	day := time.Date(2025, 1, 1, 0, 0, 0, 0, billing.BusinessLocation)
	job, _, _ := billing.NewJob(site, day, day.AddDate(0, 0, 1), "test")
	job.JobType, job.UserID, job.UsageVersion, job.BillPeriod, job.Status, job.RequestKey = "user_statement", 7, 3, "daily", "complete", job.ID
	job.MoneySnapshot, _ = billing.NewMoneySnapshot(site, `{"QuotaPerUnit":"500000"}`, time.Now())
	if err = s.CreateBillingStatementJob(ctx, job, nil, ""); err != nil {
		t.Fatal(err)
	}
	row := func(rate, before, amount string) billing.RequestDetail {
		return billing.RequestDetail{BillDay: day, UserID: 7, TokenID: 8, TokenName: "prod", ChannelID: 9, ModelName: "m", PromptTokens: 10, CompletionTokens: 20, CacheReadTokens: 30, CacheWriteTokens: 40, CacheWrite5mTokens: 50, CacheWrite1hTokens: 60, CalculatedQuota: 70,
			MultimediaUsage: billing.MultimediaUsage{ImageInputTokens: 80, ImageOutputTokens: 90, AudioInputTokens: 100, AudioOutputTokens: 110}, EmptyOutput: true,
			Charge: billing.LogCharge{Total: amount, UnitPrices: billing.UnitPrices{"输入|1": true}, Settlement: &billing.Settlement{Discount: rate, BeforeAmount: before}}}
	}
	for _, page := range [][]billing.RequestDetail{
		{row("1", "1", "1"), row("0.46", "2", "0.92"), row("0", "3", "0")},
		{row("0.460000", "2", "0.92"), row("1.000000", "1", "1"), row("0.5", "", "0.123456789012")},
	} {
		if err = s.AppendBillingHour(ctx, job, billing.JobStep{}, nil, nil, nil, page, nil, nil, billing.LogCursor{}, int64(len(page))); err != nil {
			t.Fatal(err)
		}
	}
	check := func(id string) []billing.StatementAggregateRow {
		t.Helper()
		rows, err := s.QueryBillingStatementAggregates(ctx, id)
		if err != nil || len(rows) != 4 {
			t.Fatalf("rows=%+v err=%v", rows, err)
		}
		byRate := map[string]billing.StatementAggregateRow{}
		for _, v := range rows {
			byRate[v.SettlementDiscount] = v
		}
		for _, rate := range []string{"1.000000", "0.460000"} {
			v := byRate[rate]
			if v.RequestCount != 2 || v.PromptTokens != 20 || v.CompletionTokens != 40 || v.CacheTokens != 60 || v.CacheWriteTokens != 80 || v.CacheWrite5mTokens != 100 || v.CacheWrite1hTokens != 120 || v.Quota != 140 || v.AudioOutputTokens != 220 || v.ImageInputTokens != 160 || v.ImageOutputTokens != 180 || v.AudioInputTokens != 200 {
				t.Fatal(v)
			}
		}
		if byRate["1.000000"].Amount != "2.000000000000" || byRate["1.000000"].BeforeAmount != "2.000000000000" || byRate["0.460000"].Amount != "1.840000000000" || byRate["0.460000"].BeforeAmount != "4.000000000000" || byRate["0.000000"].Amount != "0.000000000000" || byRate["0.500000"].BeforeAmount != "" {
			t.Fatal(byRate)
		}
		var count, empty int64
		var amount, emptyAmount string
		if err = db.QueryRow(`SELECT SUM(request_count),CAST(SUM(total_amount) AS CHAR),SUM(empty_output_count),CAST(SUM(empty_output_amount) AS CHAR) FROM billing_compact_daily_totals WHERE job_id=?`, id).Scan(&count, &amount, &empty, &emptyAmount); err != nil || count != 6 || empty != 6 || amount != "3.963456789012" || emptyAmount != amount {
			t.Fatal(count, amount, empty, emptyAmount, err)
		}
		tokens, err := s.QueryBillingTokenRows(ctx, id, 7, 8, day, day.AddDate(0, 1, 0))
		if err != nil || len(tokens) != 4 {
			t.Fatal(tokens, err)
		}
		for _, token := range tokens {
			v := byRate[token.SettlementDiscount]
			if token.RequestCount != v.RequestCount || token.Amount != v.Amount || token.BeforeAmount != v.BeforeAmount || token.MultimediaUsage != v.MultimediaUsage {
				t.Fatal(token, v)
			}
		}
		return rows
	}
	daily := check(job.ID)
	month, _, _ := billing.NewJob(site, day, day.AddDate(0, 1, 0), "test")
	month.JobType, month.UserID, month.UsageVersion, month.BillPeriod, month.RequestKey = "user_statement", 7, 3, "monthly", month.ID
	if err = s.CreateBillingStatementJob(ctx, month, nil, ""); err != nil {
		t.Fatal(err)
	}
	if monthly := check(month.ID); !reflect.DeepEqual(monthly, daily) {
		t.Fatal("monthly changed the saved discount groups", daily, monthly)
	}
	// Frozen month snapshots stay unchanged when a later daily row is added.
	if err = s.AppendBillingHour(ctx, job, billing.JobStep{}, nil, nil, nil, []billing.RequestDetail{row("1", "1", "1")}, nil, nil, billing.LogCursor{}, 1); err != nil {
		t.Fatal(err)
	}
	check(month.ID)
}
