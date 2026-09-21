package mysqlstore

import (
	"context"
	af "controltower/internal/archivecontract"
	"testing"
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
	st.Workflow = &af.WorkflowStatus{Phase: "import_target", ImportedRows: 123}
	out, err = s.PollLogArchive(ctx, r.SiteID, st)
	if err != nil || !out.StatusAccepted || out.BackfillTask != nil || out.ReconcileTask != nil || out.SealTask != nil {
		t.Fatalf("workflow competed with standalone queues: %+v %v", out, err)
	}
	items, err := s.ListLogArchives(ctx, r.SiteID)
	if err != nil || len(items) != 1 || items[0].Status.Workflow == nil || items[0].Status.Workflow.ImportedRows != 123 {
		t.Fatalf("progress not visible: %+v %v", items, err)
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
