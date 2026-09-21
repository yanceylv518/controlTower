package mysqlstore

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"testing"
	"time"

	af "controltower/internal/archivecontract"
	ac "controltower/internal/archivecontrol"
)

func archiveBackfillTestStore(t *testing.T) (Store, *sql.DB, af.Registration, ac.Status, ac.Config, time.Time) {
	t.Helper()
	s, db, r, st, c := archiveWriterControlTestStore(t)
	st.Foundation.Capabilities = append(st.Foundation.Capabilities, af.CapabilityBackfill)
	var now time.Time
	if err := db.QueryRow(`SELECT UTC_TIMESTAMP(6)`).Scan(&now); err != nil {
		t.Fatal(err)
	}
	return s, db, r, st, c, now.In(archiveBeijing)
}

func archiveBackfillConfirm(t *testing.T, s Store, r af.Registration, now time.Time, retained bool) af.CoveragePolicy {
	t.Helper()
	p := af.DefaultCoveragePolicy()
	p.CoverageFrom = now.AddDate(0, 0, -10).Format("2006-01-02")
	p.Evidence = "operator confirmed the retained daily source interval"
	p.RecentDays = 0
	p.Budget.MaxRows, p.Budget.MaxBytes, p.Budget.MaxRowBytes = 37, 4096, 4096
	if retained {
		p.SourceRetainedFrom = p.CoverageFrom
	}
	saved, err := s.UpdateArchiveCoveragePolicy(context.Background(), r.SiteID, r.DatasetID, p, "tester")
	if err != nil {
		t.Fatal(err)
	}
	return saved.CoveragePolicy
}

