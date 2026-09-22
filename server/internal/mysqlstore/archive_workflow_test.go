package mysqlstore

import (
	"context"
	af "controltower/internal/archivecontract"
	"testing"
	"time"
)

func TestArchiveWorkflowControlMySQL(t *testing.T) {
	s, _, r, st, c := archiveWriterControlTestStore(t)
	ctx := context.Background()
	c.FullHistory = true
	if err := s.UpdateLogArchive(ctx, r.SiteID, c, "tester"); err == nil {
		t.Fatal("old Agent accepted full history operation")
	}
	st.Foundation.Capabilities = append(st.Foundation.Capabilities, af.CapabilityBackfill, af.CapabilityReconcile, af.CapabilitySeal, af.CapabilityWorkflow)
	if _, err := s.PollLogArchive(ctx, r.SiteID, st); err != nil {
		t.Fatal(err)
	}
	if err := s.UpdateLogArchive(ctx, r.SiteID, c, "tester"); err != nil {
		t.Fatal(err)
	}
	c.Version++
	st.AppliedVersion = c.Version
	out, err := s.PollLogArchive(ctx, r.SiteID, st)
	if err != nil || !out.Granted || out.WriterGrant == nil {
		t.Fatalf("grant=%+v err=%v", out, err)
	}
	st.Foundation.WriterEpoch = out.WriterGrant.WriterEpoch
	now := time.Now().UTC()
	st.Operation = &af.Operation{Phase: "import_target", Code: "rebuild_daily_statistics", Database: "archive", Table: "log_daily_stats", State: "failed", StartedAt: now, FinishedAt: &now}
	st.Diagnostic = &af.Diagnostic{Code: "database_permission_denied", MySQLNumber: 1142, SQLState: "42000", Operation: st.Operation, OccurredAt: now}
	st.Workflow = &af.WorkflowStatus{Phase: "import_target", ImportedRows: 123, Preparation: &af.WorkflowPreparationProgress{Phase: "import_target", Table: "logs_202609", ProcessedRows: 123, LastBatchRows: 23, CommittedBatches: 2, RecordedSince: now, LastCommittedAt: now, AfterID: 9007199254740993}}
	out, err = s.PollLogArchive(ctx, r.SiteID, st)
	if err != nil || !out.StatusAccepted || out.BackfillTask != nil || out.ReconcileTask != nil || out.SealTask != nil {
		t.Fatalf("workflow competed with standalone queues: %+v %v", out, err)
	}
	items, err := s.ListLogArchives(ctx, r.SiteID)
	if err != nil || len(items) != 1 || items[0].Status.Workflow == nil || items[0].Status.Workflow.ImportedRows != 123 {
		t.Fatalf("progress not visible: %+v %v", items, err)
	}
	p := items[0].Status.Workflow.Preparation
	if d := items[0].Status.Diagnostic; d == nil || d.MySQLNumber != 1142 || d.Operation.Table != "log_daily_stats" || items[0].Status.Operation.State != "failed" {
		t.Fatalf("execution diagnostic lost: %+v", d)
	}
	if p == nil || p.ProcessedRows != 123 || p.LastBatchRows != 23 || p.AfterID != 9007199254740993 || !p.LastCommittedAt.Equal(now) {
		t.Fatalf("preparation progress lost through control/report storage: %+v", p)
	}
	c.Running = false
	if err = s.UpdateLogArchive(ctx, r.SiteID, c, "tester"); err != nil {
		t.Fatal(err)
	}
	c.Version++
	st.AppliedVersion = c.Version
	st.State = "paused"
	out, err = s.PollLogArchive(ctx, r.SiteID, st)
	if err != nil || out.Granted {
		t.Fatalf("paused workflow received grant: %+v %v", out, err)
	}
	c.FullHistory = false
	if s.UpdateLogArchive(ctx, r.SiteID, c, "tester") == nil {
		t.Fatal("workflow switched back to legacy midway")
	}
}
