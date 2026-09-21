package mysqlstore

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	af "controltower/internal/archivecontract"
	ac "controltower/internal/archivecontrol"
)

func archiveWriterControlTestStore(t *testing.T) (Store, *sql.DB, af.Registration, ac.Status, ac.Config) {
	t.Helper()
	s, db, r := archiveFoundationTestStore(t)
	ctx := context.Background()
	if err := s.RegisterArchiveDataset(ctx, r, "tester"); err != nil {
		t.Fatal(err)
	}
	st := ac.Status{SiteID: r.SiteID, AgentID: "atomic-writer", Session: strings.Repeat("a", 32), Configured: true, State: "paused", Foundation: &af.FoundationStatus{Identity: r.Identity, ProtocolVersion: af.ProtocolVersion, FormatVersion: af.FormatVersion, Capabilities: []string{af.CapabilityFoundation, af.CapabilityAtomicWriter}}}
	out, err := s.PollLogArchive(ctx, r.SiteID, st)
	if err != nil || out.Granted {
		t.Fatalf("advertise writer: %+v %v", out, err)
	}
	c := out.Config
	c.InstanceID, c.AgentID, c.Running = r.SiteID, st.AgentID, true
	if err = s.UpdateLogArchive(ctx, r.SiteID, c, "tester"); err != nil {
		t.Fatal(err)
	}
	c.Version++
	st.AppliedVersion = c.Version
	return s, db, r, st, c
}

func copyArchiveWriterStatus(st ac.Status) ac.Status {
	foundation := *st.Foundation
	st.Foundation = &foundation
	return st
}

func archiveWriterLease(t *testing.T, db *sql.DB, site string) (uint64, string, time.Time) {
	t.Helper()
	var epoch uint64
	var session string
	var until time.Time
	if err := db.QueryRow(`SELECT writer_epoch,session_id,lease_until FROM site_log_archive_control WHERE site_id=?`, site).Scan(&epoch, &session, &until); err != nil {
		t.Fatal(err)
	}
	return epoch, session, until
}

