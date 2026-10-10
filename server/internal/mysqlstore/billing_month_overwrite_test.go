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

func TestOverwriteMonthlyPublicationWaitsForAllDays(t *testing.T) {
	for _, kind := range []string{"user_statement", "upstream_statement"} {
		t.Run(kind, func(t *testing.T) {
			f := newMonthOverwriteFixture(t, kind, 30)
			f.startOverwrite()
			f.assertMonthWaiting()
			f.assertStatus(f.oldMonth, "complete")

			// Neither a verified empty day, one replacement day, nor a failed
			// third day constitutes a replacement of the complete old month.
			f.emptyDay(0)
			f.assertStatus(f.oldMonth, "complete")
			f.completeDay(1, "7")
			f.assertMonthWaiting()
			failed := f.newDay(2)
			if err := f.s.CreateBillingStatementJob(f.ctx, failed, nil, "test"); err != nil {
				t.Fatal(err)
			}
			if _, err := f.s.db.Exec(`UPDATE billing_jobs SET status='failed',error_message='source unavailable' WHERE id=?`, failed.ID); err != nil {
				t.Fatal(err)
			}
			f.assertMonthWaiting()
			f.assertStatus(f.oldMonth, "complete")
			if err := f.s.CancelBillingGeneration(f.ctx, []billing.AutomaticTarget{f.target}); err != nil {
				t.Fatal(err)
			}
			f.assertStatus(f.oldMonth, "complete")

			// A retry needs fresh evidence for all days; checks and complete
			// results from the cancelled attempt cannot satisfy it.
			f.startOverwrite()
			f.assertMonthWaiting()
			f.completeDay(0, "9")
			for day := 1; day < 29; day++ {
				f.emptyDay(day)
			}
			f.assertMonthWaiting()
			f.assertStatus(f.oldMonth, "complete")
			// Exercise a published no_data job as well as empty-day checks.
			last := f.newDay(29)
			if err := f.s.CreateBillingStatementJob(f.ctx, last, nil, "test"); err != nil {
				t.Fatal(err)
			}
			f.assertMonthWaiting()
			if _, err := f.s.db.Exec(`UPDATE billing_jobs SET status='running' WHERE id=?`, last.ID); err != nil {
				t.Fatal(err)
			}
			if _, ok, err := f.s.ForGeneration(f.site, last.ID).ClaimBillingPublish(f.ctx); err != nil || !ok {
				t.Fatal("empty day publication", ok, err)
			}
			f.assertStatus(last, "no_data")
			f.assertStatus(f.oldMonth, "complete")
			replacement := f.newMonth()
			if err := createStatementAndPublishTestMonth(t, f.s, f.ctx, replacement, nil, "test"); err != nil {
				t.Fatal(err)
			}
			f.assertStatus(f.oldMonth, "superseded")
			f.assertStatus(replacement, "complete")
			f.assertAmount(replacement, "9.000000000000")
			coverage, err := f.s.billingMonthlyCoverage(f.ctx, replacement)
			if err != nil || coverage.CoveredDays != 30 {
				t.Fatalf("replacement coverage: %+v %v", coverage, err)
			}

			// A wholly empty replacement can remove the old invoice only
			// after all 30 dates have been verified in this new generation.
			f.startOverwrite()
			for day := 0; day < 29; day++ {
				f.emptyDay(day)
			}
			f.assertMonthWaiting()
			f.assertStatus(replacement, "complete")
			f.emptyDay(29)
			if err := f.s.CreateBillingStatementJob(f.ctx, f.newMonth(), nil, "test"); !errors.Is(err, billing.ErrStatementNoData) {
				t.Fatal("verified empty replacement", err)
			}
			f.assertStatus(replacement, "superseded")
		})
	}
}

func TestOverwriteMonthCancellationBeforePublicationKeepsOldMonth(t *testing.T) {
	f := newMonthOverwriteFixture(t, "upstream_statement", 30)
	f.startOverwrite()
	f.completeDay(0, "5")
	for day := 1; day < 30; day++ {
		f.emptyDay(day)
	}
	replacement := f.newMonth()
	if err := f.s.CreateBillingStatementJob(f.ctx, replacement, nil, "test"); err != nil {
		t.Fatal(err)
	}
	f.assertStatus(f.oldMonth, "complete")
	if err := f.s.CancelBillingGeneration(f.ctx, []billing.AutomaticTarget{f.target}); err != nil {
		t.Fatal(err)
	}
	f.assertStatus(replacement, "failed")
	if err := f.s.CompleteBillingMonth(f.ctx, replacement); !errors.Is(err, billing.ErrGenerationCancelled) {
		t.Fatal("cancelled month published", err)
	}
	f.assertStatus(f.oldMonth, "complete")
}

