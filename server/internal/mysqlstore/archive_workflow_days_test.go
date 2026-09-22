package mysqlstore

import (
	"context"
	af "controltower/internal/archivecontract"
	"strings"
	"testing"
	"time"
)

func TestWorkflowDailyReportsMySQL(t *testing.T) {
	s, db, r, st, _ := archiveWriterControlTestStore(t)
	ctx := context.Background()
	st.Foundation.Capabilities = append(st.Foundation.Capabilities, af.CapabilityBackfill, af.CapabilityReconcile, af.CapabilitySeal, af.CapabilityWorkflow)
	out, err := s.PollLogArchive(ctx, r.SiteID, st)
	if err != nil || out.WriterGrant == nil {
		t.Fatal(out, err)
	}
	st.Foundation.WriterEpoch = out.WriterGrant.WriterEpoch
	now := time.Now().UTC().Truncate(time.Microsecond)
	st.WorkflowDaily = &af.WorkflowDayPage{TaskID: strings.Repeat("d", 32), ObservedAt: now, Days: []af.WorkflowDay{{Date: "2026-09-01", State: "rebuilding", ObservedAt: now, Counts: &af.WorkflowDayCounts{LogRows: "9007199254740993", RequestRows: "1", ErrorRows: "0"}}}}
	if out, err = s.PollLogArchive(ctx, r.SiteID, st); err != nil || !out.StatusAccepted {
		t.Fatal(out, err)
	}
	read := func(want string) {
		t.Helper()
		days, e := s.ListArchiveWorkflowDays(ctx, r.SiteID, "2026-09")
		if e != nil || len(days) != 1 || days[0].Counts.LogRows != want {
			t.Fatalf("daily reports: %+v %v", days, e)
		}
	}
	read("9007199254740993")
	rawRows := "123456789"
	st.WorkflowDaily.Days[0].Raw = &af.RawDayCount{Rows: &rawRows, ObservedAt: &now}
	if out, err = s.PollLogArchive(ctx, r.SiteID, st); err != nil || !out.StatusAccepted {
		t.Fatal(out, err)
	}
	rawDays, e := s.ListArchiveWorkflowDays(ctx, r.SiteID, "2026-09")
	if e != nil || len(rawDays) != 1 || rawDays[0].Raw == nil || *rawDays[0].Raw.Rows != rawRows {
		t.Fatal("raw count lost", rawDays, e)
	}
	st.WorkflowDaily.Days[0].Raw = nil
	// Stale snapshots cannot overwrite newer daily counts.
	st.WorkflowDaily.ObservedAt = now.Add(-time.Minute)
	st.WorkflowDaily.Days[0].ObservedAt = st.WorkflowDaily.ObservedAt
	st.WorkflowDaily.Days[0].Counts.LogRows = "1"
	if _, err = s.PollLogArchive(ctx, r.SiteID, st); err != nil {
		t.Fatal(err)
	}
	read("9007199254740993")
	if days, e := s.ListArchiveWorkflowDays(ctx, r.SiteID, "2026-08"); e != nil || len(days) != 0 {
		t.Fatal("month leakage", days, e)
	}
	if days, e := s.ListArchiveWorkflowDays(ctx, "another-site", "2026-09"); e != nil || len(days) != 0 {
		t.Fatal("site leakage", days, e)
	}
	// Neither an unselected executor nor a stale writer epoch can inject data.
	other := copyArchiveWriterStatus(st)
	other.AgentID = "other-agent"
	if _, err = s.PollLogArchive(ctx, r.SiteID, other); err != nil {
		t.Fatal(err)
	}
	read("9007199254740993")
	if _, err = db.Exec(`UPDATE site_log_archive_control SET writer_epoch=writer_epoch+1 WHERE site_id=?`, r.SiteID); err != nil {
		t.Fatal(err)
	}
	st.WorkflowDaily.ObservedAt = now.Add(time.Minute)
	st.WorkflowDaily.Days[0].ObservedAt = st.WorkflowDaily.ObservedAt
	if out, err = s.PollLogArchive(ctx, r.SiteID, st); err != nil || out.StatusAccepted {
		t.Fatal("stale writer accepted", out, err)
	}
	read("9007199254740993")
	// A new dataset must never inherit the previous dataset's reports.
	if _, err = db.Exec(`UPDATE site_log_archive_control SET active_dataset_id=? WHERE site_id=?`, archiveIDBytes(strings.Repeat("e", 32)), r.SiteID); err != nil {
		t.Fatal(err)
	}
	if days, e := s.ListArchiveWorkflowDays(ctx, r.SiteID, "2026-09"); e != nil || len(days) != 0 {
		t.Fatal("dataset leakage", days, e)
	}
}