func TestArchiveWriterLeaseRenewalPauseAndTakeover(t *testing.T) {
	s, db, r, st, c := archiveWriterControlTestStore(t)
	ctx := context.Background()
	st.AppliedVersion--
	out, err := s.PollLogArchive(ctx, r.SiteID, st)
	if err != nil || out.Granted || out.WriterGrant != nil {
		t.Fatalf("grant before config acknowledgement: %+v %v", out, err)
	}
	st.AppliedVersion = c.Version
	out, err = s.PollLogArchive(ctx, r.SiteID, st)
	if err != nil || !out.Granted || out.WriterGrant == nil || out.WriterGrant.WriterEpoch != 1 || out.WriterGrant.Validate() != nil {
		t.Fatalf("first atomic grant: %+v %v", out, err)
	}
	if dataset, e := s.GetArchiveDataset(ctx, r.SiteID, r.DatasetID); e != nil || dataset.LifecycleState != "active" {
		t.Fatalf("running dataset not active: %+v %v", dataset, e)
	}
	_, _, initialDeadline := archiveWriterLease(t, db, r.SiteID)
	if out, err = s.PollLogArchive(ctx, r.SiteID, st); err != nil || !out.Granted || out.WriterGrant.WriterEpoch != 1 {
		t.Fatalf("recover same bootstrap grant: %+v %v", out, err)
	}
	if _, _, deadline := archiveWriterLease(t, db, r.SiteID); !deadline.Equal(initialDeadline) {
		t.Fatal("bootstrap without target claim renewed lease")
	}
	if _, err = db.Exec(`UPDATE site_log_archive_control SET lease_until=UTC_TIMESTAMP(6)+INTERVAL 59 SECOND WHERE site_id=?`, r.SiteID); err != nil {
		t.Fatal(err)
	}
	if out, err = s.PollLogArchive(ctx, r.SiteID, st); err != nil || out.Granted || out.WriterGrant != nil {
		t.Fatalf("insufficient remaining bootstrap lease granted: %+v %v", out, err)
	}
	st.Foundation.WriterEpoch, st.Foundation.CatalogRevision = 1, 3
	st.Foundation.ReceiptID = strings.Repeat("e", 32)
	st.LastID, st.State = 55, "running"
	if out, err = s.PollLogArchive(ctx, r.SiteID, st); err != nil || !out.Granted || !out.StatusAccepted || out.WriterGrant.WriterEpoch != 1 || out.LeaseSeconds != 120 {
		t.Fatalf("claimed writer failed renewal: %+v %v", out, err)
	}
	_, _, activeDeadline := archiveWriterLease(t, db, r.SiteID)
	other := copyArchiveWriterStatus(st)
	other.Session, other.Foundation.WriterEpoch, other.Foundation.ReceiptID = strings.Repeat("b", 32), 0, ""
	if out, err = s.PollLogArchive(ctx, r.SiteID, other); err != nil || out.Granted || out.StatusAccepted {
		t.Fatalf("concurrent session accepted: %+v %v", out, err)
	}
	c.Running = false
	if err = s.UpdateLogArchive(ctx, r.SiteID, c, "tester"); err != nil {
		t.Fatal(err)
	}
	c.Version++
	st.AppliedVersion, st.State = c.Version, "paused"
	if out, err = s.PollLogArchive(ctx, r.SiteID, st); err != nil || out.Granted || !out.StatusAccepted {
		t.Fatalf("pause acknowledgement: %+v %v", out, err)
	}
	if _, _, deadline := archiveWriterLease(t, db, r.SiteID); !deadline.Equal(activeDeadline) {
		t.Fatal("pause cleared or renewed the outstanding writer lease")
	}
	if dataset, e := s.GetArchiveDataset(ctx, r.SiteID, r.DatasetID); e != nil || dataset.LifecycleState != "paused" {
		t.Fatalf("paused dataset not paused: %+v %v", dataset, e)
	}
	if err = s.RegisterArchiveDataset(ctx, r, "tester"); !errors.Is(err, af.ErrConflict) {
		t.Fatalf("registration ignored outstanding writer lease: %v", err)
	}
	if _, err = db.Exec(`UPDATE site_log_archive_control SET lease_until=UTC_TIMESTAMP(6)-INTERVAL 1 SECOND WHERE site_id=?`, r.SiteID); err != nil {
		t.Fatal(err)
	}
	if err = s.RegisterArchiveDataset(ctx, r, "tester"); err != nil {
		t.Fatal(err)
	}
	c.Running = true
	if err = s.UpdateLogArchive(ctx, r.SiteID, c, "tester"); err != nil {
		t.Fatal(err)
	}
	c.Version++
	other.AppliedVersion = c.Version
	if out, err = s.PollLogArchive(ctx, r.SiteID, other); err != nil || !out.Granted || out.StatusAccepted || out.WriterGrant.WriterEpoch != 2 {
		t.Fatalf("expired lease takeover or bootstrap progress handling failed: %+v %v", out, err)
	}
	other.Foundation.WriterEpoch, other.Foundation.ReceiptID = 2, strings.Repeat("f", 32)
	other.LastID = 60
	if out, err = s.PollLogArchive(ctx, r.SiteID, other); err != nil || !out.Granted || !out.StatusAccepted {
		t.Fatalf("new target claim not acknowledged: %+v %v", out, err)
	}
	st.AppliedVersion = c.Version
	st.LastID = 1
	if out, err = s.PollLogArchive(ctx, r.SiteID, st); err != nil || out.Granted || out.StatusAccepted {
		t.Fatalf("stale epoch accepted after takeover: %+v %v", out, err)
	}
	items, err := s.ListLogArchives(ctx, r.SiteID)
	if err != nil || items[0].Status.LastID != 60 || items[0].Status.Foundation.WriterEpoch != 2 {
		t.Fatalf("stale receipt replaced current progress: %+v %v", items, err)
	}
}

