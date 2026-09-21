package mysqlstore

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	af "controltower/internal/archivecontract"
	ac "controltower/internal/archivecontrol"
)

func TestArchiveAutoPrepareAdoptsLegacyReconciliation(t *testing.T) {
	s, db, r := archiveFoundationTestStore(t)
	ctx := context.Background()
	st := ac.Status{AutoPrepare: true, PrepareDiscovered: true, PrepareIdentity: &r.Identity, SiteID: r.SiteID, AgentID: "upgrade", Session: strings.Repeat("c", 32), Configured: true, State: "waiting"}
	if _, err := s.PollLogArchive(ctx, r.SiteID, st); err != nil {
		t.Fatal(err)
	}
	c := ac.Default()
	c.AgentID = st.AgentID
	c.InstanceID = r.SiteID
	c.Running = true
	c.Version = 1
	c.ReconcileID = strings.Repeat("d", 32)
	c.ReconcileDate = time.Now().AddDate(0, 0, -2).Format("2006-01-02")
	encoded, _ := json.Marshal(c)
	if _, err := db.Exec(`UPDATE site_log_archive_control SET config_json=? WHERE site_id=?`, string(encoded), r.SiteID); err != nil {
		t.Fatal(err)
	}
	out, err := s.PollLogArchive(ctx, r.SiteID, st)
	if err != nil || out.Prepare == nil {
		t.Fatalf("legacy reconciliation blocked automatic upgrade: %+v %v", out, err)
	}
	st.Prepared = &r
	st.PreparedToken = out.PrepareToken
	out, err = s.PollLogArchive(ctx, r.SiteID, st)
	if err != nil || !out.PreparedAccepted || !out.Config.FullHistory || out.Config.ReconcileID != "" || out.Config.ReconcileDate != "" || !out.Config.Validate() {
		t.Fatalf("legacy control not converted: %+v %v", out, err)
	}
}

func TestArchiveAutoPrepareCapabilityDoesNotSurviveDowngrade(t *testing.T) {
	s, db, r, st, _ := archiveWriterControlTestStore(t)
	ctx := context.Background()
	st.Foundation = nil
	st.AutoPrepare = true
	if _, err := s.PollLogArchive(ctx, r.SiteID, st); err != nil {
		t.Fatal(err)
	}
	st.AutoPrepare = false
	if _, err := s.PollLogArchive(ctx, r.SiteID, st); err != nil {
		t.Fatal(err)
	}
	var automatic bool
	if err := db.QueryRow(`SELECT auto_prepare FROM log_archive_targets WHERE instance_id=? AND agent_id=?`, r.SiteID, st.AgentID).Scan(&automatic); err != nil {
		t.Fatal(err)
	}
	if automatic {
		t.Fatal("legacy agent retained automatic preparation capability")
	}
}

func TestArchiveAutoPrepareDoesNotAbandonRunningVersionedTasks(t *testing.T) {
	s, db, r, st, _, now := archiveBackfillTestStore(t)
	ctx := context.Background()
	archiveBackfillConfirm(t, s, r, now, true)
	task, err := s.CreateArchiveBackfill(ctx, r.SiteID, r.DatasetID, ac.BackfillRequest{RequestID: strings.Repeat("1", 32), Date: now.AddDate(0, 0, -2).Format("2006-01-02")}, "tester")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(`UPDATE archive_tasks SET status='running' WHERE task_id=?`, archiveIDBytes(task.TaskID)); err != nil {
		t.Fatal(err)
	}
	st.AutoPrepare = true
	st.Foundation = nil
	st.PrepareIdentity = &r.Identity
	st.PrepareDiscovered = true
	out, err := s.PollLogArchive(ctx, r.SiteID, st)
	if err != nil || out.PrepareError != "archive_prepare_tasks_pending" || out.Prepare != nil || out.Config.FullHistory {
		t.Fatalf("unfinished task bypassed: %+v %v", out, err)
	}
	var state string
	var pending []byte
	if err = db.QueryRow(`SELECT status FROM archive_tasks WHERE task_id=?`, archiveIDBytes(task.TaskID)).Scan(&state); err != nil || state != "running" {
		t.Fatal("old task was reset")
	}
	if err = db.QueryRow(`SELECT prepare_json FROM site_log_archive_control WHERE site_id=?`, r.SiteID).Scan(&pending); err != nil || len(pending) > 0 {
		t.Fatal("blocked old task with a preparation reservation")
	}
	items, err := s.ListLogArchives(ctx, r.SiteID)
	if err != nil || items[0].Status.Error != "archive_prepare_tasks_pending" || items[0].Status.PreparePhase != "failed" {
		t.Fatalf("failure not visible: %+v %v", items, err)
	}
	if _, err = db.Exec(`UPDATE archive_tasks SET status='succeeded' WHERE task_id=?`, archiveIDBytes(task.TaskID)); err != nil {
		t.Fatal(err)
	}
	out, err = s.PollLogArchive(ctx, r.SiteID, st)
	if err != nil || out.Prepare == nil {
		t.Fatalf("completed task still blocks preparation: %+v %v", out, err)
	}
}

