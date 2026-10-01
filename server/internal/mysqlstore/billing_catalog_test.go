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

func TestCatalogLiteralSearchAndMonthBoundaries(t *testing.T) {
	where, args := catalogWhere(billing.CatalogFilter{Site: "a", Kind: "user_statement", Month: "2026-09", Query: "50%_!"})
	if !strings.Contains(where, "j.status='complete'") || !strings.Contains(where, "j.usage_version>=3") || args[2].(time.Time).Format(time.RFC3339) != "2026-09-30T16:00:00Z" || args[3].(time.Time).Format(time.RFC3339) != "2026-08-31T16:00:00Z" || args[4] != "%50!%!_!!%" {
		t.Fatal(where, args)
	}
}

func TestBillingCatalogFullHistoryAndIsolation(t *testing.T) {
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
	site := fmt.Sprintf("catalog-%d", time.Now().UnixNano())
	s := New(db)
	defer db.Exec(`DELETE FROM billing_jobs WHERE instance_id IN (?,?)`, site, site+"-other")
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	day := time.Date(2026, 9, 1, 0, 0, 0, 0, billing.BusinessLocation)
	insert := func(i int, kind, period, status, name, instance string, version int) {
		t.Helper()
		job, _, e := billing.NewJob(instance, day, day.AddDate(0, 0, 1), "test")
		if e != nil {
			t.Fatal(e)
		}
		job.JobType = kind
		job.BillPeriod = period
		job.Status = status
		job.UsageVersion = version
		job.UserID = int64(i + 1)
		job.RequestKey = ""
		job.CreatedAt = day.Add(time.Duration(i) * time.Second)
		job.UpdatedAt = job.CreatedAt
		if period == "monthly" {
			job.To = day.AddDate(0, 1, 0)
		}
		if period == "temporary" {
			job.To = day.Add(time.Hour)
		}
		if e = createBillingJobTx(ctx, tx, job, nil); e != nil {
			t.Fatal(e)
		}
		if _, e = tx.ExecContext(ctx, `INSERT INTO billing_statement_jobs(job_id,statement_type,subject_id,subject_name,created_at) VALUES(?,?,?,?,?)`, job.ID, kind, i+1, name, job.CreatedAt); e != nil {
			t.Fatal(e)
		}
	}
	for i := 0; i < 521; i++ {
		name := "customer"
		if i == 0 {
			name = "Old%_!"
		}
		insert(i, "user_statement", "daily", "complete", name, site, 3)
	}
	insert(600, "user_statement", "monthly", "complete", "month", site, 3)
	insert(601, "user_statement", "temporary", "complete", "temp", site, 3)
	insert(602, "user_statement", "daily", "failed", "failed", site, 3)
	insert(603, "user_statement", "daily", "superseded", "old", site, 3)
	insert(604, "user_statement", "daily", "complete", "legacy", site, 2)
	insert(605, "upstream_statement", "daily", "complete", "provider", site, 3)
	insert(606, "user_statement", "daily", "complete", "other", site+"-other", 3)
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
	f := billing.CatalogFilter{Site: site, Kind: "user_statement", Period: "daily", Page: 27, PageSize: 20}
	page, err := s.BillingCatalog(ctx, f)
	if err != nil {
		t.Fatal(err)
	}
	if page.Total != 521 || page.Subjects != 521 || page.Counts["monthly"] != 1 || page.Counts["temporary"] != 1 || len(page.Items) != 1 || page.Items[0].Job.UserName != "Old%_!" {
		t.Fatal(page)
	}
	f.Page = 1
	f.Query = "Old%_!"
	page, err = s.BillingCatalog(ctx, f)
	if err != nil || page.Total != 1 {
		t.Fatal(page, err)
	}
	f.Query = page.Items[0].Job.BillNo
	page, err = s.BillingCatalog(ctx, f)
	if err != nil || page.Total != 1 {
		t.Fatal(page, err)
	}
	f.Query = ""
	f.Month = "2026-08"
	page, err = s.BillingCatalog(ctx, f)
	if err != nil || page.Total != 0 {
		t.Fatal(page, err)
	}
	f.Month = "2026-09"
	f.Kind = "upstream_statement"
	page, err = s.BillingCatalog(ctx, f)
	if err != nil || page.Total != 1 || page.Items[0].Job.UpstreamName != "provider" {
		t.Fatal(page, err)
	}
}