func TestArchiveWriterConcurrentSessionsAndExpiredSameSession(t *testing.T) {
	s, db, r, first, c := archiveWriterControlTestStore(t)
	second := copyArchiveWriterStatus(first)
	second.Session = strings.Repeat("b", 32)
	var wait sync.WaitGroup
	start := make(chan struct{})
	type result struct {
		status ac.Status
		out    ac.Response
		err    error
	}
	results := make(chan result, 2)
	for _, status := range []ac.Status{first, second} {
		wait.Add(1)
		go func(st ac.Status) {
			defer wait.Done()
			<-start
			out, err := s.PollLogArchive(context.Background(), r.SiteID, st)
			results <- result{st, out, err}
		}(status)
	}
	close(start)
	wait.Wait()
	close(results)
	var winner ac.Status
	grants := 0
	for result := range results {
		if result.err != nil {
			t.Fatal(result.err)
		}
		if result.out.Granted {
			grants++
			winner = result.status
		}
	}
	if grants != 1 {
		t.Fatalf("simultaneous writers received %d grants", grants)
	}
	if _, err := db.Exec(`UPDATE site_log_archive_control SET lease_until=UTC_TIMESTAMP(6)-INTERVAL 1 SECOND WHERE site_id=?`, r.SiteID); err != nil {
		t.Fatal(err)
	}
	winner.Foundation.WriterEpoch = 1
	out, err := s.PollLogArchive(context.Background(), r.SiteID, winner)
	if err != nil || !out.Granted || out.WriterGrant.WriterEpoch != 2 {
		t.Fatalf("same session renewed expired epoch: %+v %v", out, err)
	}
	_, _, newDeadline := archiveWriterLease(t, db, r.SiteID)
	// Simulate a lost response: the session still reports its last target epoch.
	out, err = s.PollLogArchive(context.Background(), r.SiteID, winner)
	if err != nil || !out.Granted || out.StatusAccepted || out.WriterGrant.WriterEpoch != 2 {
		t.Fatalf("lost grant cannot recover: %+v %v", out, err)
	}
	if _, _, deadline := archiveWriterLease(t, db, r.SiteID); !deadline.Equal(newDeadline) {
		t.Fatal("stale epoch recovery extended target-unclaimed lease")
	}
	// Changing executor after expiry clears status/lease but never the fence.
	if _, err = db.Exec(`UPDATE site_log_archive_control SET lease_until=UTC_TIMESTAMP(6)-INTERVAL 1 SECOND WHERE site_id=?`, r.SiteID); err != nil {
		t.Fatal(err)
	}
	winner.AgentID, winner.Foundation.WriterEpoch, winner.Foundation.ReceiptID = "replacement", 0, ""
	if _, err = s.PollLogArchive(context.Background(), r.SiteID, winner); err != nil {
		t.Fatal(err)
	}
	c.AgentID = winner.AgentID
	if err = s.UpdateLogArchive(context.Background(), r.SiteID, c, "tester"); err != nil {
		t.Fatal(err)
	}
	winner.AppliedVersion = c.Version + 1
	out, err = s.PollLogArchive(context.Background(), r.SiteID, winner)
	if err != nil || !out.Granted || out.WriterGrant.WriterEpoch != 3 {
		t.Fatalf("executor change reset fence: %+v %v", out, err)
	}
}

