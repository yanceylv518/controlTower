package mysqlstore

import (
	"context"
	"controltower/server/internal/billing"
	"errors"
	"fmt"
	"math/big"
	"os"
	"sync"
	"testing"
	"time"
)

func TestBillingDailyOnceAndMonthlyReuse(t *testing.T) {
	for _, kind := range []string{"user_statement", "upstream_statement"} {
		t.Run(kind, func(t *testing.T) { testBillingDailyOnceAndMonthlyReuse(t, kind) })
	}
}
func testBillingDailyOnceAndMonthlyReuse(t *testing.T, kind string) {
	dsn := os.Getenv("CT_MYSQL_TEST_DSN")
	if dsn == "" {
		t.Skip("requires local CT_MYSQL_TEST_DSN")
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
	site := fmt.Sprintf("billing-once-%d", time.Now().UnixNano())
	defer func() {
		for _, table := range []string{"billing_generation_tasks", "billing_generation_ranges", "billing_automatic_targets", "billing_compact_daily_totals", "billing_day_checks", "billing_activity_days", "billing_jobs", "billing_money_snapshots", "billing_upstreams"} {
			if _, e := db.Exec("DELETE FROM "+table+" WHERE instance_id=?", site); e != nil {
				t.Error(e)
			}
		}
	}()
	var upstreamID int64
	if kind == "upstream_statement" {
		result, e := db.ExecContext(ctx, `INSERT INTO billing_upstreams(instance_id,name,created_at,updated_at) VALUES(?,'test upstream',UTC_TIMESTAMP(6),UTC_TIMESTAMP(6))`, site)
		if e != nil {
			t.Fatal(e)
		}
		upstreamID, _ = result.LastInsertId()
		if _, e = db.ExecContext(ctx, `INSERT INTO billing_upstream_channel_bindings(instance_id,upstream_id,channel_id,channel_name,created_at) VALUES(?,?,252,'test channel',UTC_TIMESTAMP(6))`, site, upstreamID); e != nil {
			t.Fatal(e)
		}
	}
	from := time.Date(2025, 9, 1, 0, 0, 0, 0, billing.BusinessLocation)
	to := from.AddDate(0, 1, 0)
	target := billing.AutomaticTarget{InstanceID: site, Kind: kind, SubjectID: 7, From: from, To: to}
	money, err := billing.NewMoneySnapshot(site, `{"QuotaPerUnit":500000,"USDExchangeRate":7.2,"DisplayInCurrencyEnabled":true}`, time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	daily, _, err := billing.NewJob(site, from, from.AddDate(0, 0, 1), "test")
	if err != nil {
		t.Fatal(err)
	}
	daily.JobType = kind
	daily.UserID = 7
	if kind == "upstream_statement" {
		daily.UserID = 0
		daily.UpstreamID = upstreamID
		target.SubjectID = upstreamID
	}
	daily.UsageVersion = 3
	daily.BillPeriod = "daily"
	daily.DataSource = "source"
	daily.MoneySnapshot = money
	daily.Status = "complete"
	daily.TotalSteps = 0
	daily.RequestKey = "test:" + daily.ID
	if err = s.CreateBillingStatementJob(ctx, daily, nil, ""); err != nil {
		t.Fatal(err)
	}
	if _, err = db.ExecContext(ctx, `INSERT INTO billing_compact_daily_totals(job_id,instance_id,bill_day,user_id,username,model_name,request_count,prompt_tokens,completion_tokens,total_amount,empty_output_count,empty_output_amount,updated_at) VALUES(?,?,?,?,?,?,2,120,30,1.25,1,0.25,UTC_TIMESTAMP(6))`, daily.ID, site, "2025-09-01", 7, "test user", "model-a"); err != nil {
		t.Fatal(err)
	}

	if _, err = db.ExecContext(ctx, `UPDATE billing_compact_daily_totals SET unit_prices='{"输入|2":true,"输入|3":true,"输出|8":true}',before_amount=2.5,before_known_count=2,settlement_discount='0.500000' WHERE job_id=?`, daily.ID); err != nil {
		t.Fatal(err)
	}

	// Identical calendar dates in another upstream and in a user bill must never
	// be merged into this upstream month or superseded by its replacements.
	sentinels := []string{}
	if kind == "upstream_statement" {
		result, e := db.ExecContext(ctx, `INSERT INTO billing_upstreams(instance_id,name,created_at,updated_at) VALUES(?,'other upstream',UTC_TIMESTAMP(6),UTC_TIMESTAMP(6))`, site)
		if e != nil {
			t.Fatal(e)
		}
		otherID, _ := result.LastInsertId()
		if _, e = db.ExecContext(ctx, `INSERT INTO billing_upstream_channel_bindings(instance_id,upstream_id,channel_id,created_at) VALUES(?,?,253,UTC_TIMESTAMP(6))`, site, otherID); e != nil {
			t.Fatal(e)
		}
		for i, otherKind := range []string{"upstream_statement", "user_statement"} {
			other := daily
			other.ID = fmt.Sprintf("sentinel%d%s", i, daily.ID[:20])
			other.RequestKey = other.ID
			other.JobType = otherKind
			other.UpstreamID = otherID
			other.UserID = 0
			if otherKind == "user_statement" {
				other.UpstreamID = 0
				other.UserID = target.SubjectID
			}
			if e = s.CreateBillingStatementJob(ctx, other, nil, "sentinel"); e != nil {
				t.Fatal(e)
			}
			if _, e = db.ExecContext(ctx, `INSERT INTO billing_compact_daily_totals(job_id,instance_id,bill_day,user_id,username,model_name,request_count,total_amount,updated_at) VALUES(?,?,?,0,'sentinel','model-a',1,999,UTC_TIMESTAMP(6))`, other.ID, site, "2025-09-01"); e != nil {
				t.Fatal(e)
			}
			sentinels = append(sentinels, other.ID)
		}
		defer func() {
			for _, id := range sentinels {
				j, e := s.BillingJob(ctx, id)
				if e != nil || j.Status != "complete" {
					t.Error("unrelated bill changed", id, j.Status, e)
				}
			}
		}()
	}
	clone := daily
	clone.ID += "x"
	clone.RequestKey += "x"
	clone.DataSource = "archive"
	if err = s.CreateBillingStatementJob(ctx, clone, nil, ""); !errors.Is(err, billing.ErrStatementDuplicate) {
		t.Fatalf("cross-source duplicate allowed: %v", err)
	}
	month, _, err := billing.NewJob(site, from, to, "test")
	if err != nil {
		t.Fatal(err)
	}
	month.JobType = kind
	month.UserID = daily.UserID
	month.UpstreamID = daily.UpstreamID
	month.UsageVersion = 3
	month.BillPeriod = "monthly"
	month.DataSource = "source"
	month.RequestKey = "test:" + month.ID
	if err = s.CreateBillingStatementJob(ctx, month, nil, ""); err != nil {
		t.Fatalf("partial month not published: %v", err)
	}
	for day := from.AddDate(0, 0, 1); day.Before(to); day = day.AddDate(0, 0, 1) {
		v := target
		v.From = day
		v.To = day.AddDate(0, 0, 1)
		if err = s.RecordBillingDayActivity(ctx, v, false); err != nil {
			t.Fatal(err)
		}
	}
	missing, err := s.MissingBillingDays(ctx, target, to)
	if err != nil || len(missing) != 0 {
		t.Fatalf("checked empty dates remained missing: %v %v", missing, err)
	}
	if err = s.CreateBillingStatementJob(ctx, month, nil, ""); !errors.Is(err, billing.ErrStatementDuplicate) {
		t.Fatal("unchanged month should be reused", err)
	}
	result, err := s.BillingJob(ctx, month.ID)
	if err != nil || result.Status != "complete" || result.MoneySnapshot.ID != money.ID {
		t.Fatalf("month result: %+v %v", result, err)
	}
	aggregates, e := s.QueryBillingStatementAggregates(ctx, month.ID)
	if e != nil || len(aggregates) != 1 || aggregates[0].BeforeAmount != "2.500000000000" || aggregates[0].SettlementDiscount != "0.500000" {
		t.Fatalf("monthly originals lost: %+v %v", aggregates, e)
	}
	if got := billing.UnitPriceLabel(aggregates[0].UnitPrices, big.NewRat(1, 1)); got != "输入 2 / 3；输出 8" {
		t.Fatal("monthly price snapshot:", got)
	}
	tokenRows, e := s.QueryBillingTokenRows(ctx, month.ID, 7, -1, from, to)
	if e != nil || len(tokenRows) != 1 || tokenRows[0].BeforeAmount != "2.500000000000" {
		t.Fatalf("token originals lost: %+v %v", tokenRows, e)
	}
	workspace, e := s.BillingWorkspace(ctx, site, kind, target.SubjectID, from, to)
	if e != nil || len(workspace) == 0 {
		t.Fatal("workspace", e)
	}
	for _, bill := range workspace {
		if bill.Job.ID == month.ID && (bill.BeforeAmount != "2.500000000000" || bill.Discount != "0.500000") {
			t.Fatal("workspace originals", bill)
		}
	}

	var amount string
	var requests, empty, steps, lineage int
	if err = db.QueryRowContext(ctx, `SELECT CAST(SUM(total_amount) AS CHAR),SUM(request_count),SUM(empty_output_count) FROM billing_compact_daily_totals WHERE job_id=?`, month.ID).Scan(&amount, &requests, &empty); err != nil {
		t.Fatal(err)
	}
	if amount != "1.250000000000" || requests != 2 || empty != 1 {
		t.Fatalf("month repriced or omitted empty output: %s %d %d", amount, requests, empty)
	}
	if err = db.QueryRowContext(ctx, `SELECT COUNT(*) FROM billing_job_steps WHERE job_id=?`, month.ID).Scan(&steps); err != nil {
		t.Fatal(err)
	}
	if err = db.QueryRowContext(ctx, `SELECT COUNT(*) FROM billing_month_daily_sources WHERE month_job_id=?`, month.ID).Scan(&lineage); err != nil {
		t.Fatal(err)
	}
	if steps != 0 || lineage != 1 {
		t.Fatalf("month created source-reading steps: %d, lineage %d", steps, lineage)
	}
	if err = s.CreateBillingStatementJob(ctx, month, nil, ""); !errors.Is(err, billing.ErrStatementDuplicate) {
		t.Fatalf("monthly duplicate allowed: %v", err)
	}
	// An entirely empty month produces no invoice.
	emptyTarget := target
	emptyTarget.SubjectID = 8
	if kind == "upstream_statement" {
		emptyTarget.SubjectID = upstreamID + 10000
	}
	for day := from; day.Before(to); day = day.AddDate(0, 0, 1) {
		emptyTarget.From = day
		if err = s.RecordBillingDayActivity(ctx, emptyTarget, false); err != nil {
			t.Fatal(err)
		}
	}
	emptyMonth := month
	emptyMonth.UserID = 8
	if kind == "upstream_statement" {
		emptyMonth.UserID = 0
		emptyMonth.UpstreamID = emptyTarget.SubjectID
	}
	emptyMonth.ID += "e"
	emptyMonth.RequestKey += "e"
	if err = s.CreateBillingStatementJob(ctx, emptyMonth, nil, ""); !errors.Is(err, billing.ErrStatementNoData) {
		t.Fatalf("empty monthly invoice created: %v", err)
	}

	emptyTarget.From = from
	if outcome, e := s.BillingGenerationFeedback(ctx, emptyTarget); e != nil || outcome != "no_consumption" {
		t.Fatal("empty feedback", outcome, e)
	}
	if outcome, e := s.BillingGenerationFeedback(ctx, target); e != nil || outcome != "already_complete" {
		t.Fatal("completed feedback", outcome, e)
	}
	if err = s.PutBillingAutomaticTargets(ctx, []billing.AutomaticTarget{target, emptyTarget}); err != nil {
		t.Fatal(err)
	}

	if kind == "upstream_statement" {
		if _, e := db.ExecContext(ctx, `UPDATE billing_upstreams SET enabled=0 WHERE instance_id=? AND id=?`, site, upstreamID); e != nil {
			t.Fatal(e)
		}
		up, e := s.BillingStatementUpstream(ctx, site, upstreamID)
		if e != nil || up.Enabled {
			t.Fatal("manual upstream unavailable when automatic billing off", up, e)
		}
		targets, e := s.ListBillingAutomaticTargets(ctx)
		if e != nil {
			t.Fatal(e)
		}
		manual := false
		for _, v := range targets {
			if v.InstanceID == site && v.SubjectID == upstreamID && v.Kind == kind {
				if v.To.IsZero() {
					t.Fatal("disabled automatic target returned")
				}
				manual = true
			}
		}
		if !manual {
			t.Fatal("submitted manual range removed")
		}
		// Remaining lifecycle checks run with automatic billing disabled, including
		// explicit overwrite, cancellation and re-generation of historical bills.
	}
	p, err := s.BillingGenerationProgress(ctx, target)
	if err != nil || p.Outcome != "complete" || p.TotalDays != 30 || p.Checked != 30 || p.Complete != 1 || p.Empty != 29 || p.Monthly == nil || p.Monthly.Status != "complete" {
		t.Fatalf("completed progress: %+v %v", p, err)
	}
	p, err = s.BillingGenerationProgress(ctx, emptyTarget)
	if err != nil || p.Outcome != "no_consumption" || p.Empty != 30 || p.Monthly != nil {
		t.Fatalf("empty progress: %+v %v", p, err)
	}
	members, e := s.BillingBatchMembers(ctx, []billing.AutomaticTarget{emptyTarget})
	if e != nil || len(members) != 2 || members[0].BatchID == "" || members[0].BatchID != members[1].BatchID {
		t.Fatal("batch members lost", members, e)
	}
	if _, e = db.ExecContext(ctx, `DELETE FROM billing_day_checks WHERE instance_id=? AND subject_id=? AND bill_day='2025-09-01'`, site, emptyTarget.SubjectID); e != nil {
		t.Fatal(e)
	}
	restored, e := s.BillingGenerationState(ctx, site)
	if e != nil || len(restored.Targets) != 2 {
		t.Fatal("restore omitted completed batch member", restored, e)
	}
	if e = s.RecordBillingDayActivity(ctx, emptyTarget, false); e != nil {
		t.Fatal(e)
	}
	if err = s.RecordBillingGenerationAttempt(ctx, target, errors.New("source unavailable")); err != nil {
		t.Fatal(err)
	}
	p, err = s.BillingGenerationProgress(ctx, target)
	if err != nil || p.Error != "source unavailable" || p.LastAttempt == nil || p.Outcome != "failed" {
		t.Fatalf("failure progress: %+v %v", p, err)
	}
	if err = s.RecordBillingGenerationAttempt(ctx, target, nil); err != nil {
		t.Fatal(err)
	}
	p, err = s.BillingGenerationProgress(ctx, target)
	if err != nil || p.Error != "" || p.Outcome != "complete" {
		t.Fatalf("recovered progress: %+v %v", p, err)
	}

	// Concurrent manual submissions accept only one new range, including before jobs exist.
	var wg sync.WaitGroup
	results := make(chan error, 2)
	concurrentIDs := []int64{emptyTarget.SubjectID + 1, emptyTarget.SubjectID + 2}
	for _, id := range concurrentIDs {
		wg.Add(1)
		go func(id int64) {
			defer wg.Done()
			v := target
			v.SubjectID = id
			results <- s.PutBillingAutomaticTargets(ctx, []billing.AutomaticTarget{v})
		}(id)
	}
	wg.Wait()
	close(results)
	accepted, rejected := 0, 0
	for e := range results {
		if e == nil {
			accepted++
		} else if errors.Is(e, billing.ErrGenerationInProgress) {
			rejected++
		} else {
			t.Fatal(e)
		}
	}
	if accepted != 1 || rejected != 1 {
		t.Fatalf("concurrent acceptance %d rejection %d", accepted, rejected)
	}
	if busy, e := s.BillingGenerationBusy(ctx, site); e != nil || !busy {
		t.Fatal("registered work not busy", busy, e)
	}
	if _, e := db.ExecContext(ctx, "DELETE FROM billing_generation_ranges WHERE instance_id=? AND subject_id IN (?,?)", site, concurrentIDs[0], concurrentIDs[1]); e != nil {
		t.Fatal(e)
	}
	if busy, e := s.BillingGenerationBusy(ctx, site); e != nil || busy {
		t.Fatal("completed work still busy", busy, e)
	}
	// The month owns its aggregate snapshot; a subsequent daily change cannot alter it.
	if _, err = db.ExecContext(ctx, `UPDATE billing_compact_daily_totals SET total_amount=9 WHERE job_id=?`, daily.ID); err != nil {
		t.Fatal(err)
	}
	if err = db.QueryRowContext(ctx, `SELECT CAST(SUM(total_amount) AS CHAR) FROM billing_compact_daily_totals WHERE job_id=?`, month.ID).Scan(&amount); err != nil || amount != "1.250000000000" {
		t.Fatal("month changed", amount, err)
	}
	// Overwrite keeps the previous successful bill until a replacement is complete.
	target.Overwrite = true
	if err = s.PutBillingAutomaticTargets(ctx, []billing.AutomaticTarget{target}); err != nil {
		t.Fatal(err)
	}
	missing, err = s.MissingBillingDays(ctx, target, to)
	if err != nil || len(missing) != 30 {
		t.Fatal("overwrite did not schedule month", len(missing), err)
	}
	version, err := s.BillingGenerationVersion(ctx, target)
	if err != nil || version == "" {
		t.Fatal(version, err)
	}
	replacement := daily
	replacement.ID += "r"
	replacement.RequestKey = "replacement:" + version
	replacement.CreatedAt = time.Now().UTC()
	replacement.UpdatedAt = replacement.CreatedAt
	replacement.Status = "pending"
	if err = s.CreateBillingStatementJob(ctx, replacement, nil, ""); err != nil {
		t.Fatal(err)
	}
	previous, err := s.BillingJob(ctx, daily.ID)
	if err != nil || previous.Status != "complete" {
		t.Fatal("old bill lost during generation", previous.Status, err)
	}
	state, err := s.BillingGenerationState(ctx, site)
	if err != nil || !state.Busy || len(state.Targets) != 1 {
		t.Fatal("restore missing active target", state, err)
	}
	if err = s.CancelBillingGeneration(ctx, []billing.AutomaticTarget{target}); err != nil {
		t.Fatal(err)
	}
	if _, err = s.BillingGenerationVersion(ctx, target); !errors.Is(err, billing.ErrGenerationCancelled) {
		t.Fatal("cancel not persistent", err)
	}
	if err = s.FailBillingStep(ctx, replacement, billing.JobStep{StepNo: 0}, errors.New("late worker failure")); err != nil {
		t.Fatal(err)
	}
	stopped, err := s.BillingJob(ctx, replacement.ID)
	if err != nil || stopped.Status != "failed" || stopped.ErrorMessage != "cancelled manually" {
		t.Fatal("cancelled job revived", stopped.Status, err)
	}
	previous, err = s.BillingJob(ctx, daily.ID)
	if err != nil || previous.Status != "complete" {
		t.Fatal("cancel removed completed day", previous.Status, err)
	}
	p, err = s.BillingGenerationProgress(ctx, target)
	if err != nil || p.Outcome != "cancelled" {
		t.Fatal("cancel state", p, err)
	}
	if busy, e := s.BillingGenerationBusy(ctx, site); e != nil || busy {
		t.Fatal("cancel not released", busy, e)
	}
	// Default resume reuses completed days rather than scheduling replacements.
	target.Overwrite = false
	if err = s.PutBillingAutomaticTargets(ctx, []billing.AutomaticTarget{target}); err != nil {
		t.Fatal(err)
	}
	missing, err = s.MissingBillingDays(ctx, target, to)
	if err != nil {
		t.Fatal(err)
	}
	for _, day := range missing {
		if day.Equal(from) {
			t.Fatal("default resume regenerated completed day")
		}
	}
	if err = s.CancelBillingGeneration(ctx, []billing.AutomaticTarget{target}); err != nil {
		t.Fatal(err)
	}
	target.Overwrite = true
	stale := replacement
	stale.ID += "stale"
	stale.RequestKey += "stale"
	if err = s.CreateBillingStatementJob(ctx, stale, nil, ""); !errors.Is(err, billing.ErrGenerationCancelled) {
		t.Fatal("stale scheduler queued after cancel", err)
	}
	if err = s.PutBillingAutomaticTargets(ctx, []billing.AutomaticTarget{target}); err != nil {
		t.Fatal(err)
	}
	version, err = s.BillingGenerationVersion(ctx, target)
	if err != nil {
		t.Fatal(err)
	}
	replacement.ID += "2"
	replacement.RequestKey = "replacement:" + version
	replacement.CreatedAt = time.Now().UTC()
	replacement.UpdatedAt = replacement.CreatedAt
	if err = s.CreateBillingStatementJob(ctx, replacement, nil, ""); err != nil {
		t.Fatal(err)
	}
	if _, err = db.ExecContext(ctx, `INSERT INTO billing_compact_daily_totals(job_id,instance_id,bill_day,user_id,username,model_name,request_count,prompt_tokens,completion_tokens,total_amount,updated_at) VALUES(?,?,?,?,?,?,2,120,30,2.5,UTC_TIMESTAMP(6))`, replacement.ID, site, "2025-09-01", 7, "test user", "model-a"); err != nil {
		t.Fatal(err)
	}
	if err = s.FinalizeBillingJob(ctx, replacement); err != nil {
		t.Fatal(err)
	}
	previous, err = s.BillingJob(ctx, daily.ID)
	if err != nil || previous.Status != "superseded" {
		t.Fatal("successful replacement not activated", previous.Status, err)
	}
	for day := from.AddDate(0, 0, 1); day.Before(to); day = day.AddDate(0, 0, 1) {
		v := target
		v.From = day
		v.To = day.AddDate(0, 0, 1)
		if err = s.RecordBillingDayActivity(ctx, v, false); err != nil {
			t.Fatal(err)
		}
	}
	month.ID += "r"
	month.RequestKey = "month:" + version
	month.CreatedAt = time.Now().UTC()
	month.UpdatedAt = month.CreatedAt
	if err = s.CreateBillingStatementJob(ctx, month, nil, ""); err != nil {
		t.Fatal(err)
	}
	p, err = s.BillingGenerationProgress(ctx, target)
	if err != nil || p.Outcome != "complete" || p.Complete != 1 || p.Empty != 29 {
		t.Fatal("replacement progress", p, err)
	}
	if err = db.QueryRowContext(ctx, `SELECT CAST(SUM(total_amount) AS CHAR) FROM billing_compact_daily_totals WHERE job_id=?`, month.ID).Scan(&amount); err != nil || amount != "2.500000000000" {
		t.Fatal("month used old daily bill", amount, err)
	}

	// Every submission retains its own original members and result after later overwrites.
	historical, e := s.BillingGenerationTask(ctx, site, members[0].BatchID)
	if e != nil || len(historical.SubjectIDs) != 2 || historical.Outcome != "complete" || historical.Percentage != 100 {
		t.Fatal("earlier task mutated", historical, e)
	}
	history, count, e := s.ListBillingGenerationTasks(ctx, site, kind, 20, 0)
	if e != nil || count < 4 || len(history) < 4 {
		t.Fatal("submissions collapsed", count, e)
	}
	for _, item := range history {
		if len(item.SubjectIDs) == 0 {
			t.Fatal("missing task members", item)
		}
	}
	// A verified empty replacement removes the old bill without creating a zero bill.
	if err = s.PutBillingAutomaticTargets(ctx, []billing.AutomaticTarget{target}); err != nil {
		t.Fatal(err)
	}
	for day := from; day.Before(to); day = day.AddDate(0, 0, 1) {
		v := target
		v.From = day
		v.To = day.AddDate(0, 0, 1)
		if err = s.RecordBillingDayActivity(ctx, v, false); err != nil {
			t.Fatal(err)
		}
	}
	emptyReplacement := month
	emptyReplacement.ID += "z"
	emptyReplacement.RequestKey += "z"
	emptyReplacement.CreatedAt = time.Now().UTC()
	if err = s.CreateBillingStatementJob(ctx, emptyReplacement, nil, ""); !errors.Is(err, billing.ErrStatementNoData) {
		t.Fatal("empty replacement invoice", err)
	}
	p, err = s.BillingGenerationProgress(ctx, target)
	if err != nil || p.Outcome != "no_consumption" {
		t.Fatal("empty replacement progress", p, err)
	}
	previous, err = s.BillingJob(ctx, month.ID)
	if err != nil || previous.Status != "superseded" {
		t.Fatal("stale monthly invoice remained", previous.Status, err)
	}

	// Cleaning up an independent failed temporary job must not erase its task record.
	temporary := daily
	temporary.ID += "tmp"
	temporary.RequestKey += "tmp"
	temporary.BillPeriod = "temporary"
	temporary.Status = "failed"
	temporary.CreatedAt = time.Now().UTC()
	temporary.UpdatedAt = temporary.CreatedAt
	if err = s.CreateBillingStatementJob(ctx, temporary, nil, ""); err != nil {
		t.Fatal(err)
	}
	if err = s.DeleteFailedBillingJob(ctx, temporary.ID); err != nil {
		t.Fatal(err)
	}
	savedTask, e := s.BillingGenerationTask(ctx, site, "job:"+temporary.ID)
	if e != nil || savedTask.Outcome != "failed" || len(savedTask.SubjectIDs) != 1 {
		t.Fatal("failed task history lost", savedTask, e)
	}

}