func TestArchiveAutoPrepareShowsSourceConflictWithoutLosingHeartbeat(t *testing.T) {
	s, _, r, st, _ := archiveWriterControlTestStore(t)
	ctx := context.Background()
	st.AutoPrepare = true
	st.Foundation = nil
	st.PrepareDiscovered = true
	st.PrepareIdentity = &r.Identity
	out, err := s.PollLogArchive(ctx, r.SiteID, st)
	if err != nil || out.Prepare == nil {
		t.Fatalf("prepare: %+v %v", out, err)
	}
	wrong := r
	wrong.SourceFingerprint = strings.Repeat("f", 64)
	st.Prepared = &wrong
	st.PreparedToken = out.PrepareToken
	out, err = s.PollLogArchive(ctx, r.SiteID, st)
	if err != nil || out.PreparedAccepted || out.PrepareError != "archive_prepare_registration_conflict" {
		t.Fatalf("source conflict not reported: %+v %v", out, err)
	}
	items, err := s.ListLogArchives(ctx, r.SiteID)
	if err != nil || items[0].Status.Error != out.PrepareError || items[0].SeenAt == nil {
		t.Fatalf("conflict hid online status: %+v %v", items, err)
	}
}

func TestArchiveAutoPrepareDrainsLegacyAndBindsWorkflow(t *testing.T) {
	s, db, r := archiveFoundationTestStore(t)
	ctx := context.Background()
	legacy := ac.Status{SiteID: r.SiteID, AgentID: "upgrade-agent", Session: strings.Repeat("c", 32), Configured: true, State: "paused"}
	if _, err := s.PollLogArchive(ctx, r.SiteID, legacy); err != nil {
		t.Fatal(err)
	}
	c := ac.Default()
	c.InstanceID = r.SiteID
	c.AgentID = legacy.AgentID
	c.Running = true
	if err := s.UpdateLogArchive(ctx, r.SiteID, c, "admin"); err != nil {
		t.Fatal(err)
	}
	out, err := s.PollLogArchive(ctx, r.SiteID, legacy)
	if err != nil || !out.Granted {
		t.Fatalf("legacy: %+v %v", out, err)
	}
	st := legacy
	st.AutoPrepare = true
	st.PrepareDiscovered = true
	st.Session = strings.Repeat("d", 32)
	out, err = s.PollLogArchive(ctx, r.SiteID, st)
	if err != nil || out.Prepare != nil || out.Granted {
		t.Fatalf("must drain: %+v %v", out, err)
	}
	out, err = s.PollLogArchive(ctx, r.SiteID, legacy)
	if err != nil || out.Granted {
		t.Fatalf("renewed old lease: %+v %v", out, err)
	}
	if _, err = db.Exec(`UPDATE site_log_archive_control SET lease_until=UTC_TIMESTAMP()-INTERVAL 1 SECOND WHERE site_id=?`, r.SiteID); err != nil {
		t.Fatal(err)
	}
	out, err = s.PollLogArchive(ctx, r.SiteID, st)
	if err != nil || out.Prepare == nil || out.Granted {
		t.Fatalf("prepare grant: %+v %v", out, err)
	}
	i := *out.Prepare
	t.Cleanup(func() { _, _ = db.Exec(`DELETE FROM archive_datasets WHERE dataset_id=?`, archiveIDBytes(i.DatasetID)) })
	st.State = "error"
	st.Error = "archive_prepare_failed_check_connection_permissions_or_writer_lease"
	out, err = s.PollLogArchive(ctx, r.SiteID, st)
	if err != nil || out.Prepare == nil || !out.Prepare.Equal(i) {
		t.Fatalf("retry lost identity: %+v %v", out, err)
	}
	st.Prepared = &af.Registration{Identity: i, StorageRef: "agent-" + i.DatasetID, ArchiveFormatVersion: af.FormatVersion, SchemaFingerprint: r.SchemaFingerprint, SourceFingerprint: r.SourceFingerprint}
	st.PreparedToken = out.PrepareToken
	st.PrepareIdentity = &i
	st.Error = ""
	st.State = "waiting"
	_, err = db.Exec(`UPDATE site_log_archive_control SET lease_until=UTC_TIMESTAMP()-INTERVAL 1 SECOND WHERE site_id=?`, r.SiteID)
	if err != nil {
		t.Fatal(err)
	}
	out, err = s.PollLogArchive(ctx, r.SiteID, st)
	if err != nil || out.PreparedAccepted || out.Prepare == nil || out.PrepareToken == st.PreparedToken || !out.Prepare.Equal(i) {
		t.Fatalf("stale preparation receipt accepted: %+v %v", out, err)
	}
	st.PreparedToken = out.PrepareToken // Agent rechecks the target under the fresh authorization.
	out, err = s.PollLogArchive(ctx, r.SiteID, st)
	if err != nil || !out.PreparedAccepted || !out.Config.FullHistory || out.Granted {
		t.Fatalf("bind: %+v %v", out, err)
	}
	version := out.Config.Version
	out, err = s.PollLogArchive(ctx, r.SiteID, st)
	if err != nil || !out.PreparedAccepted || out.Config.Version != version {
		t.Fatalf("lost ack: %+v %v", out, err)
	}
	st.Prepared = nil
	st.PrepareIdentity = nil
	st.AppliedVersion = version
	st.Foundation = &af.FoundationStatus{Identity: i, ProtocolVersion: af.ProtocolVersion, FormatVersion: af.FormatVersion, Capabilities: []string{af.CapabilityFoundation, af.CapabilityAtomicWriter, af.CapabilityBackfill, af.CapabilityReconcile, af.CapabilitySeal, af.CapabilityWorkflow}}
	out, err = s.PollLogArchive(ctx, r.SiteID, st)
	if err != nil || !out.Granted || out.WriterGrant == nil {
		t.Fatalf("workflow grant: %+v %v", out, err)
	}
	old, err := s.PollLogArchive(ctx, r.SiteID, legacy)
	if err != nil || old.Granted {
		t.Fatalf("legacy after binding: %+v %v", old, err)
	}
	// A process restart drains the prior writer and keeps the active identity.
	st.Foundation = nil
	st.Session = strings.Repeat("e", 32)
	st.PrepareIdentity = &i
	out, err = s.PollLogArchive(ctx, r.SiteID, st)
	if err != nil || out.Prepare != nil {
		t.Fatalf("restart drain: %+v %v", out, err)
	}
	_, err = db.Exec(`UPDATE site_log_archive_control SET lease_until=UTC_TIMESTAMP()-INTERVAL 1 SECOND WHERE site_id=?`, r.SiteID)
	if err != nil {
		t.Fatal(err)
	}
	out, err = s.PollLogArchive(ctx, r.SiteID, st)
	if err != nil || out.Prepare == nil || !out.Prepare.Equal(i) {
		t.Fatalf("restart changed binding: %+v %v", out, err)
	}
	c = out.Config
	c.Running = false
	if err = s.UpdateLogArchive(ctx, r.SiteID, c, "admin"); err != nil {
		t.Fatal(err)
	}
	out, err = s.PollLogArchive(ctx, r.SiteID, st)
	if err != nil || out.Prepare != nil {
		t.Fatalf("pause after restart: %+v %v", out, err)
	}
	c = out.Config
	c.Running = true
	if err = s.UpdateLogArchive(ctx, r.SiteID, c, "admin"); err != nil {
		t.Fatalf("automatic target cannot resume before foundation report: %v", err)
	}
}