func TestOverwriteMonthRechecksCoverageBeforePublication(t *testing.T) {
	f := newMonthOverwriteFixture(t, "upstream_statement", 30)
	f.startOverwrite()
	f.completeDay(0, "5")
	for day := 1; day < 30; day++ {
		f.emptyDay(day)
	}
	replacement := f.newMonth()
	if err := f.s.CreateBillingStatementJob(f.ctx, replacement, nil, "test"); err != nil {
		t.Fatal(err)
	}
	// Evidence invalidated after enqueue must be checked again at publish.
	if _, err := f.s.db.Exec(`DELETE FROM billing_day_checks WHERE instance_id=? AND bill_day='2025-09-30'`, f.site); err != nil {
		t.Fatal(err)
	}
	publishQueuedMonthForTest(t, f.s, f.ctx, replacement)
	f.assertStatus(replacement, "superseded")
	f.assertStatus(f.oldMonth, "complete")
	f.assertMonthWaiting()
}

func TestOverwriteMonthUsesFrozenWorkUntil(t *testing.T) {
	f := newMonthOverwriteFixture(t, "upstream_statement", 2)
	f.startOverwrite()
	// Simulate submission on the third day of this month. Later dates are
	// outside the submitted task even if the month is still open.
	if _, err := f.s.db.Exec(`UPDATE billing_generation_tasks SET work_until=? WHERE instance_id=? AND overwrite_existing=1`, f.from.AddDate(0, 0, 2).UTC(), f.site); err != nil {
		t.Fatal(err)
	}
	f.completeDay(0, "3")
	f.assertMonthWaiting()
	f.assertStatus(f.oldMonth, "complete")
	f.emptyDay(1)
	replacement := f.newMonth()
	if err := createStatementAndPublishTestMonth(t, f.s, f.ctx, replacement, nil, "test"); err != nil {
		t.Fatal(err)
	}
	f.assertStatus(f.oldMonth, "superseded")
	f.assertAmount(replacement, "3.000000000000")
	coverage, err := f.s.billingMonthlyCoverage(f.ctx, replacement)
	if err != nil || coverage.CoveredDays != 2 {
		t.Fatalf("open month coverage: %+v %v", coverage, err)
	}
	// After this overwrite finishes, a later automatic day still refreshes
	// the incremental month, despite the retained overwrite range record.
	f.completeDay(2, "4")
	f.assertStatus(replacement, "superseded")
	next := f.newMonth()
	if err := createStatementAndPublishTestMonth(t, f.s, f.ctx, next, nil, "test"); err != nil {
		t.Fatal(err)
	}
	f.assertAmount(next, "7.000000000000")
}

type monthOverwriteFixture struct {
	t        *testing.T
	ctx      context.Context
	s        Store
	site     string
	from, to time.Time
	target   billing.AutomaticTarget
	money    *billing.MoneySnapshot
	oldMonth billing.Job
}

