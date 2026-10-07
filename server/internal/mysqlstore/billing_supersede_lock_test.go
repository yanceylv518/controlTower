package mysqlstore

import (
	"context"
	"errors"
	"fmt"
	"os"
	"testing"
	"time"

	"controltower/server/internal/billing"
)

func TestBillingSupersedeDoesNotLockOtherSubjects(t *testing.T) {
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
	for _, kind := range []string{"user_statement", "upstream_statement"} {
		t.Run(kind, func(t *testing.T) {
			site := fmt.Sprintf("supersede-lock-%d", time.Now().UnixNano())
			defer db.Exec(`DELETE FROM billing_jobs WHERE instance_id=?`, site)
			from := time.Date(2025, 9, 1, 0, 0, 0, 0, billing.BusinessLocation)
			makeJob := func(subject int64, status string) billing.Job {
				j, _, e := billing.NewJob(site, from, from.AddDate(0, 1, 0), "test")
				if e != nil {
					t.Fatal(e)
				}
				j.JobType, j.BillPeriod, j.UsageVersion, j.Status = kind, "monthly", 3, status
				j.UserID, j.UpstreamID, j.RequestKey = subject, subject, j.ID
				tx, e := db.BeginTx(ctx, nil)
				if e != nil {
					t.Fatal(e)
				}
				defer tx.Rollback()
				if e = createBillingJobTx(ctx, tx, j, nil); e != nil {
					t.Fatal(e)
				}
				if _, e = tx.ExecContext(ctx, `INSERT INTO billing_statement_jobs(job_id,statement_type,subject_id,subject_name,created_at) VALUES(?,?,?,'',?)`, j.ID, kind, subject, j.CreatedAt); e != nil {
					t.Fatal(e)
				}
				if e = tx.Commit(); e != nil {
					t.Fatal(e)
				}
				return j
			}
			old := makeJob(2, "complete")
			other := makeJob(26, "publishing")
			replacement := makeJob(2, "publishing")
			blocker, err := db.BeginTx(ctx, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer blocker.Rollback()
			if _, err = blocker.ExecContext(ctx, `UPDATE billing_jobs SET updated_at=UTC_TIMESTAMP(6) WHERE id=?`, other.ID); err != nil {
				t.Fatal(err)
			}
			short, stop := context.WithTimeout(ctx, 2*time.Second)
			defer stop()
			tx, err := db.BeginTx(short, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback()
			if err = supersedeBillingStatement(short, tx, replacement); err != nil {
				t.Fatalf("unrelated publisher blocked replacement: %v", err)
			}
			if err = tx.Commit(); err != nil {
				t.Fatal(err)
			}
			var status string
			if err = db.QueryRowContext(ctx, `SELECT status FROM billing_jobs WHERE id=?`, old.ID).Scan(&status); err != nil || status != "superseded" {
				t.Fatalf("old bill: %s %v", status, err)
			}
			if err = db.QueryRowContext(ctx, `SELECT status FROM billing_jobs WHERE id=?`, other.ID).Scan(&status); err != nil || status != "publishing" {
				t.Fatalf("other bill changed: %s %v", status, err)
			}
			_ = blocker.Rollback()
			if _, err = db.ExecContext(ctx, `UPDATE billing_jobs SET status='complete' WHERE id=?`, old.ID); err != nil {
				t.Fatal(err)
			}
			rollbackTx, err := db.BeginTx(ctx, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer rollbackTx.Rollback()
			if err = supersedeBillingStatement(ctx, rollbackTx, replacement); err != nil {
				t.Fatal(err)
			}
			if err = rollbackTx.Rollback(); err != nil {
				t.Fatal(err)
			}
			if err = db.QueryRowContext(ctx, `SELECT status FROM billing_jobs WHERE id=?`, old.ID).Scan(&status); err != nil || status != "complete" {
				t.Fatalf("rollback lost previous bill: %s %v", status, err)
			}
			_, release, err := s.LockGenerationSite(ctx, site)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { release() }()
			candidate, _, err := billing.NewJob(site, from.AddDate(0, 1, 0), from.AddDate(0, 2, 0), "test")
			if err != nil {
				t.Fatal(err)
			}
			candidate.JobType, candidate.BillPeriod, candidate.UsageVersion, candidate.UserID, candidate.UpstreamID = kind, "monthly", 3, 99, 99
			if err = s.CreateBillingStatementJob(ctx, candidate, nil, ""); !errors.Is(err, billing.ErrStatementQueueFull) {
				t.Fatalf("month bypassed site lease: %v", err)
			}
			otherSite := candidate
			otherSite.InstanceID = site + "-other"
			if err = s.CreateBillingStatementJob(ctx, otherSite, nil, ""); !errors.Is(err, billing.ErrStatementNoData) {
				t.Fatalf("busy site blocked another site: %v", err)
			}
			release()
			release = func() {}
			if err = s.CreateBillingStatementJob(ctx, candidate, nil, ""); !errors.Is(err, billing.ErrStatementNoData) {
				t.Fatalf("empty month after release: %v", err)
			}
		})
	}
}