func archiveBackfillStart(t *testing.T, s Store, r af.Registration, st *ac.Status) ac.Response {
	t.Helper()
	out, err := s.PollLogArchive(context.Background(), r.SiteID, *st)
	if err != nil || out.WriterGrant == nil {
		t.Fatalf("grant: %+v %v", out, err)
	}
	if out.BackfillTask != nil {
		t.Fatal("bootstrap epoch dispatched task before target claim")
	}
	if out.ArchivePolicy == nil || out.ArchivePolicy.Revision == 0 || out.ArchivePolicy.Budget.MaxBytes != 4096 || out.ArchivePolicy.Budget.MaxRows != 37 {
		t.Fatal("bootstrap grant omitted explicitly configured scan policy")
	}
	st.Foundation.WriterEpoch = out.WriterGrant.WriterEpoch
	out, err = s.PollLogArchive(context.Background(), r.SiteID, *st)
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func archiveBackfillComplete(task af.BackfillTask, epoch uint64, now time.Time, rows uint64) af.BackfillStatus {
	from, _, _ := af.DateBounds(task.Date)
	v := af.BackfillStatus{TaskID: task.TaskID, Date: task.Date, Attempt: task.Attempt, WriterEpoch: epoch, State: "succeeded", ScannedRows: rows, SourceNowUnix: now.Unix(), BatchID: strings.Repeat("e", 32), EmptyCandidate: rows == 0}
	if rows > 0 {
		v.AfterID = 9007199254740993
		v.AfterCreatedUnix = from + 1
	}
	return v
}

func TestArchiveBackfillPolicyAndRequestIdempotency(t *testing.T) {
	s, _, r, _, _, now := archiveBackfillTestStore(t)
	ctx := context.Background()
	month, err := s.GetArchiveCoverage(ctx, r.SiteID, r.DatasetID, now.Format("2006-01"))
	if err != nil {
		t.Fatal(err)
	}
	if month.Policy.CoverageFrom != "" || month.Policy.Revision != 0 {
		t.Fatal("source rows inferred coverage policy")
	}
	for _, day := range month.Days {
		if day.Date < now.Format("2006-01-02") && day.State != "unknown_history" {
			t.Fatalf("unconfirmed history: %+v", day)
		}
	}
	p := archiveBackfillConfirm(t, s, r, now, true)
	if p.Revision != 1 {
		t.Fatalf("policy revision %d", p.Revision)
	}
	p.Revision = 0
	if _, err = s.UpdateArchiveCoveragePolicy(ctx, r.SiteID, r.DatasetID, p, "tester"); !errors.Is(err, af.ErrConflict) {
		t.Fatalf("stale policy overwrite: %v", err)
	}
	request := ac.BackfillRequest{RequestID: strings.Repeat("1", 32), Date: now.AddDate(0, 0, -2).Format("2006-01-02")}
	a, err := s.CreateArchiveBackfill(ctx, r.SiteID, r.DatasetID, request, "tester")
	if err != nil {
		t.Fatal(err)
	}
	b, err := s.CreateArchiveBackfill(ctx, r.SiteID, r.DatasetID, request, "tester")
	if err != nil || a.TaskID != b.TaskID {
		t.Fatalf("idempotent request: %+v %v", b, err)
	}
	request.Date = now.AddDate(0, 0, -3).Format("2006-01-02")
	if _, err = s.CreateArchiveBackfill(ctx, r.SiteID, r.DatasetID, request, "tester"); !errors.Is(err, af.ErrConflict) {
		t.Fatalf("request key reused for another date: %v", err)
	}
	request.RequestID = strings.Repeat("2", 32)
	request.Date = now.Format("2006-01-02")
	if _, err = s.CreateArchiveBackfill(ctx, r.SiteID, r.DatasetID, request, "tester"); !errors.Is(err, af.ErrConflict) {
		t.Fatalf("today queued: %v", err)
	}
	request.Date = now.AddDate(0, 0, -11).Format("2006-01-02")
	cleared, err := s.CreateArchiveBackfill(ctx, r.SiteID, r.DatasetID, request, "tester")
	if err != nil || cleared.State != "blocked" || cleared.ErrorCode != "source_cleared" {
		t.Fatalf("cleared source: %+v %v", cleared, err)
	}
	if _, err = s.RetryArchiveBackfill(ctx, r.SiteID, r.DatasetID, cleared.TaskID, "tester"); !errors.Is(err, af.ErrConflict) {
		t.Fatalf("source-cleared retry: %v", err)
	}
	if _, err = s.ListArchiveBackfills(ctx, "foreign-site", r.DatasetID); !errors.Is(err, af.ErrNotFound) {
		t.Fatalf("cross site listing: %v", err)
	}
}

func TestArchiveBackfillDispatchCompletionAndPause(t *testing.T) {
	s, db, r, st, c, now := archiveBackfillTestStore(t)
	ctx := context.Background()
	archiveBackfillConfirm(t, s, r, now, true)
	request := ac.BackfillRequest{RequestID: strings.Repeat("1", 32), Date: now.AddDate(0, 0, -2).Format("2006-01-02")}
	task, err := s.CreateArchiveBackfill(ctx, r.SiteID, r.DatasetID, request, "tester")
	if err != nil {
		t.Fatal(err)
	}
	out := archiveBackfillStart(t, s, r, &st)
	if out.BackfillTask == nil || out.BackfillTask.TaskID != task.TaskID {
		t.Fatalf("manual task not dispatched: %+v", out)
	}
	report := archiveBackfillComplete(task.BackfillTask, st.Foundation.WriterEpoch, now, 2)
	st.Backfill = &report
	c.Running = false
	if err = s.UpdateLogArchive(ctx, r.SiteID, c, "tester"); err != nil {
		t.Fatal(err)
	}
	c.Version++
	st.AppliedVersion = c.Version
	out, err = s.PollLogArchive(ctx, r.SiteID, st)
	if err != nil || out.Granted || out.BackfillAccepted || out.BackfillTask != nil {
		t.Fatalf("pause accepted/dispatched: %+v %v", out, err)
	}
	items, err := s.ListArchiveBackfills(ctx, r.SiteID, r.DatasetID)
	if err != nil || items[0].State != "running" {
		t.Fatalf("pause changed target report: %+v %v", items, err)
	}
	if _, err = db.Exec(`UPDATE site_log_archive_control SET lease_until=UTC_TIMESTAMP(6)-INTERVAL 1 SECOND WHERE site_id=?`, r.SiteID); err != nil {
		t.Fatal(err)
	}
	c.Running = true
	if err = s.UpdateLogArchive(ctx, r.SiteID, c, "tester"); err != nil {
		t.Fatal(err)
	}
	c.Version++
	st.AppliedVersion = c.Version
	out, err = s.PollLogArchive(ctx, r.SiteID, st)
	if err != nil || out.WriterGrant == nil || out.BackfillTask != nil || out.BackfillAccepted {
		t.Fatalf("old epoch after resume: %+v %v", out, err)
	}
	st.Foundation.WriterEpoch = out.WriterGrant.WriterEpoch
	out, err = s.PollLogArchive(ctx, r.SiteID, st)
	if err != nil || out.BackfillTask == nil || out.BackfillTask.TaskID != task.TaskID || out.BackfillAccepted {
		t.Fatalf("resume didn't preserve same task: %+v %v", out, err)
	}
	report.WriterEpoch = st.Foundation.WriterEpoch
	out, err = s.PollLogArchive(ctx, r.SiteID, st)
	if err != nil || !out.BackfillAccepted {
		t.Fatalf("completion not accepted: %+v %v", out, err)
	}
	out, err = s.PollLogArchive(ctx, r.SiteID, st)
	if err != nil || !out.BackfillAccepted || out.BackfillTask != nil {
		t.Fatalf("completion replay not idempotent: %+v %v", out, err)
	}
	coverage, err := s.GetArchiveCoverage(ctx, r.SiteID, r.DatasetID, request.Date[:7])
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, day := range coverage.Days {
		if day.Date == request.Date {
			found = true
			if day.State != "scanned_pending_verify" || day.Task.Progress.AfterID != 9007199254740993 {
				t.Fatalf("completion became verified or lost ID: %+v", day)
			}
		}
	}
	if !found || coverage.ArchiveBilling || coverage.DayVersions {
		t.Fatal("invalid coverage capabilities")
	}
	var count int
	if err = db.QueryRow(`SELECT COUNT(*) FROM archive_day_catalog WHERE dataset_id=?`, archiveIDBytes(r.DatasetID)).Scan(&count); err != nil || count != 0 {
		t.Fatalf("scan populated authoritative catalog: %d %v", count, err)
	}
}

func TestArchiveBackfillRetryAndStaleAttempt(t *testing.T) {
	s, db, r, st, _, now := archiveBackfillTestStore(t)
	ctx := context.Background()
	archiveBackfillConfirm(t, s, r, now, true)
	task, err := s.CreateArchiveBackfill(ctx, r.SiteID, r.DatasetID, ac.BackfillRequest{RequestID: strings.Repeat("1", 32), Date: now.AddDate(0, 0, -2).Format("2006-01-02")}, "tester")
	if err != nil {
		t.Fatal(err)
	}
	archiveBackfillStart(t, s, r, &st)
	report := af.BackfillStatus{TaskID: task.TaskID, Date: task.Date, Attempt: 1, WriterEpoch: st.Foundation.WriterEpoch, State: "retry_wait", ErrorCode: "source_unavailable"}
	st.Backfill = &report
	out, err := s.PollLogArchive(ctx, r.SiteID, st)
	if err != nil || !out.BackfillAccepted || out.BackfillTask != nil {
		t.Fatalf("retry status: %+v %v", out, err)
	}
	var deadline time.Time
	if err = db.QueryRow(`SELECT next_attempt_at FROM archive_tasks WHERE task_id=?`, archiveIDBytes(task.TaskID)).Scan(&deadline); err != nil {
		t.Fatal(err)
	}
	if _, err = s.PollLogArchive(ctx, r.SiteID, st); err != nil {
		t.Fatal(err)
	}
	var replayDeadline time.Time
	_ = db.QueryRow(`SELECT next_attempt_at FROM archive_tasks WHERE task_id=?`, archiveIDBytes(task.TaskID)).Scan(&replayDeadline)
	if !deadline.Equal(replayDeadline) {
		t.Fatal("duplicate retry report extended backoff")
	}
	if _, err = db.Exec(`UPDATE archive_tasks SET next_attempt_at=UTC_TIMESTAMP(6)-INTERVAL 1 SECOND WHERE task_id=?`, archiveIDBytes(task.TaskID)); err != nil {
		t.Fatal(err)
	}
	out, err = s.PollLogArchive(ctx, r.SiteID, st)
	if err != nil || out.BackfillTask == nil || out.BackfillTask.Attempt != 1 {
		t.Fatalf("automatic retry reset attempt: %+v %v", out, err)
	}
	out, err = s.PollLogArchive(ctx, r.SiteID, st)
	if err != nil || out.BackfillTask == nil {
		t.Fatalf("cached failure re-entered backoff: %+v %v", out, err)
	}
	report.State, report.ErrorCode = "blocked", "row_too_large"
	if _, err = s.PollLogArchive(ctx, r.SiteID, st); err != nil {
		t.Fatal(err)
	}
	retried, err := s.RetryArchiveBackfill(ctx, r.SiteID, r.DatasetID, task.TaskID, "tester")
	if err != nil || retried.Attempt != 2 {
		t.Fatalf("manual retry: %+v %v", retried, err)
	}
	out, err = s.PollLogArchive(ctx, r.SiteID, st)
	if err != nil || out.BackfillAccepted || out.BackfillTask == nil || out.BackfillTask.Attempt != 2 {
		t.Fatalf("old attempt accepted: %+v %v", out, err)
	}
}

func TestArchiveBackfillRecentRoundsAndUnknownEmpty(t *testing.T) {
	s, db, r, st, _, now := archiveBackfillTestStore(t)
	ctx := context.Background()
	p := archiveBackfillConfirm(t, s, r, now, false)
	p.RecentDays = 2
	if _, err := s.UpdateArchiveCoveragePolicy(ctx, r.SiteID, r.DatasetID, p, "tester"); err != nil {
		t.Fatal(err)
	}
	out := archiveBackfillStart(t, s, r, &st)
	if out.BackfillTask == nil || out.BackfillTask.Type != "recent_backfill" {
		t.Fatalf("missing recent task: %+v", out)
	}
	first := *out.BackfillTask
	report := archiveBackfillComplete(first, st.Foundation.WriterEpoch, now, 0)
	st.Backfill = &report
	out, err := s.PollLogArchive(ctx, r.SiteID, st)
	if err != nil || !out.BackfillAccepted {
		t.Fatalf("empty completion: %+v %v", out, err)
	}
	coverage, err := s.GetArchiveCoverage(ctx, r.SiteID, r.DatasetID, first.Date[:7])
	if err != nil {
		t.Fatal(err)
	}
	for _, day := range coverage.Days {
		if day.Date == first.Date && (day.State != "unknown_history" || day.BlockReason != "source_history_unknown") {
			t.Fatalf("unknown retention became empty proof: %+v", day)
		}
	}
	var before int
	_ = db.QueryRow(`SELECT COUNT(*) FROM archive_tasks WHERE dataset_id=? AND log_date=?`, archiveIDBytes(r.DatasetID), first.Date).Scan(&before)
	if _, err = s.PollLogArchive(ctx, r.SiteID, st); err != nil {
		t.Fatal(err)
	}
	var after int
	_ = db.QueryRow(`SELECT COUNT(*) FROM archive_tasks WHERE dataset_id=? AND log_date=?`, archiveIDBytes(r.DatasetID), first.Date).Scan(&after)
	if before != after {
		t.Fatal("recent date restarted before six hours")
	}
	// Simulate the prior completed task belonging to an earlier scheduler bucket.
	if _, err = db.Exec(`UPDATE archive_tasks SET created_at=UTC_TIMESTAMP(6)-INTERVAL 7 HOUR,request_key=RANDOM_BYTES(32) WHERE task_id=?`, archiveIDBytes(first.TaskID)); err != nil {
		t.Fatal(err)
	}
	if _, err = s.PollLogArchive(ctx, r.SiteID, st); err != nil {
		t.Fatal(err)
	}
	_ = db.QueryRow(`SELECT COUNT(*) FROM archive_tasks WHERE dataset_id=? AND log_date=?`, archiveIDBytes(r.DatasetID), first.Date).Scan(&after)
	if after != before+1 {
		t.Fatalf("new recent round missing: before %d after %d", before, after)
	}
}

func TestArchiveBackfillRejectsForeignAndRegressingReports(t *testing.T) {
	s, _, r, st, _, now := archiveBackfillTestStore(t)
	ctx := context.Background()
	archiveBackfillConfirm(t, s, r, now, true)
	task, err := s.CreateArchiveBackfill(ctx, r.SiteID, r.DatasetID, ac.BackfillRequest{RequestID: strings.Repeat("1", 32), Date: now.AddDate(0, 0, -2).Format("2006-01-02")}, "tester")
	if err != nil {
		t.Fatal(err)
	}
	archiveBackfillStart(t, s, r, &st)
	report := archiveBackfillComplete(task.BackfillTask, st.Foundation.WriterEpoch, now, 2)
	report.State, report.CatalogRevision = "running", 10
	st.Backfill = &report
	if out, e := s.PollLogArchive(ctx, r.SiteID, st); e != nil || !out.BackfillAccepted {
		t.Fatalf("progress: %+v %v", out, e)
	}
	for name, mutate := range map[string]func(*af.BackfillStatus){
		"foreign task": func(p *af.BackfillStatus) { p.TaskID = strings.Repeat("f", 32) },
		"different date": func(p *af.BackfillStatus) {
			p.Date = now.AddDate(0, 0, -3).Format("2006-01-02")
			p.AfterCreatedUnix, _, _ = af.DateBounds(p.Date)
		},
		"future epoch":           func(p *af.BackfillStatus) { p.WriterEpoch++ },
		"wrong attempt":          func(p *af.BackfillStatus) { p.Attempt++ },
		"regressing rows":        func(p *af.BackfillStatus) { p.ScannedRows-- },
		"regressing cursor":      func(p *af.BackfillStatus) { p.AfterID-- },
		"regressing catalog":     func(p *af.BackfillStatus) { p.CatalogRevision-- },
		"unsupported completion": func(p *af.BackfillStatus) { p.State = "completed" },
		"missing receipt":        func(p *af.BackfillStatus) { p.State = "succeeded"; p.BatchID = "" },
	} {
		t.Run(name, func(t *testing.T) {
			bad := report
			mutate(&bad)
			st.Backfill = &bad
			out, e := s.PollLogArchive(ctx, r.SiteID, st)
			if e != nil || out.BackfillAccepted {
				t.Fatalf("invalid progress accepted: %+v %v", out, e)
			}
			items, e := s.ListArchiveBackfills(ctx, r.SiteID, r.DatasetID)
			if e != nil || items[0].Progress == nil || *items[0].Progress != report {
				t.Fatalf("invalid progress overwrote committed report: %+v %v", items, e)
			}
		})
	}
}

func TestArchiveBackfillRecentTaskAvoidsManualStarvation(t *testing.T) {
	s, db, r, st, _, now := archiveBackfillTestStore(t)
	ctx := context.Background()
	p := archiveBackfillConfirm(t, s, r, now, true)
	p.RecentDays = 3
	if _, err := s.UpdateArchiveCoveragePolicy(ctx, r.SiteID, r.DatasetID, p, "tester"); err != nil {
		t.Fatal(err)
	}
	manual, err := s.CreateArchiveBackfill(ctx, r.SiteID, r.DatasetID, ac.BackfillRequest{RequestID: strings.Repeat("1", 32), Date: now.AddDate(0, 0, -4).Format("2006-01-02")}, "tester")
	if err != nil {
		t.Fatal(err)
	}
	out := archiveBackfillStart(t, s, r, &st)
	if out.BackfillTask == nil || out.BackfillTask.TaskID != manual.TaskID {
		t.Fatalf("manual task did not receive initial priority: %+v", out)
	}
	if _, err = s.CreateArchiveBackfill(ctx, r.SiteID, r.DatasetID, ac.BackfillRequest{RequestID: strings.Repeat("2", 32), Date: now.AddDate(0, 0, -5).Format("2006-01-02")}, "tester"); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(`UPDATE archive_tasks SET created_at=UTC_TIMESTAMP(6)-INTERVAL 31 MINUTE WHERE dataset_id=? AND task_type='recent_backfill'`, archiveIDBytes(r.DatasetID)); err != nil {
		t.Fatal(err)
	}
	report := archiveBackfillComplete(manual.BackfillTask, st.Foundation.WriterEpoch, now, 1)
	st.Backfill = &report
	out, err = s.PollLogArchive(ctx, r.SiteID, st)
	if err != nil || !out.BackfillAccepted || out.BackfillTask == nil || out.BackfillTask.Type != "recent_backfill" {
		t.Fatalf("manual traffic starved recent scan: %+v %v", out, err)
	}
}

func TestArchiveBackfillTargetBackoffLeavesOtherDatesRunnable(t *testing.T) {
	s, db, r, st, _, now := archiveBackfillTestStore(t)
	ctx := context.Background()
	archiveBackfillConfirm(t, s, r, now, true)
	first, err := s.CreateArchiveBackfill(ctx, r.SiteID, r.DatasetID, ac.BackfillRequest{RequestID: strings.Repeat("1", 32), Date: now.AddDate(0, 0, -2).Format("2006-01-02")}, "tester")
	if err != nil {
		t.Fatal(err)
	}
	second, err := s.CreateArchiveBackfill(ctx, r.SiteID, r.DatasetID, ac.BackfillRequest{RequestID: strings.Repeat("2", 32), Date: now.AddDate(0, 0, -3).Format("2006-01-02")}, "tester")
	if err != nil {
		t.Fatal(err)
	}
	out := archiveBackfillStart(t, s, r, &st)
	if out.BackfillTask == nil || out.BackfillTask.TaskID != first.TaskID {
		t.Fatalf("first date not dispatched: %+v", out)
	}
	retry := af.BackfillStatus{TaskID: first.TaskID, Date: first.Date, Attempt: 1, WriterEpoch: st.Foundation.WriterEpoch, State: "retry_wait", ErrorCode: "source_unavailable", RetryAfterUnix: now.Add(900 * time.Second).Unix()}
	st.Backfill = &retry
	out, err = s.PollLogArchive(ctx, r.SiteID, st)
	if err != nil || !out.BackfillAccepted || out.BackfillTask == nil || out.BackfillTask.TaskID != second.TaskID {
		t.Fatalf("target backoff blocked another date: %+v %v", out, err)
	}
	var deadline time.Time
	if err = db.QueryRow(`SELECT next_attempt_at FROM archive_tasks WHERE task_id=?`, archiveIDBytes(first.TaskID)).Scan(&deadline); err != nil {
		t.Fatal(err)
	}
	if !deadline.Equal(time.Unix(retry.RetryAfterUnix, 0)) {
		t.Fatalf("target deadline shortened: got %s want %s", deadline, time.Unix(retry.RetryAfterUnix, 0))
	}
	if _, err = s.PollLogArchive(ctx, r.SiteID, st); err != nil {
		t.Fatal(err)
	}
	var replayDeadline time.Time
	if err = db.QueryRow(`SELECT next_attempt_at FROM archive_tasks WHERE task_id=?`, archiveIDBytes(first.TaskID)).Scan(&replayDeadline); err != nil || !deadline.Equal(replayDeadline) {
		t.Fatalf("replayed retry extended deadline: %s %v", replayDeadline, err)
	}
	complete := archiveBackfillComplete(second.BackfillTask, st.Foundation.WriterEpoch, now, 1)
	st.Backfill = &complete
	out, err = s.PollLogArchive(ctx, r.SiteID, st)
	if err != nil || !out.BackfillAccepted || out.BackfillTask != nil {
		t.Fatalf("long-backoff date dispatched early: %+v %v", out, err)
	}
	// Advance the persisted deadline instead of sleeping for the target's
	// fifteen-minute backoff; the dispatch still uses CT database time.
	if _, err = db.Exec(`UPDATE archive_tasks SET next_attempt_at=UTC_TIMESTAMP(6)-INTERVAL 1 SECOND WHERE task_id=?`, archiveIDBytes(first.TaskID)); err != nil {
		t.Fatal(err)
	}
	out, err = s.PollLogArchive(ctx, r.SiteID, st)
	if err != nil || out.BackfillTask == nil || out.BackfillTask.TaskID != first.TaskID || out.BackfillTask.Attempt != 1 {
		t.Fatalf("deadline did not resume original attempt: %+v %v", out, err)
	}
	// An anomalous far-future source clock is clamped to CT's 900-second
	// ceiling rather than pinning the task indefinitely.
	retry.RetryAfterUnix = 9223372036854775807
	st.Backfill = &retry
	if _, err = s.PollLogArchive(ctx, r.SiteID, st); err != nil {
		t.Fatal(err)
	}
	var seconds int64
	if err = db.QueryRow(`SELECT TIMESTAMPDIFF(SECOND,UTC_TIMESTAMP(6),next_attempt_at) FROM archive_tasks WHERE task_id=?`, archiveIDBytes(first.TaskID)).Scan(&seconds); err != nil || seconds < 890 || seconds > 900 {
		t.Fatalf("future retry not clamped: %d %v", seconds, err)
	}
}
