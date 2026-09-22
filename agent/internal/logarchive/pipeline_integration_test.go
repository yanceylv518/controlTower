package logarchive

import (
	af "controltower/internal/archivecontract"
	ap "controltower/internal/archivepipeline"
	"database/sql"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestPipelineSelectedRangeAndResumeMySQL(t *testing.T) {
	w, ctx, g, _ := scanFixture(t)
	first := time.Now().In(archiveLocation).AddDate(0, 0, -4).Format("2006-01-02")
	from, _, _ := af.DateBounds(first)
	next := time.Unix(from+86400, 0).In(archiveLocation).Format("2006-01-02")
	scanRow(t, w, ctx, 1, from+1, `{"model_ratio":1}`)
	scanRow(t, w, ctx, 2, from+86401, `{"model_ratio":1}`)
	settings := ap.Settings{Migration: true, Collection: true, CollectionFrom: first, CollectionThrough: next, CollectionNewestFirst: true}
	var status *ap.Status
	for i := 0; i < 15; i++ {
		var err error
		status, err = w.PipelinePass(ctx, g, 90*time.Second, settings, true)
		if err != nil {
			t.Fatal(err)
		}
		if len(status.Errors) > 0 {
			t.Fatalf("range errors: %+v", status)
		}
		if status.CollectionDone {
			break
		}
	}
	if !status.CollectionDone {
		t.Fatal("range never completed", status)
	}
	if n := writerTestScalar(t, ctx, w.target, `SELECT COUNT(*) FROM archive_raw_state`); n != 2 {
		t.Fatal("range rows", n)
	}
	if n := writerTestScalar(t, ctx, w.target, `SELECT after_id FROM archive_checkpoints WHERE stream_key='incremental'`); n != 0 {
		t.Fatal("range changed continuous cursor", n)
	}
	if n := writerTestScalar(t, ctx, w.target, `SELECT COUNT(*) FROM archive_log_state`); n != 0 {
		t.Fatal("range updated derived ledger", n)
	}
	// Reopening the continuous stream adopts identical raw rows without creating
	// false revisions, statistics or a second migration cleanup.
	settings.CollectionFrom = ""
	settings.CollectionThrough = ""
	settings.CollectionNewestFirst = false
	if _, err := w.PipelinePass(ctx, g, 90*time.Second, settings, true); err != nil {
		t.Fatal(err)
	}
	status, err := w.PipelineProgress(ctx)
	if err != nil || !status.MigrationDone {
		t.Fatal(status, err)
	}
	if n := writerTestScalar(t, ctx, w.target, `SELECT after_id FROM archive_checkpoints WHERE stream_key='incremental'`); n != 2 {
		t.Fatal("continuous did not resume", n)
	}
}

func TestPipelineSourceExclusionAndPausedVerificationMySQL(t *testing.T) {
	w, ctx, g, _ := scanFixture(t)
	day := time.Now().In(archiveLocation).AddDate(0, 0, -3).Format("2006-01-02")
	from, _, _ := af.DateBounds(day)
	scanRow(t, w, ctx, 1, from+1, `{"model_ratio":1}`)
	scanRow(t, w, ctx, 2, from+86401, `{"model_ratio":1}`)
	settings := ap.Settings{Migration: true, Organization: true, Verification: true, Collection: true}
	var status *ap.Status
	for i := 0; i < 40; i++ {
		var err error
		status, err = w.PipelinePass(ctx, g, 90*time.Second, settings, true)
		if err != nil {
			t.Fatal(err)
		}
		if _, ok := status.Active[ap.Verification]; ok {
			break
		}
	}
	if _, ok := status.Active[ap.Verification]; !ok {
		t.Fatal("verifier not reserved", status)
	}
	scanRow(t, w, ctx, 3, from+86402, `{"model_ratio":1}`)
	if _, err := w.PipelinePass(ctx, g, 90*time.Second, settings, true); err != nil {
		t.Fatal(err)
	}
	if n := writerTestScalar(t, ctx, w.target, `SELECT after_id FROM archive_checkpoints WHERE stream_key='incremental'`); n != 2 {
		t.Fatal("collector overlapped verifier", n)
	}
	settings.Verification = false
	if _, err := w.PipelinePass(ctx, g, 90*time.Second, settings, true); err != nil {
		t.Fatal(err)
	}
	if n := writerTestScalar(t, ctx, w.target, `SELECT after_id FROM archive_checkpoints WHERE stream_key='incremental'`); n != 3 {
		t.Fatal("paused verifier did not release collection", n)
	}
	// A persisted checkpoint survives a fresh worker's raw inventory cache.
	w.rawInventory = rawInventoryCache{}
	status, err := w.PipelineProgress(ctx)
	if err != nil || !status.MigrationDone {
		t.Fatal(status, err)
	}
}

func TestPipelineDateFailureContinuesAndRetryMySQL(t *testing.T) {
	w, ctx, g, _ := scanFixture(t)
	day := time.Now().In(archiveLocation).AddDate(0, 0, -4).Format("2006-01-02")
	from, _, _ := af.DateBounds(day)
	for i := 0; i < 3; i++ {
		scanRow(t, w, ctx, int64(i+1), from+int64(i)*86400+1, `{"model_ratio":1}`)
	}
	settings := ap.Settings{Migration: true, Organization: true, Verification: true, Collection: true}
	for i := 0; i < 80; i++ {
		status, err := w.PipelinePass(ctx, g, 90*time.Second, settings, false)
		if err != nil {
			t.Fatal(err)
		}
		if len(status.Errors) > 0 {
			t.Fatalf("infrastructure failure %+v", status)
		}
	}
	var raw []byte
	if err := w.target.QueryRowContext(ctx, `SELECT state_json FROM archive_pipeline WHERE singleton_id=1`).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	var checkpoint pipelineCheckpoint
	if err := json.Unmarshal(raw, &checkpoint); err != nil {
		t.Fatal(err)
	}
	blocked := 0
	for _, d := range checkpoint.State.Days {
		if d.Result != nil && d.Result.Code != "" {
			blocked++
		}
	}
	if blocked != 2 {
		t.Fatalf("date failure stopped later date: %d %+v", blocked, checkpoint.State)
	}
	settings.RetryToken = strings.Repeat("e", 32)
	settings.Collection = false
	settings.Organization = false
	settings.Verification = false
	if _, err := w.PipelinePass(ctx, g, 90*time.Second, settings, false); err != nil {
		t.Fatal(err)
	}
	if err := w.pipelineTransaction(ctx, g, func(tx *sql.Tx, p *pipelineCheckpoint) error {
		for _, d := range p.State.Days {
			if d.Result != nil {
				t.Fatal("retry retained failed result")
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func TestPipelineRawMoveRepairsOldDateStatisticsMySQL(t *testing.T) {
	w, ctx, g, _ := scanFixture(t)
	day := time.Now().In(archiveLocation).AddDate(0, 0, -4).Format("2006-01-02")
	from, _, _ := af.DateBounds(day)
	next := time.Unix(from+86400, 0).In(archiveLocation).Format("2006-01-02")
	scanRow(t, w, ctx, 1, from+1, `{"model_ratio":1}`)
	scanRow(t, w, ctx, 2, from+86401, `{"model_ratio":1}`)
	settings := ap.Settings{Migration: true, Organization: true, Collection: true}
	for i := 0; i < 20; i++ {
		if _, err := w.PipelinePass(ctx, g, 90*time.Second, settings, true); err != nil {
			t.Fatal(err)
		}
	}
	if n := writerTestScalar(t, ctx, w.target, `SELECT log_rows FROM log_daily_stats WHERE period_key=?`, day); n != 1 {
		t.Fatal("initial statistics", n)
	}
	if _, err := w.source.ExecContext(ctx, `UPDATE logs SET created_at=? WHERE id=1`, from+86402); err != nil {
		t.Fatal(err)
	}
	settings.CollectionFrom = next
	settings.CollectionThrough = next
	for i := 0; i < 20; i++ {
		status, err := w.PipelinePass(ctx, g, 90*time.Second, settings, true)
		if err != nil || len(status.Errors) > 0 {
			t.Fatal(status, err)
		}
	}
	if n := writerTestScalar(t, ctx, w.target, `SELECT COALESCE(SUM(log_rows),0) FROM log_daily_stats WHERE period_key=?`, day); n != 0 {
		t.Fatal("moved-out row left old contribution", n)
	}
	if n := writerTestScalar(t, ctx, w.target, `SELECT COUNT(*) FROM archive_pending_statistics WHERE log_date=?`, day); n != 0 {
		t.Fatal("old-date pending queue not drained", n)
	}
}

func TestPipelineRawCollectionAndDailyProcessingMySQL(t *testing.T) {
	w, ctx, g, _ := scanFixture(t)
	w.SetBatchSize(2)
	day := time.Now().In(archiveLocation).AddDate(0, 0, -3).Format("2006-01-02")
	from, _, _ := af.DateBounds(day)
	scanRow(t, w, ctx, 1, from+1, `{"model_ratio":1}`)
	scanRow(t, w, ctx, 2, from+2, `{"model_ratio":1}`)
	scanRow(t, w, ctx, 3, from+86400+1, `{"model_ratio":1}`)
	settings := ap.Settings{Migration: true, Collection: true}
	for i := 0; i < 6; i++ {
		if _, err := w.PipelinePass(ctx, g, 90*time.Second, settings, true); err != nil {
			t.Fatal(err)
		}
	}
	month := strings.ReplaceAll(day[:7], "-", "")
	if n := writerTestScalar(t, ctx, w.target, "SELECT COUNT(*) FROM logs_"+month); n < 2 {
		t.Fatal("raw collection missing", n)
	}
	if n := writerTestScalar(t, ctx, w.target, "SELECT COUNT(*) FROM archive_log_state"); n != 0 {
		t.Fatal("collection updated statistics", n)
	}
	settings.Organization = true
	settings.Verification = true
	for i := 0; i < 100; i++ {
		status, err := w.PipelinePass(ctx, g, 90*time.Second, settings, true)
		if err != nil {
			t.Fatal(err)
		}
		if len(status.Errors) > 0 {
			t.Fatalf("pass %d: %+v", i, status.Errors)
		}
		if n := writerTestScalar(t, ctx, w.target, `SELECT COUNT(*) FROM archive_workflow_days WHERE state='sealed'`); n > 0 {
			break
		}
	}
	if n := writerTestScalar(t, ctx, w.target, `SELECT COUNT(*) FROM archive_workflow_days WHERE state='sealed'`); n == 0 {
		t.Fatal("no day sealed")
	}
	if n := writerTestScalar(t, ctx, w.target, `SELECT log_rows FROM log_daily_stats WHERE period_key=?`, day); n != 2 {
		t.Fatal("wrong organized count", n)
	}
	// A higher ID with an old timestamp is a real late arrival.
	scanRow(t, w, ctx, 4, from+3, `{"model_ratio":1}`)
	settings.Organization = false
	settings.Verification = false
	if _, err := w.PipelinePass(ctx, g, 90*time.Second, settings, true); err != nil {
		t.Fatal(err)
	}
	var raw []byte
	if err := w.target.QueryRowContext(ctx, `SELECT state_json FROM archive_pipeline WHERE singleton_id=1`).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), `"last_seal"`) {
		t.Fatal("old version lost")
	}
	// Calendar cannot continue showing the old sealed revision after mutation.
	page, err := w.WorkflowDayPage(ctx, "")
	if err != nil {
		t.Fatal(err)
	}
	for _, d := range page.Days {
		if d.Date == day && d.State == "sealed" {
			t.Fatal("stale seal displayed")
		}
	}
}

func TestRawInventoryIgnoresDerivedStatisticsMySQL(t *testing.T) {
	w, ctx, g, _ := scanFixture(t)
	month := "202609"
	if err := ensureMonthlyTables(ctx, w.target, []string{month}); err != nil {
		t.Fatal(err)
	}
	if _, err := w.target.ExecContext(ctx, `ALTER TABLE logs_202609 ADD INDEX idx_raw_created(created_at)`); err != nil {
		t.Fatal(err)
	}
	from, _, _ := af.DateBounds("2026-09-01")
	if _, err := w.target.ExecContext(ctx, `INSERT INTO logs_202609(id,created_at,type,quota,other) VALUES(1,?,2,5,'{}'),(2,?,2,5,'{}')`, from, from+1); err != nil {
		t.Fatal(err)
	}
	state := workflowState{TaskID: strings.Repeat("d", 32), WorkflowStatus: af.WorkflowStatus{Phase: "import_target"}}
	if err := w.commitWorkflow(ctx, g, state); err != nil {
		t.Fatal(err)
	}
	page, err := w.WorkflowDayPage(ctx, "")
	if err != nil {
		t.Fatal(err)
	}
	for _, d := range page.Days {
		if d.Date == "2026-09-01" {
			if d.Raw == nil || d.Raw.Rows == nil || *d.Raw.Rows != "2" || d.Counts != nil {
				t.Fatalf("bad raw count: %+v", d)
			}
			return
		}
	}
	t.Fatal("physical date absent without derived statistics")
}
