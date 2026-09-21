package mysqlstore

import (
	"context"
	"controltower/server/internal/billing"
	"fmt"
	"github.com/go-sql-driver/mysql"
	"os"
	"testing"
	"time"
)

func TestMoneySnapshotMySQL(t *testing.T) {
	dsn := os.Getenv("CT_ARCHIVE_TEST_DSN")
	if dsn == "" {
		t.Skip("set CT_ARCHIVE_TEST_DSN for isolated local MySQL")
	}
	config, err := mysql.ParseDSN(dsn)
	if err != nil {
		t.Fatal("invalid test DSN")
	}
	if config.Net != "tcp" || (config.Addr != "127.0.0.1:3306" && config.Addr != "localhost:3306") {
		t.Fatal("money test requires local disposable MySQL on 3306")
	}
	config.DBName = ""
	admin, err := Open(config.FormatDSN())
	if err != nil {
		t.Fatal(err)
	}
	defer admin.Close()
	name := fmt.Sprintf("ct_money_test_%d", time.Now().UnixNano())
	if _, err = admin.Exec("CREATE DATABASE `" + name + "`"); err != nil {
		t.Fatal(err)
	}
	defer admin.Exec("DROP DATABASE `" + name + "`")
	config.DBName = name
	config.ParseTime = true
	db, err := Open(config.FormatDSN())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ctx := context.Background()
	for i := 0; i < 2; i++ {
		if err = ApplyDir(ctx, db, "../../migrations"); err != nil {
			t.Fatal(err)
		}
	}
	store := New(db)
	day := time.Date(2026, 9, 20, 0, 0, 0, 0, billing.BusinessLocation)
	job, steps, err := billing.NewJob("site", day, day.AddDate(0, 0, 1), "test")
	if err != nil {
		t.Fatal(err)
	}
	job.JobType = "user_statement"
	job.UserID = 7
	job.RequestKey = "test-fixed-money"
	job.PricingSource = billing.PricingSourceNewAPI
	if store.CreateBillingStatementJob(ctx, job, steps, "") == nil {
		t.Fatal("unbound new statement accepted")
	}
	job.MoneySnapshot, err = billing.NewMoneySnapshot(job.InstanceID, `{"QuotaPerUnit":"500000"}`, day)
	if err != nil {
		t.Fatal(err)
	}
	if err = store.CreateBillingStatementJob(ctx, job, steps, ""); err != nil {
		t.Fatal(err)
	}
	loaded, err := store.BillingJob(ctx, job.ID)
	if err != nil || loaded.MoneySnapshot == nil || loaded.MoneySnapshot.ID != job.MoneySnapshot.ID {
		t.Fatalf("read binding: %v %+v", err, loaded)
	}
	if err = store.CancelBillingJob(ctx, job.ID); err != nil {
		t.Fatal(err)
	}
	previous, err := store.FailedStatementMoneySnapshot(ctx, job.RequestKey)
	if err != nil || previous == nil || previous.ID != job.MoneySnapshot.ID {
		t.Fatalf("retry binding: %v", err)
	}
	retry, retrySteps, err := billing.NewJob("site", day, day.AddDate(0, 0, 1), "test")
	if err != nil {
		t.Fatal(err)
	}
	retry.JobType = job.JobType
	retry.UserID = job.UserID
	retry.RequestKey = job.RequestKey
	retry.MoneySnapshot, _ = billing.NewMoneySnapshot("site", `{"QuotaPerUnit":"1000000"}`, day.Add(time.Hour))
	if store.CreateBillingStatementJob(ctx, retry, retrySteps, "") == nil {
		t.Fatal("failed retry replaced snapshot")
	}
	retry.MoneySnapshot = previous
	if err = store.CreateBillingStatementJob(ctx, retry, retrySteps, ""); err != nil {
		t.Fatal(err)
	}
	var count int
	if err = db.QueryRow("SELECT COUNT(*) FROM billing_money_snapshots").Scan(&count); err != nil || count != 1 {
		t.Fatalf("immutable observation count=%d err=%v", count, err)
	}
	bound, err := store.BillingJobMoneySnapshot(ctx, retry.ID)
	if err != nil || bound == nil || bound.QuotaPerUnit != "500000" {
		t.Fatalf("retry=%+v err=%v", bound, err)
	}
	// A tampered payload cannot be substituted under the old binding.
	if _, err = db.Exec(`UPDATE billing_money_snapshots SET snapshot_json=REPLACE(snapshot_json,'500000','1000000') WHERE id=?`, previous.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = store.BillingJobMoneySnapshot(ctx, retry.ID); err == nil {
		t.Fatal("corrupted immutable context accepted")
	}
}
