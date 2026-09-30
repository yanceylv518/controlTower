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

func TestBillingWorkspaceModelsMatchSavedDailyTotals(t *testing.T) {
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
	site := fmt.Sprintf("workspace-models-%d", time.Now().UnixNano())
	s := New(db)
	day := time.Date(2025, 1, 1, 0, 0, 0, 0, billing.BusinessLocation)
	job, _, err := billing.NewJob(site, day, day.AddDate(0, 0, 1), "test")
	if err != nil {
		t.Fatal(err)
	}
	job.JobType, job.UserID, job.UsageVersion, job.BillPeriod, job.Status, job.RequestKey = "user_statement", 7, 3, "daily", "complete", job.ID
	job.MoneySnapshot, err = billing.NewMoneySnapshot(site, `{"QuotaPerUnit":"500000","USDExchangeRate":"7.2","general_setting.quota_display_type":"CNY"}`, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		db.Exec("DELETE FROM billing_compact_daily_totals WHERE instance_id=?", site)
		db.Exec("DELETE FROM billing_jobs WHERE instance_id=?", site)
		db.Exec("DELETE FROM billing_money_snapshots WHERE instance_id=?", site)
	}()
	if err = s.CreateBillingStatementJob(ctx, job, nil, ""); err != nil {
		t.Fatal(err)
	}
	appendRow := func(model, amount, before, discount string, channel int64) {
		t.Helper()
		row := billing.RequestDetail{BillDay: day, UserID: 7, ChannelID: channel, TokenID: channel, ModelName: model, PromptTokens: 17, CompletionTokens: 8, CacheReadTokens: 6, CacheWriteTokens: 3, EmptyOutput: true, MultimediaUsage: billing.MultimediaUsage{ImageInputTokens: 4, ImageOutputTokens: 5, AudioInputTokens: 6, AudioOutputTokens: 7}, Charge: billing.LogCharge{Total: amount, Settlement: &billing.Settlement{BeforeAmount: before, Discount: discount}}}
		if err := s.AppendBillingHour(ctx, job, billing.JobStep{}, nil, nil, nil, []billing.RequestDetail{row}, nil, nil, billing.LogCursor{}, 1); err != nil {
			t.Fatal(err)
		}
	}
	appendRow("model-A", "0.012280", "0.026696", "0.46", 1)
	appendRow("model-A", "0.01", "0.01", "1", 2)
	appendRow("model-a", "0.02", "0.02", "1", 3)
	appendRow("model-a ", "0.03", "0.06", "0.5", 4)
	appendRow("", "0", "0", "1", 5)
	check := func(unknown bool) {
		t.Helper()
		bills, err := s.BillingWorkspace(ctx, site, "user_statement", 7, day, day.AddDate(0, 0, 1))
		if err != nil || len(bills) != 1 || len(bills[0].Models) != 4 {
			t.Fatalf("bills=%+v err=%v", bills, err)
		}
		var expected billing.WorkspaceTotals
		err = db.QueryRowContext(ctx, `SELECT `+workspaceTotalsSQL+` FROM billing_compact_daily_totals WHERE job_id=?`, job.ID).Scan(&expected.Requests, &expected.Input, &expected.Output, &expected.CacheRead, &expected.CacheWrite, &expected.Amount, &expected.BeforeAmount, &expected.Discount, &expected.EmptyCount, &expected.EmptyAmount, &expected.ImageInputTokens, &expected.ImageOutputTokens, &expected.AudioInputTokens, &expected.AudioOutputTokens)
		if err != nil || !reflect.DeepEqual(bills[0].WorkspaceTotals, expected) {
			t.Fatalf("model sum=%+v existing total=%+v err=%v", bills[0].WorkspaceTotals, expected, err)
		}
		models := map[string]billing.WorkspaceModel{}
		for _, model := range bills[0].Models {
			models[model.Model] = model
		}
		if len(models) != 4 || models["model-a"].Amount != "0.020000000000" || models["model-a "].Amount != "0.030000000000" || models[""].Amount != "0.000000000000" {
			t.Fatal(models)
		}
		if unknown {
			if models["model-A"].BeforeAmount != "" || expected.BeforeAmount != "" {
				t.Fatal("unknown original was replaced", models, expected)
			}
		} else if models["model-A"].Requests != 2 || models["model-A"].BeforeAmount != "0.036696000000" || models["model-A"].Discount != "mixed" {
			t.Fatal("same model was not combined across channels/tokens", models)
		}
	}
	check(false)
	appendRow("model-A", "0.01", "", "0.46", 6)
	check(true)
	// The same subject in another site must not see this bill or its models.
	if other, err := s.BillingWorkspace(ctx, site+"-other", "user_statement", 7, day, day.AddDate(0, 0, 1)); err != nil || len(other) != 0 {
		t.Fatal(other, err)
	}
}