func TestArchiveWriterCapabilityIdentityAndReceiptGates(t *testing.T) {
	s, db, r, st, c := archiveWriterControlTestStore(t)
	ctx := context.Background()
	// A legitimate target can downgrade or become stale between configuration
	// edits; the next start request must re-check its current advertisement.
	old := copyArchiveWriterStatus(st)
	old.Foundation.Capabilities = []string{af.CapabilityFoundation}
	if out, err := s.PollLogArchive(ctx, r.SiteID, old); err != nil || out.Granted || out.Config.Running {
		t.Fatalf("foundation-only Agent received writer configuration: %+v %v", out, err)
	}
	if err := s.UpdateLogArchive(ctx, r.SiteID, c, "tester"); !errors.Is(err, ac.ErrConflict) {
		t.Fatalf("foundation-only target can be started: %v", err)
	}
	if _, err := s.PollLogArchive(ctx, r.SiteID, st); err != nil {
		t.Fatal(err)
	}
	for name, mutate := range map[string]func(*ac.Status){
		"site":               func(v *ac.Status) { v.SiteID = "foreign-site" },
		"dataset":            func(v *ac.Status) { v.Foundation.DatasetID = strings.Repeat("b", 32) },
		"generation":         func(v *ac.Status) { v.Foundation.SourceGenerationID = strings.Repeat("b", 32) },
		"future epoch":       func(v *ac.Status) { v.Foundation.WriterEpoch = 20 },
		"unknown capability": func(v *ac.Status) { v.Foundation.Capabilities = []string{af.CapabilityFoundation, "future_writer"} },
		"unsupported daily counter": func(v *ac.Status) {
			v.Days = []ac.Day{{Date: "2026-09-01", ArchivedRows: "100", RequestRows: "100", ErrorRows: "0", VerifiedAt: time.Now().UTC()}}
		},
	} {
		t.Run(name, func(t *testing.T) {
			bad := copyArchiveWriterStatus(st)
			mutate(&bad)
			var before string
			if err := db.QueryRow(`SELECT status_json FROM site_log_archive_control WHERE site_id=?`, r.SiteID).Scan(&before); err != nil {
				t.Fatal(err)
			}
			if _, err := s.PollLogArchive(ctx, r.SiteID, bad); err == nil {
				t.Fatal("invalid writer evidence accepted")
			}
			var after string
			if err := db.QueryRow(`SELECT status_json FROM site_log_archive_control WHERE site_id=?`, r.SiteID).Scan(&after); err != nil || before != after {
				t.Fatalf("rejected poll mutated progress: %v", err)
			}
		})
	}
	c.ReconcileID, c.ReconcileDate = strings.Repeat("c", 32), "2026-08-01"
	if err := s.UpdateLogArchive(ctx, r.SiteID, c, "tester"); !errors.Is(err, ac.ErrConflict) {
		t.Fatalf("unimplemented versioned reconciliation enabled: %v", err)
	}
	c.ReconcileID, c.ReconcileDate = "", ""
	if _, err := db.Exec(`UPDATE log_archive_targets SET seen_at=UTC_TIMESTAMP()-INTERVAL 91 SECOND WHERE instance_id=?`, r.SiteID); err != nil {
		t.Fatal(err)
	}
	if err := s.UpdateLogArchive(ctx, r.SiteID, c, "tester"); !errors.Is(err, ac.ErrConflict) {
		t.Fatalf("stale writer target enabled: %v", err)
	}
	// Exhausting the fence counter cannot wrap around and authorize an old token.
	if _, err := db.Exec(`UPDATE site_log_archive_control SET writer_epoch=18446744073709551615,lease_until=UTC_TIMESTAMP()-INTERVAL 1 SECOND WHERE site_id=?`, r.SiteID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.PollLogArchive(ctx, r.SiteID, st); !errors.Is(err, ac.ErrConflict) {
		t.Fatalf("writer epoch overflow accepted: %v", err)
	}
}

func TestArchiveWriterMigrationResumePreservesEpoch(t *testing.T) {
	_, db, r, _, _ := archiveWriterControlTestStore(t)
	if _, err := db.Exec(`UPDATE site_log_archive_control SET writer_epoch=25 WHERE site_id=?`, r.SiteID); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile("../../migrations/086_archive_writer.sql")
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		if err = ApplySQL(context.Background(), db, string(data)); err != nil {
			t.Fatal(err)
		}
	}
	var epoch uint64
	if err = db.QueryRow(`SELECT writer_epoch FROM site_log_archive_control WHERE site_id=?`, r.SiteID).Scan(&epoch); err != nil || epoch != 25 {
		t.Fatalf("migration resume reset writer epoch: %d %v", epoch, err)
	}
}
