package mysqlstore

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	af "controltower/internal/archivecontract"
	ac "controltower/internal/archivecontrol"
	"controltower/server/internal/storage"
)

func archiveFoundationTestStore(t *testing.T) (Store, *sql.DB, af.Registration) {
	t.Helper()
	dsn := os.Getenv("CT_MYSQL_TEST_DSN")
	if dsn == "" {
		t.Skip("set CT_MYSQL_TEST_DSN to run archive foundation integration tests")
	}
	db, err := Open(dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err = ApplyDir(context.Background(), db, "../../migrations"); err != nil {
		t.Fatal(err)
	}
	raw := make([]byte, 16)
	if _, err = rand.Read(raw); err != nil {
		t.Fatal(err)
	}
	datasetID := hex.EncodeToString(raw)
	if _, err = rand.Read(raw); err != nil {
		t.Fatal(err)
	}
	r := af.Registration{
		Identity:   af.Identity{SiteID: "archive-foundation-" + datasetID, DatasetID: datasetID, SourceGenerationID: hex.EncodeToString(raw)},
		StorageRef: "archive-" + datasetID, ArchiveFormatVersion: af.FormatVersion,
		SchemaFingerprint: strings.Repeat("a", 64), SourceFingerprint: strings.Repeat("b", 64),
	}
	s := New(db)
	now := time.Now().UTC()
	if err = s.CreateInstance(storage.Instance{ID: r.SiteID, SiteID: r.SiteID, Enabled: true, CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		for _, table := range []string{"archive_day_catalog", "archive_tasks", "archive_datasets"} {
			_, _ = db.Exec("DELETE FROM "+table+" WHERE dataset_id=?", archiveIDBytes(r.DatasetID))
		}
		for _, table := range []string{"site_log_archive_days", "site_log_archive_control"} {
			_, _ = db.Exec("DELETE FROM "+table+" WHERE site_id=?", r.SiteID)
		}
		for _, table := range []string{"log_archive_targets", "log_archive_control", "operation_audits"} {
			_, _ = db.Exec("DELETE FROM "+table+" WHERE instance_id=?", r.SiteID)
		}
		_, _ = db.Exec("DELETE FROM instances WHERE id=?", r.SiteID)
	})
	return s, db, r
}

func TestArchiveFoundationRegistrationAndLegacyBoundary(t *testing.T) {
	s, db, registration := archiveFoundationTestStore(t)
	ctx := context.Background()
	caseVariant := registration
	caseVariant.SiteID = strings.ToUpper(registration.SiteID)
	if err := s.RegisterArchiveDataset(ctx, caseVariant, "tester"); !errors.Is(err, af.ErrIdentity) {
		t.Fatalf("case variant of canonical site identity accepted: %v", err)
	}
	var registrations, controls int
	if err := db.QueryRow(`SELECT COUNT(*) FROM archive_datasets WHERE dataset_id=?`, archiveIDBytes(registration.DatasetID)).Scan(&registrations); err != nil || registrations != 0 {
		t.Fatalf("rejected site identity created a dataset: %d %v", registrations, err)
	}
	if err := db.QueryRow(`SELECT COUNT(*) FROM site_log_archive_control WHERE site_id=?`, registration.SiteID).Scan(&controls); err != nil || controls != 0 {
		t.Fatalf("rejected site identity created site control: %d %v", controls, err)
	}
	legacy := ac.Status{SiteID: registration.SiteID, AgentID: "archive-test", Session: strings.Repeat("c", 32), Configured: true, State: "paused"}
	out, err := s.PollLogArchive(ctx, registration.SiteID, legacy)
	if err != nil {
		t.Fatal(err)
	}
	config := out.Config
	config.InstanceID, config.AgentID, config.Running = registration.SiteID, legacy.AgentID, true
	if err = s.UpdateLogArchive(ctx, registration.SiteID, config, "tester"); err != nil {
		t.Fatal(err)
	}
	legacy.Days = []ac.Day{{Date: "2026-08-31", ArchivedRows: "7", RequestRows: "7", ErrorRows: "0", LastID: 7, VerifiedAt: time.Now().UTC()}}
	out, err = s.PollLogArchive(ctx, registration.SiteID, legacy)
	if err != nil || !out.Granted {
		t.Fatalf("legacy execution must work before binding: %+v %v", out, err)
	}
	if err = s.RegisterArchiveDataset(ctx, registration, "tester"); !errors.Is(err, af.ErrConflict) {
		t.Fatalf("running legacy writer must prevent registration: %v", err)
	}
	config = out.Config
	config.Running = false
	if err = s.UpdateLogArchive(ctx, registration.SiteID, config, "tester"); err != nil {
		t.Fatal(err)
	}
	if err = s.RegisterArchiveDataset(ctx, registration, "tester"); !errors.Is(err, af.ErrConflict) {
		t.Fatalf("unexpired legacy lease must prevent registration: %v", err)
	}
	if _, err = db.Exec(`UPDATE site_log_archive_control SET lease_until=UTC_TIMESTAMP()-INTERVAL 1 SECOND WHERE site_id=?`, registration.SiteID); err != nil {
		t.Fatal(err)
	}
	if err = s.RegisterArchiveDataset(ctx, registration, "tester"); err != nil {
		t.Fatal(err)
	}
	var catalogRows int
	if err = db.QueryRow(`SELECT COUNT(*) FROM archive_day_catalog WHERE dataset_id=?`, archiveIDBytes(registration.DatasetID)).Scan(&catalogRows); err != nil || catalogRows != 0 {
		t.Fatalf("legacy counters must not become a versioned catalog: %d %v", catalogRows, err)
	}
	if days, e := s.ListLogArchiveDays(ctx, registration.SiteID, "2026-08"); e != nil || len(days) != 0 {
		t.Fatalf("new dataset page must not present legacy daily history: %+v %v", days, e)
	}
	if month, e := s.LatestLogArchiveMonth(ctx, registration.SiteID); e != nil || month != "" {
		t.Fatalf("legacy month selected for newly bound dataset: %q %v", month, e)
	}
	if items, e := s.ListLogArchives(ctx, registration.SiteID); e != nil || len(items) != 1 || items[0].ActiveDatasetID != registration.DatasetID || items[0].RequiredProtocolVersion != af.ProtocolVersion {
		t.Fatalf("active foundation identity missing from page metadata: %+v %v", items, e)
	}
	registered, err := s.GetArchiveDataset(ctx, registration.SiteID, registration.DatasetID)
	if err != nil || !sameArchiveRegistration(registered.Registration, registration) || registered.LifecycleState != "paused" || registered.ObservedCatalogRevision != 0 || registered.CatalogHash != "" {
		t.Fatalf("unexpected initial dataset: %+v %v", registered, err)
	}
	if err = s.RegisterArchiveDataset(ctx, registration, "tester"); err != nil {
		t.Fatalf("repeated registration must be idempotent: %v", err)
	}
	after, err := s.GetArchiveDataset(ctx, registration.SiteID, registration.DatasetID)
	if err != nil || after.ConfigRevision != registered.ConfigRevision || !after.UpdatedAt.Equal(registered.UpdatedAt) {
		t.Fatalf("repeat changed registration: %+v %v", after, err)
	}
	for name, mutate := range map[string]func(*af.Registration){
		"storage":    func(r *af.Registration) { r.StorageRef += "-other" },
		"source":     func(r *af.Registration) { r.SourceFingerprint = strings.Repeat("f", 64) },
		"schema":     func(r *af.Registration) { r.SchemaFingerprint = strings.Repeat("f", 64) },
		"generation": func(r *af.Registration) { r.SourceGenerationID = strings.Repeat("f", 32) },
	} {
		t.Run(name, func(t *testing.T) {
			changed := registration
			mutate(&changed)
			if err := s.RegisterArchiveDataset(ctx, changed, "tester"); !errors.Is(err, af.ErrConflict) {
				t.Fatalf("registration rebinding accepted: %v", err)
			}
		})
	}
	if _, err = s.GetArchiveDataset(ctx, "other-site", registration.DatasetID); !errors.Is(err, af.ErrNotFound) {
		t.Fatalf("cross-site dataset read accepted: %v", err)
	}
	sameGeneration := registration
	sameGeneration.DatasetID = strings.Repeat("d", 32)
	sameGeneration.StorageRef += "-other"
	if err = s.RegisterArchiveDataset(ctx, sameGeneration, "tester"); !errors.Is(err, af.ErrConflict) {
		t.Fatalf("same source generation registered under another dataset: %v", err)
	}
	sameStorage := sameGeneration
	sameStorage.SourceGenerationID = strings.Repeat("d", 32)
	sameStorage.StorageRef = registration.StorageRef
	if err = s.RegisterArchiveDataset(ctx, sameStorage, "tester"); !errors.Is(err, af.ErrConflict) {
		t.Fatalf("same archive storage registered to a new identity: %v", err)
	}
	legacy.Days = []ac.Day{{Date: "2026-09-01", ArchivedRows: "42", RequestRows: "42", ErrorRows: "0", LastID: 42, VerifiedAt: time.Now().UTC()}}
	out, err = s.PollLogArchive(ctx, registration.SiteID, legacy)
	if err != nil || out.Granted || out.StatusAccepted || out.Config.Running {
		t.Fatalf("bound legacy poll must stay unaccepted and paused: %+v %v", out, err)
	}
	var rows int
	if err = db.QueryRow(`SELECT COUNT(*) FROM site_log_archive_days WHERE site_id=? AND log_date='2026-09-01'`, registration.SiteID).Scan(&rows); err != nil || rows != 0 {
		t.Fatalf("legacy day incorrectly consumed after binding: %d %v", rows, err)
	}
	if err = db.QueryRow(`SELECT COUNT(*) FROM site_log_archive_days WHERE site_id=? AND log_date='2026-08-31'`, registration.SiteID).Scan(&rows); err != nil || rows != 1 {
		t.Fatalf("binding removed old legacy history: %d %v", rows, err)
	}
	config = out.Config
	config.Running = true
	if err = s.UpdateLogArchive(ctx, registration.SiteID, config, "tester"); !errors.Is(err, ac.ErrConflict) {
		t.Fatalf("foundation writer incorrectly enabled: %v", err)
	}
	v2 := legacy
	v2.Days = nil
	v2.Foundation = &af.FoundationStatus{Identity: registration.Identity, ProtocolVersion: af.ProtocolVersion, FormatVersion: af.FormatVersion, Capabilities: []string{af.CapabilityFoundation}}
	v2.AppliedVersion = out.Config.Version
	out, err = s.PollLogArchive(ctx, registration.SiteID, v2)
	if err != nil || out.Granted || !out.StatusAccepted {
		t.Fatalf("matching foundation identity should be acknowledged without execution: %+v %v", out, err)
	}
	for _, state := range []string{"error", "waiting", "unconfigured", "running"} {
		t.Run("observed_"+state, func(t *testing.T) {
			report := v2
			report.State, report.Configured, report.Error = state, false, "archive foundation preflight failed"
			if response, e := s.PollLogArchive(ctx, registration.SiteID, report); e != nil || response.Granted || !response.StatusAccepted {
				t.Fatalf("foundation health status lost: %+v %v", response, e)
			}
			items, e := s.ListLogArchives(ctx, registration.SiteID)
			if e != nil || len(items) != 1 {
				t.Fatalf("read foundation health: %+v %v", items, e)
			}
			want := state
			if want == "running" {
				want = "paused"
			}
			if items[0].Status.State != want || items[0].Status.Configured || items[0].Status.Error != report.Error {
				t.Fatalf("incorrect foundation health projection: %+v", items[0].Status)
			}
		})
	}
	for name, mutate := range map[string]func(*ac.Status){
		"wrong site":         func(v *ac.Status) { v.Foundation.SiteID = "other-site" },
		"wrong dataset":      func(v *ac.Status) { v.Foundation.DatasetID = strings.Repeat("e", 32) },
		"wrong generation":   func(v *ac.Status) { v.Foundation.SourceGenerationID = strings.Repeat("e", 32) },
		"old protocol":       func(v *ac.Status) { v.Foundation.ProtocolVersion = 1 },
		"missing capability": func(v *ac.Status) { v.Foundation.Capabilities = nil },
		"writer epoch":       func(v *ac.Status) { v.Foundation.WriterEpoch = 1 },
		"receipt":            func(v *ac.Status) { v.Foundation.ReceiptID = strings.Repeat("e", 32) },
		"legacy days":        func(v *ac.Status) { v.Days = legacy.Days },
	} {
		t.Run(name, func(t *testing.T) {
			bad := v2
			foundation := *v2.Foundation
			bad.Foundation = &foundation
			mutate(&bad)
			if _, err := s.PollLogArchive(ctx, registration.SiteID, bad); err == nil {
				t.Fatal("invalid versioned poll accepted")
			}
		})
	}
}

func TestArchiveFoundationCatalogRevisionAndAtomicReplacement(t *testing.T) {
	s, db, registration := archiveFoundationTestStore(t)
	ctx := context.Background()
	if err := s.RegisterArchiveDataset(ctx, registration, "tester"); err != nil {
		t.Fatal(err)
	}
	zero, large := "0", "9007199254740993"
	snapshot := af.CatalogSnapshot{Identity: registration.Identity, CatalogRevision: 3, UnscopedBlockingIssues: 2, Days: []af.CatalogDay{
		{Date: "2026-09-01", State: "unknown"},
		{Date: "2026-09-02", State: "collecting", AllRows: &large, ConsumeRows: &zero, ConsumeQuota: &zero},
	}}
	if applied, err := s.ApplyArchiveCatalog(ctx, snapshot); err != nil || !applied {
		t.Fatalf("initial catalog application: %v %v", applied, err)
	}
	var unknown, exact sql.NullString
	if err := db.QueryRow(`SELECT all_rows FROM archive_day_catalog WHERE dataset_id=? AND log_date='2026-09-01'`, archiveIDBytes(registration.DatasetID)).Scan(&unknown); err != nil || unknown.Valid {
		t.Fatalf("unknown count became zero: %+v %v", unknown, err)
	}
	if err := db.QueryRow(`SELECT all_rows FROM archive_day_catalog WHERE dataset_id=? AND log_date='2026-09-02'`, archiveIDBytes(registration.DatasetID)).Scan(&exact); err != nil || !exact.Valid || exact.String != large {
		t.Fatalf("large exact count lost: %+v %v", exact, err)
	}
	reordered := snapshot
	reordered.Days = []af.CatalogDay{snapshot.Days[1], snapshot.Days[0]}
	if applied, err := s.ApplyArchiveCatalog(ctx, reordered); err != nil || applied {
		t.Fatalf("identical revision replay should be a no-op: %v %v", applied, err)
	}
	changed := snapshot
	changed.UnscopedBlockingIssues++
	if _, err := s.ApplyArchiveCatalog(ctx, changed); !errors.Is(err, af.ErrConflict) {
		t.Fatalf("same revision with different content accepted: %v", err)
	}
	older := changed
	older.CatalogRevision = 2
	if applied, err := s.ApplyArchiveCatalog(ctx, older); err != nil || applied {
		t.Fatalf("stale catalog should be ignored: %v %v", applied, err)
	}
	wrong := snapshot
	wrong.SourceGenerationID = strings.Repeat("f", 32)
	if _, err := s.ApplyArchiveCatalog(ctx, wrong); !errors.Is(err, af.ErrIdentity) {
		t.Fatalf("foreign generation catalog accepted: %v", err)
	}
	invalid := snapshot
	invalid.CatalogRevision++
	invalid.Days = []af.CatalogDay{{Date: "2026-09-03", State: "unknown"}, {Date: "2026-09-04", State: "unknown", BlockReason: "无法写入 ASCII"}}
	if _, err := s.ApplyArchiveCatalog(ctx, invalid); err == nil {
		t.Fatal("invalid catalog should fail")
	}
	d, err := s.GetArchiveDataset(ctx, registration.SiteID, registration.DatasetID)
	if err != nil || d.ObservedCatalogRevision != 3 || d.UnscopedBlockingIssues != 2 {
		t.Fatalf("failed catalog partially changed registration: %+v %v", d, err)
	}
	var rows int
	if err = db.QueryRow(`SELECT COUNT(*) FROM archive_day_catalog WHERE dataset_id=? AND log_date IN ('2026-09-01','2026-09-02')`, archiveIDBytes(registration.DatasetID)).Scan(&rows); err != nil || rows != 2 {
		t.Fatalf("failed snapshot removed previous directory: %d %v", rows, err)
	}
	empty := af.CatalogSnapshot{Identity: registration.Identity, CatalogRevision: 4, Days: []af.CatalogDay{}}
	if applied, err := s.ApplyArchiveCatalog(ctx, empty); err != nil || !applied {
		t.Fatalf("empty authoritative snapshot: %v %v", applied, err)
	}
	if err = db.QueryRow(`SELECT COUNT(*) FROM archive_day_catalog WHERE dataset_id=?`, archiveIDBytes(registration.DatasetID)).Scan(&rows); err != nil || rows != 0 {
		t.Fatalf("whole directory replacement retained removed days: %d %v", rows, err)
	}
}

func TestArchiveFoundationMigrationCanResume(t *testing.T) {
	_, db, _ := archiveFoundationTestStore(t)
	data, err := os.ReadFile("../../migrations/085_archive_foundation.sql")
	if err != nil {
		t.Fatal(err)
	}
	// Each DDL operation must tolerate a previous partial application because
	// MySQL commits DDL separately from the migration bookkeeping record.
	for repeat := 0; repeat < 2; repeat++ {
		if err = ApplySQL(context.Background(), db, string(data)); err != nil {
			t.Fatalf("migration repeat %d: %v", repeat, err)
		}
	}
	for _, table := range []string{"archive_datasets", "archive_day_catalog", "archive_tasks"} {
		var count int
		if err = db.QueryRow(`SELECT COUNT(*) FROM information_schema.tables WHERE table_schema=DATABASE() AND table_name=?`, table).Scan(&count); err != nil || count != 1 {
			t.Fatal(fmt.Sprintf("missing foundation table %s", table), err)
		}
	}
}

func TestArchiveLegacySiteEchoRejectsForeignReceipts(t *testing.T) {
	s, db, registration := archiveFoundationTestStore(t)
	ctx := context.Background()
	status := ac.Status{AgentID: "archive-test", Session: strings.Repeat("a", 32), Configured: true, State: "paused"}
	first, err := s.PollLogArchive(ctx, registration.SiteID, status)
	if err != nil || first.SiteID != registration.SiteID || first.Granted || first.StatusAccepted {
		t.Fatalf("legacy identity bootstrap failed: %+v %v", first, err)
	}
	config := first.Config
	config.InstanceID, config.AgentID, config.Running = registration.SiteID, status.AgentID, true
	if err = s.UpdateLogArchive(ctx, registration.SiteID, config, "tester"); err != nil {
		t.Fatal(err)
	}
	status.Days = []ac.Day{{Date: "2026-09-01", ArchivedRows: "901", RequestRows: "900", ErrorRows: "1", LastID: 901, VerifiedAt: time.Now().UTC()}}
	status.AppliedVersion = config.Version + 1000
	for _, foreignSite := range []string{"", "foreign-site"} {
		status.SiteID = foreignSite
		response, e := s.PollLogArchive(ctx, registration.SiteID, status)
		if e != nil || response.SiteID != registration.SiteID || response.Granted || response.StatusAccepted || response.LeaseSeconds != 0 {
			t.Fatalf("foreign receipt did not receive a safe bootstrap response: %+v %v", response, e)
		}
		var count int
		if e = db.QueryRow(`SELECT COUNT(*) FROM site_log_archive_days WHERE site_id=?`, registration.SiteID).Scan(&count); e != nil || count != 0 {
			t.Fatalf("foreign site daily receipt imported: %d %v", count, e)
		}
		var storedStatus, session string
		var seen, lease sql.NullTime
		if e = db.QueryRow(`SELECT status_json,session_id,seen_at,lease_until FROM site_log_archive_control WHERE site_id=?`, registration.SiteID).Scan(&storedStatus, &session, &seen, &lease); e != nil || storedStatus != "{}" || session != "" || seen.Valid || lease.Valid {
			t.Fatalf("foreign receipt changed control status: status=%q session=%q seen=%+v lease=%+v error=%v", storedStatus, session, seen, lease, e)
		}
		status.AppliedVersion = response.Config.Version
	}
	status.SiteID = registration.SiteID
	response, err := s.PollLogArchive(ctx, registration.SiteID, status)
	if err != nil || !response.Granted || !response.StatusAccepted {
		t.Fatalf("correct site echo failed after bootstrap: %+v %v", response, err)
	}
	days, err := s.ListLogArchiveDays(ctx, registration.SiteID, "2026-09")
	if err != nil || len(days) != 1 || days[0].ArchivedRows != "901" {
		t.Fatalf("valid echoed receipt was not imported: %+v %v", days, err)
	}
}
