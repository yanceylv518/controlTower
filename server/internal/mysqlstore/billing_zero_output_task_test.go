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

func TestZeroOutputTaskPersistenceAndSettlementIsolation(t *testing.T) {
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
	dir := os.Getenv("CT_TEST_MIGRATIONS")
	if dir == "" {
		dir = "../../migrations"
	}
	if err = ApplyDir(ctx, db, dir); err != nil {
		t.Fatal(err)
	}
	s := New(db)
	site := fmt.Sprintf("zero-task-%d", time.Now().UnixNano())
	defer func() {
		for _, table := range []string{"billing_generation_tasks", "billing_jobs", "billing_money_snapshots"} {
			if _, e := db.Exec("DELETE FROM "+table+" WHERE instance_id=?", site); e != nil {
				t.Error(e)
			}
		}
	}()
	from := time.Date(2025, 9, 1, 0, 0, 0, 0, billing.BusinessLocation)
	money, _ := billing.NewMoneySnapshot(site, `{"QuotaPerUnit":"500000"}`, time.Now())
	for _, kind := range []string{"user_statement", "upstream_statement"} {
		t.Run(kind, func(t *testing.T) {
			j, _, _ := billing.NewJob(site, from, from.AddDate(0, 0, 1), "test")
			j.JobType, j.UserID, j.UpstreamID, j.UsageVersion, j.BillPeriod, j.Status, j.TotalSteps, j.MoneySnapshot = kind, 7, 9, 3, billing.ZeroOutputPeriod, "publishing", 0, money
			j.RequestKey = j.ID
			if kind == "user_statement" {
				if err = s.CreateBillingStatementJob(ctx, j, nil, "user"); err != nil {
					t.Fatal(err)
				}
			} else {
				tx, e := db.BeginTx(ctx, nil)
				if e != nil {
					t.Fatal(e)
				}
				defer tx.Rollback()
				if e = createBillingJobTx(ctx, tx, j, nil); e != nil {
					t.Fatal(e)
				}
				if _, e = tx.Exec(`INSERT INTO billing_statement_jobs(job_id,statement_type,subject_id,subject_name,created_at) VALUES(?,?,?,?,?)`, j.ID, kind, 9, "upstream", j.CreatedAt); e != nil {
					t.Fatal(e)
				}
				if e = recordStandaloneBillingTask(ctx, tx, j); e != nil {
					t.Fatal(e)
				}
				if e = tx.Commit(); e != nil {
					t.Fatal(e)
				}
			}
			if err = s.PutBillingUserDailyFile(ctx, billing.UserDailyFile{JobID: j.ID, InstanceID: site, BillDay: from, UserID: 7, RelativePath: j.ID + ".xlsx", FileSize: 1, SHA256: "test", CreatedAt: time.Now().AddDate(-1, 0, 0)}); err != nil {
				t.Fatal(err)
			}
			if err = s.FinalizeBillingJob(ctx, j); err != nil {
				t.Fatal(err)
			}
			loaded, e := s.BillingJob(ctx, j.ID)
			if e != nil || loaded.Status != "complete" || loaded.BillPeriod != billing.ZeroOutputPeriod {
				t.Fatal(loaded, e)
			}
			for _, table := range []string{"billing_statements", "billing_user_daily_active", "billing_active_versions"} {
				var n int
				if e = db.QueryRow("SELECT COUNT(*) FROM "+table+" WHERE job_id=?", j.ID).Scan(&n); e != nil || n != 0 {
					t.Fatal(table, n, e)
				}
			}
			task, e := s.BillingGenerationTask(ctx, site, "job:"+j.ID)
			if e != nil || task.Source != billing.ZeroOutputPeriod || task.Outcome != "complete" {
				t.Fatal(task, e)
			}
			page, e := s.BillingCatalog(ctx, billing.CatalogFilter{Site: site, Kind: kind, Period: billing.ZeroOutputPeriod, Page: 1, PageSize: 20})
			if e != nil || page.Total != 1 || len(page.Items) != 1 {
				t.Fatal(page, e)
			}
			expired, e := s.ListExpiredInactiveBillingFiles(ctx, time.Now(), 1000)
			if e != nil {
				t.Fatal(e)
			}
			for _, f := range expired {
				if f.JobID == j.ID {
					t.Fatal("published export scheduled for deletion")
				}
			}
			if kind == "user_statement" {
				retry, _, _ := billing.NewJob(site, from, j.To, "test")
				retry.JobType, retry.UserID, retry.UsageVersion, retry.BillPeriod, retry.RequestKey, retry.MoneySnapshot = kind, 7, 3, billing.ZeroOutputPeriod, j.RequestKey, money
				if e = s.CreateBillingStatementJob(ctx, retry, nil, "user"); e != nil {
					t.Fatal("completed export cannot regenerate", e)
				}
				duplicate := retry
				duplicate.ID = "duplicate-" + retry.ID
				if e = s.CreateBillingStatementJob(ctx, duplicate, nil, "user"); !errors.Is(e, billing.ErrStatementDuplicate) {
					t.Fatal("active export not deduplicated", e)
				}
			}
		})
	}
}