func newMonthOverwriteFixture(t *testing.T, kind string, coveredDays int) *monthOverwriteFixture {
	t.Helper()
	dsn := os.Getenv("CT_MYSQL_TEST_DSN")
	if dsn == "" {
		t.Skip("requires local CT_MYSQL_TEST_DSN")
	}
	db, err := Open(dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	ctx := context.Background()
	if err = ApplyDir(ctx, db, "../../migrations"); err != nil {
		t.Fatal(err)
	}
	f := &monthOverwriteFixture{t: t, ctx: ctx, s: New(db), site: fmt.Sprintf("month-overwrite-%d", time.Now().UnixNano()), from: time.Date(2025, 9, 1, 0, 0, 0, 0, billing.BusinessLocation)}
	f.to = f.from.AddDate(0, 1, 0)
	f.target = billing.AutomaticTarget{InstanceID: f.site, Kind: kind, SubjectID: 7, From: f.from, To: f.to, Overwrite: true}
	t.Cleanup(func() {
		for _, table := range []string{"billing_generation_tasks", "billing_generation_ranges", "billing_automatic_targets", "billing_compact_daily_totals", "billing_day_checks", "billing_activity_days", "billing_jobs", "billing_money_snapshots", "billing_upstreams"} {
			if _, err := db.Exec("DELETE FROM "+table+" WHERE instance_id=?", f.site); err != nil {
				t.Error(err)
			}
		}
	})
	if kind == "upstream_statement" {
		result, err := db.Exec(`INSERT INTO billing_upstreams(instance_id,name,created_at,updated_at) VALUES(?,'test',UTC_TIMESTAMP(6),UTC_TIMESTAMP(6))`, f.site)
		if err != nil {
			t.Fatal(err)
		}
		f.target.SubjectID, _ = result.LastInsertId()
		if _, err = db.Exec(`INSERT INTO billing_upstream_channel_bindings(instance_id,upstream_id,channel_id,channel_name,created_at) VALUES(?,?,1,'test_channel',UTC_TIMESTAMP(6))`, f.site, f.target.SubjectID); err != nil {
			t.Fatal(err)
		}
	}
	f.money, err = billing.NewMoneySnapshot(f.site, `{"QuotaPerUnit":500000}`, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	f.completeDay(0, "1")
	for day := 1; day < coveredDays; day++ {
		f.emptyDay(day)
	}
	f.oldMonth = f.newMonth()
	if err = createStatementAndPublishTestMonth(t, f.s, ctx, f.oldMonth, nil, "test"); err != nil {
		t.Fatal(err)
	}
	return f
}
func (f *monthOverwriteFixture) newJob(from, to time.Time, period string) billing.Job {
	f.t.Helper()
	j, _, err := billing.NewJob(f.site, from, to, "test")
	if err != nil {
		f.t.Fatal(err)
	}
	j.JobType = f.target.Kind
	if j.JobType == "upstream_statement" {
		j.UpstreamID = f.target.SubjectID
	} else {
		j.UserID = f.target.SubjectID
	}
	j.UsageVersion = 3
	j.BillPeriod = period
	j.DataSource = "source"
	j.RequestKey = j.ID
	j.MoneySnapshot = f.money
	j.TotalSteps = 0
	return j
}
func (f *monthOverwriteFixture) newDay(day int) billing.Job {
	return f.newJob(f.from.AddDate(0, 0, day), f.from.AddDate(0, 0, day+1), "daily")
}
func (f *monthOverwriteFixture) newMonth() billing.Job { return f.newJob(f.from, f.to, "monthly") }
func (f *monthOverwriteFixture) startOverwrite() {
	f.t.Helper()
	if err := f.s.PutBillingAutomaticTargets(f.ctx, []billing.AutomaticTarget{f.target}); err != nil {
		f.t.Fatal(err)
	}
}
func (f *monthOverwriteFixture) emptyDay(day int) {
	f.t.Helper()
	target := f.target
	target.From = f.from.AddDate(0, 0, day)
	target.To = target.From.AddDate(0, 0, 1)
	if err := f.s.RecordBillingDayActivity(f.ctx, target, false); err != nil {
		f.t.Fatal(err)
	}
}
func (f *monthOverwriteFixture) completeDay(day int, amount string) {
	f.t.Helper()
	j := f.newDay(day)
	if err := f.s.CreateBillingStatementJob(f.ctx, j, nil, "test"); err != nil {
		f.t.Fatal(err)
	}
	if _, err := f.s.db.Exec(`INSERT INTO billing_compact_daily_totals(job_id,instance_id,bill_day,user_id,username,model_name,request_count,total_amount,updated_at) VALUES(?,?,?,?, 'test','model',1,?,UTC_TIMESTAMP(6))`, j.ID, f.site, j.From.In(billing.BusinessLocation).Format("2006-01-02"), 7, amount); err != nil {
		f.t.Fatal(err)
	}
	if err := f.s.FinalizeBillingJob(f.ctx, j); err != nil {
		f.t.Fatal(err)
	}
}
func (f *monthOverwriteFixture) assertMonthWaiting() {
	f.t.Helper()
	if err := f.s.CreateBillingStatementJob(f.ctx, f.newMonth(), nil, "test"); !errors.Is(err, billing.ErrDailyBillsIncomplete) {
		f.t.Fatalf("incomplete overwrite enqueued a month: %v", err)
	}
}
func (f *monthOverwriteFixture) assertStatus(j billing.Job, want string) {
	f.t.Helper()
	got, err := f.s.BillingJob(f.ctx, j.ID)
	if err != nil || got.Status != want {
		f.t.Fatalf("status %s want %s: %v", got.Status, want, err)
	}
}
func (f *monthOverwriteFixture) assertAmount(j billing.Job, want string) {
	f.t.Helper()
	var amount string
	err := f.s.db.QueryRow(`SELECT CAST(SUM(total_amount) AS CHAR) FROM billing_compact_daily_totals WHERE job_id=?`, j.ID).Scan(&amount)
	if err != nil || amount != want {
		f.t.Fatalf("amount %s want %s: %v", amount, want, err)
	}
}
