package mysqlstore

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"controltower/server/internal/billing"
)

func TestBillingSourceAndMultimediaMySQL(t *testing.T) {
	dsn := os.Getenv("CT_BILLING_TEST_DSN")
	if dsn == "" {
		t.Skip("set CT_BILLING_TEST_DSN for isolated billing integration")
	}
	if !strings.Contains(dsn, "127.0.0.1") {
		t.Fatal("billing integration requires a local disposable database")
	}
	db, err := Open(dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	ctx := context.Background()
	for i := 0; i < 2; i++ {
		if err = ApplyDir(ctx, db, "../../migrations"); err != nil {
			t.Fatal(err)
		}
	}
	store := New(db)
	day := time.Date(2026, 9, 14, 0, 0, 0, 0, billing.BusinessLocation)
	for _, mode := range []string{billing.PricingSourceNewAPI, billing.PricingSourceRecalculate} {
		job, steps, e := billing.NewJob("billing-mode-test", day, day.AddDate(0, 0, 1), "test")
		if e != nil {
			t.Fatal(e)
		}
		job.JobType = "user_statement"
		job.UserID = 7
		job.PricingSource = mode
		job.UsageVersion = 1
		job.RequestKey = fmt.Sprintf("mode-test-%s-%s", job.ID, mode)
		job.MoneySnapshot, e = billing.NewMoneySnapshot(job.InstanceID, `{"QuotaPerUnit":"500000"}`, time.Now())
		if e != nil { t.Fatal(e) }
		if e = store.CreateBillingStatementJob(ctx, job, steps, ""); e != nil {
			t.Fatal(e)
		}
		t.Cleanup(func() {
			_, _ = db.Exec("DELETE FROM billing_compact_daily_totals WHERE job_id=?", job.ID)
			_, _ = db.Exec("DELETE FROM billing_jobs WHERE id=?", job.ID)
		})
		if e = store.CreateBillingStatementJob(ctx, job, steps, ""); e != billing.ErrStatementDuplicate {
			t.Fatalf("duplicate=%v", e)
		}
		loaded, e := store.BillingJob(ctx, job.ID)
		if e != nil || loaded.PricingSource != mode || loaded.UsageVersion != 1 {
			t.Fatalf("loaded=%+v err=%v", loaded, e)
		}
		listed, e := store.ListBillingJobs(ctx, job.InstanceID, "", 200)
		if e != nil {
			t.Fatal(e)
		}
		found := false
		for _, v := range listed {
			if v.ID == job.ID {
				found = v.PricingSource == mode && v.UsageVersion == 1
			}
		}
		if !found {
			t.Fatal("list lost pricing source")
		}
		claimed, step, ok, e := store.ClaimBillingStep(ctx)
		if e != nil || !ok || claimed.ID != job.ID || claimed.PricingSource != mode || claimed.UsageVersion != 1 {
			t.Fatalf("claim=%+v err=%v", claimed, e)
		}
		detail := billing.RequestDetail{JobID: job.ID, InstanceID: job.InstanceID, UserID: 7, TokenID: 8, ChannelID: 9, ModelName: "m", BillDay: day, PromptTokens: 90, CompletionTokens: 60, MultimediaUsage: billing.MultimediaUsage{ImageInputTokens: 70, ImageOutputTokens: 20, AudioInputTokens: 30, AudioOutputTokens: 10}, Charge: billing.LogCharge{Total: "0.012345000000"}, CalculatedQuota: 12345}
		for page := 1; page <= 2; page++ {
			if e = store.AppendBillingHour(ctx, claimed, step, nil, nil, nil, []billing.RequestDetail{detail}, nil, nil, billing.LogCursor{ID: int64(page), CreatedUnix: day.Unix()}, 1); e != nil {
				t.Fatal(e)
			}
		}
		rows, e := store.QueryBillingStatementAggregates(ctx, job.ID)
		if e != nil || len(rows) != 1 {
			t.Fatalf("rows=%+v err=%v", rows, e)
		}
		v := rows[0]
		if v.PromptTokens != 180 || v.CompletionTokens != 120 || v.ImageInputTokens != 140 || v.ImageOutputTokens != 40 || v.AudioInputTokens != 60 || v.AudioOutputTokens != 20 || v.Quota != 24690 || v.Amount != "0.024690000000" {
			t.Fatalf("aggregate=%+v", v)
		}
		tokens, e := store.QueryBillingTokenRows(ctx, job.ID, 7, -1, day, day.AddDate(0, 0, 1))
		if e != nil || len(tokens) != 1 || tokens[0].MultimediaUsage != v.MultimediaUsage || tokens[0].Amount != v.Amount {
			t.Fatalf("tokens=%+v err=%v", tokens, e)
		}
		if e = store.CompleteBillingStep(ctx, claimed, step, 2, 0); e != nil {
			t.Fatal(e)
		}
		publish, ok, e := store.ClaimBillingPublish(ctx)
		if e != nil || !ok || publish.ID != job.ID || publish.PricingSource != mode || publish.UsageVersion != 1 {
			t.Fatalf("publish=%+v err=%v", publish, e)
		}
		if _, e = db.Exec("UPDATE billing_jobs SET status='complete' WHERE id=?", job.ID); e != nil {
			t.Fatal(e)
		}
	}
	// Old insert paths omit both fields, including rows created before upgrade.
	oldID := fmt.Sprintf("legacy-%d", time.Now().UnixNano())
	_, err = db.Exec("INSERT INTO billing_jobs(id,instance_id,range_from,range_to,created_at,updated_at) VALUES(?,?,?,?,?,?)", oldID, "billing-mode-test", day, day.AddDate(0, 0, 1), day, day)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Exec("DELETE FROM billing_jobs WHERE id=?", oldID)
	old, err := store.BillingJob(ctx, oldID)
	if err != nil || old.PricingSource != billing.PricingSourceRecalculate || old.UsageVersion != 0 {
		t.Fatalf("legacy=%+v err=%v", old, err)
	}
}
