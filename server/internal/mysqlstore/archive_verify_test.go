package mysqlstore

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	af "controltower/internal/archivecontract"
	ac "controltower/internal/archivecontrol"
)

func TestArchiveVerifyExplicitDateSet(t *testing.T) {
	input := []string{"2026-09-02", "2026-09-01"}
	got, err := archiveVerifyDates(input)
	if err != nil || !reflect.DeepEqual(got, []string{"2026-09-01", "2026-09-02"}) || input[0] != "2026-09-02" {
		t.Fatalf("canonical set mutated input: %v %v", got, err)
	}
	for _, invalid := range [][]string{nil, {"2026-02-30"}, {"2026-09-01", "2026-09-01"}, {"2026-9-1"}, make([]string, 32)} {
		if _, err := archiveVerifyDates(invalid); !errors.Is(err, af.ErrConflict) {
			t.Fatalf("invalid date set accepted: %v %v", invalid, err)
		}
	}
}

func archiveVerifyTestStore(t *testing.T) (Store, *sql.DB, af.Registration, ac.Status, ac.Config, time.Time) {
	t.Helper()
	s, db, r, st, c, now := archiveBackfillTestStore(t)
	archiveBackfillConfirm(t, s, r, now, true)
	st.Foundation.Capabilities = append(st.Foundation.Capabilities, af.CapabilityReconcile, af.CapabilitySeal)
	return s, db, r, st, c, now
}

func archiveVerifyTestRequest(now time.Time, id string, age int) ac.ReconcileRequest {
	return ac.ReconcileRequest{RequestID: strings.Repeat(id, 32), Date: now.AddDate(0, 0, -age).Format("2006-01-02"), Assurance: af.VerificationAssurance{StableBeforeUnix: now.Add(-time.Hour).Unix(), ValidUntilUnix: now.Add(time.Hour).Unix(), Evidence: "operator confirms retained immutable source interval"}}
}