func TestArchiveAutoPrepareWaitsForDiscoveryAndRejectsWrongSite(t *testing.T) {
	s, db, r := archiveFoundationTestStore(t)
	ctx := context.Background()
	st := ac.Status{AutoPrepare: true, AgentID: "auto", Session: strings.Repeat("c", 32), Configured: true, State: "paused"}
	if _, err := s.PollLogArchive(ctx, r.SiteID, st); err != nil {
		t.Fatal(err)
	}
	c := ac.Default()
	c.InstanceID = r.SiteID
	c.AgentID = st.AgentID
	c.Running = true
	if err := s.UpdateLogArchive(ctx, r.SiteID, c, "admin"); err != nil {
		t.Fatal(err)
	}
	out, err := s.PollLogArchive(ctx, r.SiteID, st)
	if err != nil || out.Prepare != nil {
		t.Fatalf("allocated before discovery: %+v %v", out, err)
	}
	st.PrepareDiscovered = true
	wrong := r.Identity
	wrong.SiteID = "other"
	st.PrepareIdentity = &wrong
	if rejected, e := s.PollLogArchive(ctx, r.SiteID, st); e != nil || rejected.PrepareError != "archive_prepare_identity_mismatch" || rejected.Prepare != nil {
		t.Fatalf("identity mismatch was not surfaced: %+v %v", rejected, e)
	}
	st.PrepareIdentity = &r.Identity
	out, err = s.PollLogArchive(ctx, r.SiteID, st)
	if err != nil || out.Prepare == nil || !out.Prepare.Equal(r.Identity) {
		t.Fatalf("existing target identity not reused: %+v %v", out, err)
	}
	c = out.Config
	c.Running = false
	if err = s.UpdateLogArchive(ctx, r.SiteID, c, "admin"); err != nil {
		t.Fatal(err)
	}
	out, err = s.PollLogArchive(ctx, r.SiteID, st)
	if err != nil || out.Prepare != nil || out.Granted {
		t.Fatalf("paused prepare: %+v %v", out, err)
	}
	var raw []byte
	if err = db.QueryRow(`SELECT prepare_json FROM site_log_archive_control WHERE site_id=?`, r.SiteID).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	var pending archivePreparation
	if json.Unmarshal(raw, &pending) != nil || !pending.Identity.Equal(r.Identity) {
		t.Fatal("pause discarded identity")
	}
}
