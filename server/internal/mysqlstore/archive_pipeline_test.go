package mysqlstore

import (
	"context"
	af "controltower/internal/archivecontract"
	ap "controltower/internal/archivepipeline"
	"testing"
	"time"
)

func TestArchivePipelineCapabilityAndIndependentControlMySQL(t *testing.T) {
	s, _, r, st, c := archiveWriterControlTestStore(t)
	ctx := context.Background()
	c.FullHistory = true
	c.Pipeline = &ap.Settings{Migration: true, Organization: true, Verification: true, Collection: true}
	if err := s.UpdateLogArchive(ctx, r.SiteID, c, "test"); err == nil {
		t.Fatal("old/running agent accepted new mode")
	}
	c.Pipeline = nil
	c.Running = false
	st.Foundation.Capabilities = append(st.Foundation.Capabilities, af.CapabilityBackfill, af.CapabilityReconcile, af.CapabilitySeal, af.CapabilityWorkflow, af.CapabilityPipeline)
	if _, err := s.PollLogArchive(ctx, r.SiteID, st); err != nil {
		t.Fatal(err)
	}
	if err := s.UpdateLogArchive(ctx, r.SiteID, c, "test"); err != nil {
		t.Fatal(err)
	}
	c.Version++
	st.AppliedVersion = c.Version
	st.State = "paused"
	if _, err := s.PollLogArchive(ctx, r.SiteID, st); err != nil {
		t.Fatal(err)
	}
	c.Pipeline = &ap.Settings{Migration: true, Organization: true, Verification: false, Collection: true}
	c.Running = true
	if err := s.UpdateLogArchive(ctx, r.SiteID, c, "test"); err != nil {
		t.Fatal(err)
	}
	c.Version++
	st.AppliedVersion = c.Version
	out, err := s.PollLogArchive(ctx, r.SiteID, st)
	if err != nil || out.WriterGrant == nil || out.Config.Pipeline == nil || out.Config.Pipeline.Verification {
		t.Fatal(out, err)
	}
	st.Foundation.WriterEpoch = out.WriterGrant.WriterEpoch
	st.Pipeline = &ap.Status{MigrationDone: true, Cutoff: "2026-09-21", Settings: *c.Pipeline, Active: map[ap.Task]ap.Work{}, Progress: map[ap.Task]ap.Progress{ap.Collection: {AfterID: 9007199254740993, Rows: 5, UpdatedAt: time.Now().UTC()}}}
	if out, err = s.PollLogArchive(ctx, r.SiteID, st); err != nil || !out.StatusAccepted {
		t.Fatal(out, err)
	}
	items, err := s.ListLogArchives(ctx, r.SiteID)
	if err != nil || len(items) != 1 || items[0].Status.Pipeline == nil || items[0].Status.Pipeline.Progress[ap.Collection].AfterID != 9007199254740993 {
		t.Fatal(items, err)
	}
	c.Pipeline.CollectionFrom = "2026-09-01"
	c.Pipeline.CollectionThrough = "2026-09-02"
	if err = s.UpdateLogArchive(ctx, r.SiteID, c, "test"); err == nil {
		t.Fatal("range changed before pause acknowledged")
	}
	c.Pipeline = nil
	if err = s.UpdateLogArchive(ctx, r.SiteID, c, "test"); err == nil {
		t.Fatal("new task state silently discarded")
	}
}