func archiveVerifyStart(t *testing.T, s Store, r af.Registration, st *ac.Status) ac.Response {
	t.Helper()
	out, err := s.PollLogArchive(context.Background(), r.SiteID, *st)
	if err != nil || out.WriterGrant == nil || out.ReconcileTask != nil || out.SealTask != nil || out.BackfillTask != nil || out.ArchivePolicy == nil {
		t.Fatalf("unclaimed grant dispatched task or omitted policy: %+v %v", out, err)
	}
	st.Foundation.WriterEpoch = out.WriterGrant.WriterEpoch
	out, err = s.PollLogArchive(context.Background(), r.SiteID, *st)
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func archiveVerifyReport(task ac.ReconcileTaskItem, epoch uint64) af.ReconcileStatus {
	return af.ReconcileStatus{TaskID: task.TaskID, RunID: strings.Repeat("c", 32), Date: task.Date, Attempt: task.Attempt, WriterEpoch: epoch, ProgressVersion: 1, StartRevision: 4, FinalRevision: 4, SourceRows: 8, TargetRows: 8, CatalogRevision: 4, State: "running", Phase: "source_first", Method: "stable_window_paged"}
}

func TestArchiveVerifyRequestsBindAssuranceAndDateSet(t *testing.T) {
	s, _, r, _, _, now := archiveVerifyTestStore(t)
	ctx := context.Background()
	request := archiveVerifyTestRequest(now, "1", 2)
	a, err := s.CreateArchiveReconcile(ctx, r.SiteID, r.DatasetID, request, "tester")
	if err != nil {
		t.Fatal(err)
	}
	b, err := s.CreateArchiveReconcile(ctx, r.SiteID, r.DatasetID, request, "tester")
	if err != nil || a.TaskID != b.TaskID {
		t.Fatalf("same request not idempotent: %+v %v", b, err)
	}
	request.Assurance.Evidence = "different stability assertion"
	if _, err = s.CreateArchiveReconcile(ctx, r.SiteID, r.DatasetID, request, "tester"); !errors.Is(err, af.ErrConflict) {
		t.Fatalf("reused id changed assurance: %v", err)
	}
	if _, err = s.CreateArchiveSeal(ctx, r.SiteID, r.DatasetID, ac.SealRequest{RequestID: request.RequestID, Dates: []string{request.Date}}, "tester"); !errors.Is(err, af.ErrConflict) {
		t.Fatalf("verify request became seal: %v", err)
	}
	seal := ac.SealRequest{RequestID: strings.Repeat("2", 32), Dates: []string{now.AddDate(0, 0, -2).Format("2006-01-02"), now.AddDate(0, 0, -3).Format("2006-01-02")}}
	x, err := s.CreateArchiveSeal(ctx, r.SiteID, r.DatasetID, seal, "tester")
	if err != nil {
		t.Fatal(err)
	}
	seal.Dates[0], seal.Dates[1] = seal.Dates[1], seal.Dates[0]
	y, err := s.CreateArchiveSeal(ctx, r.SiteID, r.DatasetID, seal, "tester")
	if err != nil || x.TaskID != y.TaskID || y.Dates[0] >= y.Dates[1] {
		t.Fatalf("equivalent set not idempotent: %+v %v", y, err)
	}
	seal.Dates = seal.Dates[:1]
	if _, err = s.CreateArchiveSeal(ctx, r.SiteID, r.DatasetID, seal, "tester"); !errors.Is(err, af.ErrConflict) {
		t.Fatalf("seal cohort changed: %v", err)
	}
	if _, err = s.ListArchiveReconciles(ctx, "foreign-site", r.DatasetID); !errors.Is(err, af.ErrNotFound) {
		t.Fatalf("cross-site verify list: %v", err)
	}
	if _, err = s.RetryArchiveSeal(ctx, "foreign-site", r.DatasetID, x.TaskID, "tester"); err == nil {
		t.Fatal("cross-site seal retry accepted")
	}
	request = archiveVerifyTestRequest(now, "3", 0)
	if _, err = s.CreateArchiveReconcile(ctx, r.SiteID, r.DatasetID, request, "tester"); !errors.Is(err, af.ErrConflict) {
		t.Fatalf("today was queued: %v", err)
	}
}

func TestArchiveVerifyReportGuardsAndPersistentRunReference(t *testing.T) {
	s, db, r, st, _, now := archiveVerifyTestStore(t)
	ctx := context.Background()
	task, err := s.CreateArchiveReconcile(ctx, r.SiteID, r.DatasetID, archiveVerifyTestRequest(now, "1", 2), "tester")
	if err != nil {
		t.Fatal(err)
	}
	out := archiveVerifyStart(t, s, r, &st)
	if out.ReconcileTask == nil || out.ReconcileTask.TaskID != task.TaskID || out.SealTask != nil || out.BackfillTask != nil {
		t.Fatalf("wrong dispatched task: %+v", out)
	}
	report := archiveVerifyReport(task, st.Foundation.WriterEpoch)
	st.Reconcile = &report
	if out, err = s.PollLogArchive(ctx, r.SiteID, st); err != nil || !out.ReconcileAccepted {
		t.Fatalf("progress not accepted: %+v %v", out, err)
	}
	for name, mutate := range map[string]func(*af.ReconcileStatus){
		"foreign task":                       func(p *af.ReconcileStatus) { p.TaskID = strings.Repeat("f", 32) },
		"different date":                     func(p *af.ReconcileStatus) { p.Date = now.AddDate(0, 0, -3).Format("2006-01-02") },
		"future epoch":                       func(p *af.ReconcileStatus) { p.WriterEpoch++ },
		"wrong attempt":                      func(p *af.ReconcileStatus) { p.Attempt++ },
		"replayed version with new contents": func(p *af.ReconcileStatus) { p.SourceRows++ },
		"regressing progress":                func(p *af.ReconcileStatus) { p.ProgressVersion = 0 },
		"retry mutated evidence": func(p *af.ReconcileStatus) {
			p.State = "retry_wait"
			p.ErrorCode = "source_unavailable"
			p.SourceRows++
		},
		"regressing catalog":   func(p *af.ReconcileStatus) { p.ProgressVersion++; p.CatalogRevision-- },
		"different target run": func(p *af.ReconcileStatus) { p.ProgressVersion++; p.RunID = strings.Repeat("d", 32) },
		"bare client match": func(p *af.ReconcileStatus) {
			p.ProgressVersion++
			p.State = "matched"
			p.Phase = "completed"
			p.RunID = ""
			p.SourceDigest = strings.Repeat("a", 64)
			p.TargetDigest = p.SourceDigest
		},
	} {
		t.Run(name, func(t *testing.T) {
			bad := report
			mutate(&bad)
			st.Reconcile = &bad
			out, err := s.PollLogArchive(ctx, r.SiteID, st)
			if err != nil || out.ReconcileAccepted {
				t.Fatalf("invalid report accepted: %+v %v", out, err)
			}
		})
	}
	st.Reconcile = &report
	if _, err = db.Exec(`UPDATE archive_tasks SET lease_session=? WHERE task_id=?`, strings.Repeat("f", 32), archiveIDBytes(task.TaskID)); err != nil {
		t.Fatal(err)
	}
	if out, err = s.PollLogArchive(ctx, r.SiteID, st); err != nil || out.ReconcileAccepted {
		t.Fatalf("wrong session accepted: %+v %v", out, err)
	}
	// Dispatch restores the current session; completion now refers to the same
	// persisted target run and is still not a CT catalog authorization.
	report.State, report.Phase, report.ProgressVersion = "matched", "completed", 2
	report.SourceDigest, report.TargetDigest = strings.Repeat("a", 64), strings.Repeat("a", 64)
	if out, err = s.PollLogArchive(ctx, r.SiteID, st); err != nil || !out.ReconcileAccepted || out.ReconcileTask != nil {
		t.Fatalf("terminal run not accepted: %+v %v", out, err)
	}
	var run []byte
	if err = db.QueryRow(`SELECT result_run_id FROM archive_tasks WHERE task_id=?`, archiveIDBytes(task.TaskID)).Scan(&run); err != nil || !reflect.DeepEqual(run, archiveIDBytes(report.RunID)) {
		t.Fatalf("target run reference not persisted: %x %v", run, err)
	}
	if out, err = s.PollLogArchive(ctx, r.SiteID, st); err != nil || !out.ReconcileAccepted {
		t.Fatalf("terminal replay not acknowledged: %+v %v", out, err)
	}
	var count int
	if err = db.QueryRow(`SELECT COUNT(*) FROM archive_day_catalog WHERE dataset_id=?`, archiveIDBytes(r.DatasetID)).Scan(&count); err != nil || count != 0 {
		t.Fatalf("poll wrote authoritative catalog: %d %v", count, err)
	}
}

func TestArchiveVerifyRetryPauseAndOldAttempt(t *testing.T) {
	s, _, r, st, c, now := archiveVerifyTestStore(t)
	ctx := context.Background()
	task, err := s.CreateArchiveReconcile(ctx, r.SiteID, r.DatasetID, archiveVerifyTestRequest(now, "1", 2), "tester")
	if err != nil {
		t.Fatal(err)
	}
	archiveVerifyStart(t, s, r, &st)
	report := archiveVerifyReport(task, st.Foundation.WriterEpoch)
	report.State, report.Phase, report.ErrorCode = "mismatched", "completed", "verification_mismatch"
	report.IssueCount = 1
	st.Reconcile = &report
	c.Running = false
	if err = s.UpdateLogArchive(ctx, r.SiteID, c, "tester"); err != nil {
		t.Fatal(err)
	}
	c.Version++
	st.AppliedVersion = c.Version
	out, err := s.PollLogArchive(ctx, r.SiteID, st)
	if err != nil || out.ReconcileAccepted || out.ReconcileTask != nil {
		t.Fatalf("pause accepted/dispatched: %+v %v", out, err)
	}
	c.Running = true
	if err = s.UpdateLogArchive(ctx, r.SiteID, c, "tester"); err != nil {
		t.Fatal(err)
	}
	c.Version++
	st.AppliedVersion = c.Version
	out, err = s.PollLogArchive(ctx, r.SiteID, st)
	if err != nil || !out.ReconcileAccepted {
		t.Fatalf("resume lost completion: %+v %v", out, err)
	}
	retried, err := s.RetryArchiveReconcile(ctx, r.SiteID, r.DatasetID, task.TaskID, "tester")
	if err != nil || retried.Attempt != 2 || retried.Progress != nil {
		t.Fatalf("retry failed: %+v %v", retried, err)
	}
	out, err = s.PollLogArchive(ctx, r.SiteID, st)
	if err != nil || out.ReconcileAccepted || out.ReconcileTask == nil || out.ReconcileTask.Attempt != 2 {
		t.Fatalf("old attempt accepted: %+v %v", out, err)
	}
}

func TestArchiveSealRequiresExactVersionCohort(t *testing.T) {
	s, db, r, st, _, now := archiveVerifyTestStore(t)
	ctx := context.Background()
	task, err := s.CreateArchiveSeal(ctx, r.SiteID, r.DatasetID, ac.SealRequest{RequestID: strings.Repeat("1", 32), Dates: []string{now.AddDate(0, 0, -3).Format("2006-01-02"), now.AddDate(0, 0, -2).Format("2006-01-02")}}, "tester")
	if err != nil {
		t.Fatal(err)
	}
	out := archiveVerifyStart(t, s, r, &st)
	if out.SealTask == nil || out.SealTask.TaskID != task.TaskID || out.ReconcileTask != nil || out.BackfillTask != nil {
		t.Fatalf("seal not exclusively dispatched: %+v", out)
	}
	report := af.SealStatus{TaskID: task.TaskID, BuildID: strings.Repeat("b", 32), Attempt: 1, WriterEpoch: st.Foundation.WriterEpoch, ProgressVersion: 1, CatalogRevision: 4, State: "succeeded"}
	for i, date := range task.Dates {
		report.Versions = append(report.Versions, af.SealedDay{Date: date, VersionID: strings.Repeat(string(rune('c'+i)), 32), VersionNo: 1, ManifestHash: strings.Repeat("e", 64)})
	}
	for name, mutate := range map[string]func(*af.SealStatus){
		"partial cohort": func(p *af.SealStatus) { p.Versions = p.Versions[:1] },
		"different date": func(p *af.SealStatus) { p.Versions[0].Date = now.AddDate(0, 0, -4).Format("2006-01-02") },
		"no build":       func(p *af.SealStatus) { p.BuildID = "" },
		"future epoch":   func(p *af.SealStatus) { p.WriterEpoch++ },
	} {
		t.Run(name, func(t *testing.T) {
			bad := report
			bad.Versions = append([]af.SealedDay(nil), report.Versions...)
			mutate(&bad)
			st.Seal = &bad
			out, err := s.PollLogArchive(ctx, r.SiteID, st)
			if err != nil || out.SealAccepted {
				t.Fatalf("bad cohort accepted: %+v %v", out, err)
			}
		})
	}
	st.Seal = &report
	out, err = s.PollLogArchive(ctx, r.SiteID, st)
	if err != nil || !out.SealAccepted || out.SealTask != nil {
		t.Fatalf("seal completion failed: %+v %v", out, err)
	}
	items, err := s.ListArchiveSeals(ctx, r.SiteID, r.DatasetID)
	if err != nil || len(items) != 1 || !reflect.DeepEqual(items[0].Progress, &report) {
		t.Fatalf("seal receipt lost: %+v %v", items, err)
	}
	var count int
	if err = db.QueryRow(`SELECT COUNT(*) FROM archive_day_catalog WHERE dataset_id=?`, archiveIDBytes(r.DatasetID)).Scan(&count); err != nil || count != 0 {
		t.Fatalf("seal report modified catalog: %d %v", count, err)
	}
}

func TestArchiveVerificationBackoffSharesWriterAndCapabilityGate(t *testing.T) {
	s, db, r, st, _, now := archiveVerifyTestStore(t)
	ctx := context.Background()
	verify, err := s.CreateArchiveReconcile(ctx, r.SiteID, r.DatasetID, archiveVerifyTestRequest(now, "1", 2), "tester")
	if err != nil {
		t.Fatal(err)
	}
	backfill, err := s.CreateArchiveBackfill(ctx, r.SiteID, r.DatasetID, ac.BackfillRequest{RequestID: strings.Repeat("2", 32), Date: now.AddDate(0, 0, -3).Format("2006-01-02")}, "tester")
	if err != nil {
		t.Fatal(err)
	}
	out := archiveVerifyStart(t, s, r, &st)
	if out.ReconcileTask == nil {
		t.Fatalf("FIFO verify missing: %+v", out)
	}
	report := archiveVerifyReport(verify, st.Foundation.WriterEpoch)
	st.Reconcile = &report
	if out, err = s.PollLogArchive(ctx, r.SiteID, st); err != nil || !out.ReconcileAccepted {
		t.Fatalf("initial target progress not accepted: %+v %v", out, err)
	}
	report.State, report.ErrorCode, report.RetryAfterUnix = "retry_wait", "source_unavailable", now.Add(900*time.Second).Unix()
	st.Reconcile = &report
	out, err = s.PollLogArchive(ctx, r.SiteID, st)
	if err != nil || !out.ReconcileAccepted || out.BackfillTask == nil || out.BackfillTask.TaskID != backfill.TaskID || out.ReconcileTask != nil {
		t.Fatalf("backoff monopolized writer: %+v %v", out, err)
	}
	var deadline, replay time.Time
	if err = db.QueryRow(`SELECT next_attempt_at FROM archive_tasks WHERE task_id=?`, archiveIDBytes(verify.TaskID)).Scan(&deadline); err != nil {
		t.Fatal(err)
	}
	if _, err = s.PollLogArchive(ctx, r.SiteID, st); err != nil {
		t.Fatal(err)
	}
	if err = db.QueryRow(`SELECT next_attempt_at FROM archive_tasks WHERE task_id=?`, archiveIDBytes(verify.TaskID)).Scan(&replay); err != nil || !deadline.Equal(replay) || !deadline.Equal(time.Unix(report.RetryAfterUnix, 0)) {
		t.Fatalf("replay changed target backoff: %s %s %v", deadline, replay, err)
	}
	finished := archiveBackfillComplete(backfill.BackfillTask, st.Foundation.WriterEpoch, now, 1)
	st.Backfill = &finished
	if _, err = s.PollLogArchive(ctx, r.SiteID, st); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(`UPDATE archive_tasks SET next_attempt_at=UTC_TIMESTAMP(6)-INTERVAL 1 SECOND WHERE task_id=?`, archiveIDBytes(verify.TaskID)); err != nil {
		t.Fatal(err)
	}
	st.Foundation.Capabilities = []string{af.CapabilityFoundation, af.CapabilityAtomicWriter, af.CapabilityBackfill}
	out, err = s.PollLogArchive(ctx, r.SiteID, st)
	if err != nil || out.ReconcileTask != nil || out.ReconcileAccepted {
		t.Fatalf("noncapable Agent received verification: %+v %v", out, err)
	}
	st.Foundation.Capabilities = append(st.Foundation.Capabilities, af.CapabilityReconcile, af.CapabilitySeal)
	out, err = s.PollLogArchive(ctx, r.SiteID, st)
	if err != nil || out.ReconcileTask == nil || out.ReconcileTask.Attempt != 1 {
		t.Fatalf("automatic retry lost original attempt: %+v %v", out, err)
	}
	report.ProgressVersion++
	report.RetryAfterUnix = 9223372036854775807
	if _, err = s.PollLogArchive(ctx, r.SiteID, st); err != nil {
		t.Fatal(err)
	}
	var seconds int64
	if err = db.QueryRow(`SELECT TIMESTAMPDIFF(SECOND,UTC_TIMESTAMP(6),next_attempt_at) FROM archive_tasks WHERE task_id=?`, archiveIDBytes(verify.TaskID)).Scan(&seconds); err != nil || seconds < 890 || seconds > 900 {
		t.Fatalf("far-future deadline unbounded: %d %v", seconds, err)
	}
}

func TestArchiveVerificationRechecksRetentionAndAssurance(t *testing.T) {
	s, db, r, st, _, now := archiveVerifyTestStore(t)
	ctx := context.Background()
	cleared, err := s.CreateArchiveReconcile(ctx, r.SiteID, r.DatasetID, archiveVerifyTestRequest(now, "1", 4), "tester")
	if err != nil {
		t.Fatal(err)
	}
	expired, err := s.CreateArchiveReconcile(ctx, r.SiteID, r.DatasetID, archiveVerifyTestRequest(now, "2", 2), "tester")
	if err != nil {
		t.Fatal(err)
	}
	p, err := s.GetArchiveCoveragePolicy(ctx, r.SiteID, r.DatasetID)
	if err != nil {
		t.Fatal(err)
	}
	p.SourceRetainedFrom = now.AddDate(0, 0, -3).Format("2006-01-02")
	if _, err = s.UpdateArchiveCoveragePolicy(ctx, r.SiteID, r.DatasetID, p.CoveragePolicy, "tester"); err != nil {
		t.Fatal(err)
	}
	// Move the persisted expiry past database time instead of sleeping for
	// an hour. It remains a syntactically valid assurance for the old day.
	expired.Assurance.ValidUntilUnix = now.Add(-time.Second).Unix()
	raw, _ := json.Marshal(expired.ReconcileTask)
	if _, err = db.Exec(`UPDATE archive_tasks SET parameters_json=? WHERE task_id=?`, string(raw), archiveIDBytes(expired.TaskID)); err != nil {
		t.Fatal(err)
	}
	out := archiveVerifyStart(t, s, r, &st)
	if out.ReconcileTask != nil {
		t.Fatalf("cleared source dispatched: %+v", out)
	}
	out, err = s.PollLogArchive(ctx, r.SiteID, st)
	if err != nil || out.ReconcileTask != nil {
		t.Fatalf("expired assurance dispatched: %+v %v", out, err)
	}
	items, err := s.ListArchiveReconciles(ctx, r.SiteID, r.DatasetID)
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range items {
		want := "verification_expired"
		if item.TaskID == cleared.TaskID {
			want = "source_cleared"
		}
		if item.State != "blocked" || item.ErrorCode != want {
			t.Fatalf("unexpected protection: %+v", item)
		}
		if _, err = s.RetryArchiveReconcile(ctx, r.SiteID, r.DatasetID, item.TaskID, "tester"); !errors.Is(err, af.ErrConflict) {
			t.Fatalf("operator retry bypassed protection: %v", err)
		}
	}
}

func TestArchiveSealRetryCannotOrphanFrozenBuild(t *testing.T) {
	s, db, r, st, _, now := archiveVerifyTestStore(t)
	ctx := context.Background()
	task, err := s.CreateArchiveSeal(ctx, r.SiteID, r.DatasetID, ac.SealRequest{RequestID: strings.Repeat("1", 32), Dates: []string{now.AddDate(0, 0, -2).Format("2006-01-02")}}, "tester")
	if err != nil {
		t.Fatal(err)
	}
	archiveVerifyStart(t, s, r, &st)
	report := af.SealStatus{TaskID: task.TaskID, BuildID: strings.Repeat("b", 32), Attempt: 1, WriterEpoch: st.Foundation.WriterEpoch, ProgressVersion: 1, State: "running"}
	st.Seal = &report
	if out, err := s.PollLogArchive(ctx, r.SiteID, st); err != nil || !out.SealAccepted {
		t.Fatalf("initial seal progress not accepted: %+v %v", out, err)
	}
	report.State, report.ErrorCode, report.RetryAfterUnix = "retry_wait", "target_unavailable", now.Add(time.Minute).Unix()
	if out, err := s.PollLogArchive(ctx, r.SiteID, st); err != nil || !out.SealAccepted {
		t.Fatalf("retry report not accepted: %+v %v", out, err)
	}
	if _, err = s.RetryArchiveSeal(ctx, r.SiteID, r.DatasetID, task.TaskID, "tester"); !errors.Is(err, af.ErrConflict) {
		t.Fatalf("manual retry orphaned frozen build: %v", err)
	}
	if _, err = db.Exec(`UPDATE archive_tasks SET next_attempt_at=UTC_TIMESTAMP(6)-INTERVAL 1 SECOND WHERE task_id=?`, archiveIDBytes(task.TaskID)); err != nil {
		t.Fatal(err)
	}
	if out, err := s.PollLogArchive(ctx, r.SiteID, st); err != nil || out.SealTask == nil || out.SealTask.Attempt != 1 {
		t.Fatalf("frozen build not resumed in same attempt: %+v %v", out, err)
	}
	report.State, report.ErrorCode, report.RetryAfterUnix = "blocked", "build_conflict", 0
	report.ProgressVersion++
	if out, err := s.PollLogArchive(ctx, r.SiteID, st); err != nil || !out.SealAccepted {
		t.Fatalf("abandoned build not accepted: %+v %v", out, err)
	}
	if item, err := s.RetryArchiveSeal(ctx, r.SiteID, r.DatasetID, task.TaskID, "tester"); err != nil || item.Attempt != 2 {
		t.Fatalf("abandoned build retry failed: %+v %v", item, err)
	}
}
