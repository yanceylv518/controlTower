package mysqlstore

import (
	"context"
	"controltower/server/internal/billing"
	"fmt"
	"os"
	"testing"
	"time"
)

func TestBillingZeroOutputPolicyPersistsAcrossReload(t *testing.T) {
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
	site := fmt.Sprintf("zero-policy-%d", time.Now().UnixNano())
	defer func() {
		for _, table := range []string{"billing_generation_tasks", "billing_generation_ranges", "billing_automatic_targets"} {
			if _, e := db.Exec("DELETE FROM "+table+" WHERE instance_id=?", site); e != nil {
				t.Error(e)
			}
		}
	}()
	from := time.Date(2025, 9, 1, 0, 0, 0, 0, billing.BusinessLocation)
	for _, kind := range []string{"user_statement", "upstream_statement"} {
		target := billing.AutomaticTarget{InstanceID: site, Kind: kind, SubjectID: 7, From: from, To: from.AddDate(0, 1, 0), ExcludeZeroOutput: kind == "user_statement"}
		// Opposite-to-default choice must survive persistence and an overlapping
		// automatic target; SQL reads must not substitute the kind's default.
		if err = s.PutBillingAutomaticTargets(ctx, []billing.AutomaticTarget{target}); err != nil {
			t.Fatal(err)
		}
		targets, e := s.ListBillingAutomaticTargets(ctx)
		if e != nil {
			t.Fatal(e)
		}
		found := false
		for _, v := range targets {
			if v.InstanceID == site && v.Kind == kind && !v.To.IsZero() {
				found = true
				if v.ExcludeZeroOutput != target.ExcludeZeroOutput {
					t.Fatal(v)
				}
			}
		}
		if !found {
			t.Fatal("range not reloaded")
		}
		automatic := target
		automatic.To = time.Time{}
		automatic.ExcludeZeroOutput = !target.ExcludeZeroOutput
		excluded, e := s.BillingGenerationExcludeZeroOutput(ctx, automatic)
		if e != nil || excluded != target.ExcludeZeroOutput {
			t.Fatal(excluded, e)
		}
		var batch string
		if e = db.QueryRow(`SELECT batch_id FROM billing_generation_ranges WHERE instance_id=? AND kind=?`, site, kind).Scan(&batch); e != nil {
			t.Fatal(e)
		}
		task, e := s.BillingGenerationTask(ctx, site, batch)
		if e != nil || task.ExcludeZeroOutput != target.ExcludeZeroOutput {
			t.Fatal(task, e)
		}
		if err = s.CancelBillingGeneration(ctx, []billing.AutomaticTarget{target}); err != nil {
			t.Fatal(err)
		}
	}
}
