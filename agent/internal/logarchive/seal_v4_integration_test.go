package logarchive

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	af "controltower/internal/archivecontract"
)

func sealV4Fixture(t *testing.T) (*Worker, context.Context, af.WriterGrant, af.ReconcileTask, af.SealTask) {
	t.Helper()
	w, ctx, g, r, b := reconcileV4Fixture(t)
	from, _, _ := af.DateBounds(r.Date)
	scanRow(t, w, ctx, 1, from+1, `{"model_ratio":1}`)
	if _, err := w.ScanDateV3(ctx, g, b); err != nil {
		t.Fatal(err)
	}
	if s := completeReconcileV4(t, w, ctx, g, r); s.State != "matched" {
		t.Fatalf("fixture verification: %+v", s)
	}
	return w, ctx, g, r, af.SealTask{Identity: g.Identity, TaskID: strings.Repeat("e", 32), Dates: []string{r.Date}, Attempt: 1, Policy: r.Policy}
}

func completeSealV4(t *testing.T, w *Worker, ctx context.Context, g af.WriterGrant, task af.SealTask) af.SealStatus {
	t.Helper()
	for i := 0; i < 100; i++ {
		s, err := w.SealDaysV4(ctx, g, task)
		if err != nil {
			t.Fatalf("seal failed: %+v %v", s, err)
		}
		if s.Validate() != nil {
			t.Fatalf("invalid seal status: %+v", s)
		}
		if s.State != "running" {
			return s
		}
	}
	t.Fatal("seal did not complete")
	return af.SealStatus{}
}

func TestSealV4MySQL(t *testing.T) {
	t.Run("publishes_immutable_fact_evidence_and_canonical_manifest", func(t *testing.T) {
		w, ctx, g, r, task := sealV4Fixture(t)
		s := completeSealV4(t, w, ctx, g, task)
		if s.State != "succeeded" || len(s.Versions) != 1 || s.Versions[0].VersionNo != 1 {
			t.Fatalf("not published: %+v", s)
		}
		var manifest, hash []byte
		var state string
		if err := w.target.QueryRowContext(ctx, "SELECT manifest_json,manifest_hash,state FROM archive_day_versions WHERE day_version_id=?", archiveID(s.Versions[0].VersionID)).Scan(&manifest, &hash, &state); err != nil {
			t.Fatal(err)
		}
		digest, err := CanonicalManifestHash(manifest)
		if err != nil || !bytes.Equal(hash, digest[:]) || hex.EncodeToString(hash) != s.Versions[0].ManifestHash || state != "published" {
			t.Fatal("normalized manifest hash mismatch")
		}
		if writerTestScalar(t, ctx, w.target, "SELECT COUNT(*) FROM billing_facts_202609 WHERE day_version_id=?", archiveID(s.Versions[0].VersionID)) != 1 || writerTestScalar(t, ctx, w.target, "SELECT COUNT(*) FROM archive_billing_evidence_202609") != 1 {
			t.Fatal("fact/evidence not persisted")
		}
		if writerTestScalar(t, ctx, w.target, "SELECT COUNT(*) FROM archive_days WHERE state='sealed' AND freeze_task_id IS NULL") != 1 {
			t.Fatal("publication not visible or freeze leaked")
		}
		var firstFactHash []byte
		if err := w.target.QueryRowContext(ctx, "SELECT fact_hash FROM billing_facts_202609 WHERE day_version_id=?", archiveID(s.Versions[0].VersionID)).Scan(&firstFactHash); err != nil {
			t.Fatal(err)
		}
		if _, err := w.source.ExecContext(ctx, "UPDATE logs SET quota=70,other='{\"model_ratio\":2}' WHERE id=1"); err != nil {
			t.Fatal(err)
		}
		b := af.BackfillTask{Identity: g.Identity, TaskID: strings.Repeat("f", 32), Date: r.Date, Attempt: 1, Type: "date_backfill", Policy: r.Policy}
		if _, err := w.ScanDateV3(ctx, g, b); err != nil {
			t.Fatal(err)
		}
		r.Attempt++
		if verified := completeReconcileV4(t, w, ctx, g, r); verified.State != "matched" {
			t.Fatal(verified)
		}
		task.Attempt++
		second := completeSealV4(t, w, ctx, g, task)
		if second.State != "succeeded" || second.Versions[0].VersionNo != 2 {
			t.Fatalf("revision: %+v", second)
		}
		var old []byte
		if err := w.target.QueryRowContext(ctx, "SELECT fact_hash FROM billing_facts_202609 WHERE day_version_id=?", archiveID(s.Versions[0].VersionID)).Scan(&old); err != nil || !bytes.Equal(old, firstFactHash) {
			t.Fatal("prior facts changed")
		}
		if writerTestScalar(t, ctx, w.target, "SELECT COUNT(*) FROM archive_day_versions WHERE previous_version_id=? AND state='published'", archiveID(s.Versions[0].VersionID)) != 1 {
			t.Fatal("version ancestry missing")
		}
	})
	t.Run("freeze_blocks_writes_and_resume_new_epoch_after_expiry", func(t *testing.T) {
		w, ctx, g, r, task := sealV4Fixture(t)
		first, err := w.SealDaysV4(ctx, g, task)
		if err != nil || first.State != "running" {
			t.Fatalf("build: %+v %v", first, err)
		}
		if writerTestScalar(t, ctx, w.target, "SELECT COUNT(*) FROM archive_days WHERE current_version_id IS NOT NULL") != 0 {
			t.Fatal("building version visible")
		}
		from, _, _ := af.DateBounds(r.Date)
		queued := reviewWriterBatch()
		queued.Rows[0][0] = "2"
		queued.Rows[0][1] = fmt.Sprint(from + 2)
		queued.AfterID = 2
		if _, err := w.commitWriterBatch(ctx, g, queued, writerFaultHooks{}); !errors.Is(err, ErrWriterFrozen) {
			t.Fatalf("freeze ignored: %v", err)
		}
		if _, err := w.target.ExecContext(ctx, "UPDATE archive_days SET freeze_until=DATE_SUB(UTC_TIMESTAMP(6),INTERVAL 1 SECOND)"); err != nil {
			t.Fatal(err)
		}
		restarted := &Worker{source: w.source, target: w.target, batchSize: 1, delay: w.delay}
		next := g
		next.WriterEpoch++
		next.Session = strings.Repeat("b", 32)
		if err := restarted.AcquireWriter(ctx, next, 90*time.Second); err != nil {
			t.Fatal(err)
		}
		if _, err := w.SealDaysV4(ctx, g, task); !errors.Is(err, ErrWriterLease) {
			t.Fatalf("old epoch survived: %v", err)
		}
		last := completeSealV4(t, restarted, ctx, next, task)
		if last.State != "succeeded" || last.BuildID != first.BuildID {
			t.Fatalf("restart changed build: %+v", last)
		}
	})
	for _, kind := range []string{"fact", "evidence"} {
		t.Run("audit_rejects_corrupted_"+kind, func(t *testing.T) {
			w, ctx, g, _, task := sealV4Fixture(t)
			first, err := w.SealDaysV4(ctx, g, task)
			if err != nil || first.State != "running" {
				t.Fatal(err)
			}
			q := "UPDATE billing_facts_202609 SET quota=quota+1"
			if kind == "evidence" {
				q = "UPDATE archive_billing_evidence_202609 SET payload=CONCAT(payload,'corrupt')"
			}
			if _, err := w.target.ExecContext(ctx, q); err != nil {
				t.Fatal(err)
			}
			s, err := w.SealDaysV4(ctx, g, task)
			if err == nil || s.State != "blocked" || s.ErrorCode != "fact_invalid" {
				t.Fatalf("corruption published: %+v %v", s, err)
			}
			if writerTestScalar(t, ctx, w.target, "SELECT COUNT(*) FROM archive_day_versions WHERE state='published'") != 0 || writerTestScalar(t, ctx, w.target, "SELECT COUNT(*) FROM archive_days WHERE freeze_task_id IS NOT NULL") != 0 {
				t.Fatal("corrupt build was published or stuck frozen")
			}
		})
	}
	t.Run("raw_changes_after_verify_cannot_publish", func(t *testing.T) {
		w, ctx, g, _, task := sealV4Fixture(t)
		if _, err := w.target.ExecContext(ctx, "UPDATE logs_202609 SET quota=quota+1"); err != nil {
			t.Fatal(err)
		}
		s, err := w.SealDaysV4(ctx, g, task)
		if err == nil || s.State != "blocked" || s.ErrorCode != "verification_mismatch" {
			t.Fatalf("raw drift accepted: %+v %v", s, err)
		}
	})
	t.Run("raw_schema_changed_without_column_name_change_blocks", func(t *testing.T) {
		w, ctx, g, _, task := sealV4Fixture(t)
		if _, err := w.target.ExecContext(ctx, "ALTER TABLE logs_202609 MODIFY quota DECIMAL(20,0) NULL"); err != nil {
			t.Fatal(err)
		}
		s, err := w.SealDaysV4(ctx, g, task)
		if err == nil || s.State != "blocked" || s.ErrorCode != "schema_changed" {
			t.Fatalf("schema drift accepted: %+v %v", s, err)
		}
	})
	t.Run("partial_freeze_marker_is_not_overwritten", func(t *testing.T) {
		w, ctx, g, _, task := sealV4Fixture(t)
		if _, err := w.target.ExecContext(ctx, "UPDATE archive_days SET freeze_epoch=1"); err != nil {
			t.Fatal(err)
		}
		s, err := w.SealDaysV4(ctx, g, task)
		if err == nil || s.ErrorCode != "date_frozen" {
			t.Fatalf("partial marker ignored: %+v %v", s, err)
		}
	})
	t.Run("unexpected_version_state_cannot_publish_pointer", func(t *testing.T) {
		w, ctx, g, _, task := sealV4Fixture(t)
		for i := 0; i < 2; i++ {
			if _, err := w.SealDaysV4(ctx, g, task); err != nil {
				t.Fatal(err)
			}
		}
		if _, err := w.target.ExecContext(ctx, "UPDATE archive_day_versions SET state='abandoned'"); err != nil {
			t.Fatal(err)
		}
		s, err := w.SealDaysV4(ctx, g, task)
		if err == nil || s.State != "blocked" || s.ErrorCode != "build_conflict" {
			t.Fatalf("non-building version published: %+v %v", s, err)
		}
		if writerTestScalar(t, ctx, w.target, "SELECT COUNT(*) FROM archive_days WHERE current_version_id IS NOT NULL") != 0 {
			t.Fatal("dangling current pointer")
		}
	})
	t.Run("cross_month_cohort_publishes_atomically_after_commit_failure", func(t *testing.T) {
		w, ctx, g, r, b := reconcileV4Fixture(t)
		aug, _, _ := af.DateBounds("2026-08-31")
		sep, _, _ := af.DateBounds(r.Date)
		scanRow(t, w, ctx, 1, aug+1, `{}`)
		if _, err := w.PassV2(ctx, g); err != nil {
			t.Fatal(err)
		}
		if _, err := w.source.ExecContext(ctx, "UPDATE logs SET created_at=? WHERE id=1", sep+1); err != nil {
			t.Fatal(err)
		}
		if _, err := w.ScanDateV3(ctx, g, b); err != nil {
			t.Fatal(err)
		}
		if s := completeReconcileV4(t, w, ctx, g, r); s.State != "matched" {
			t.Fatal(s)
		}
		augRun := r
		augRun.TaskID = strings.Repeat("9", 32)
		augRun.Date = "2026-08-31"
		if s := completeReconcileV4(t, w, ctx, g, augRun); s.State != "matched" {
			t.Fatal(s)
		}
		task := af.SealTask{Identity: g.Identity, TaskID: strings.Repeat("e", 32), Dates: []string{r.Date}, Attempt: 1, Policy: r.Policy}
		partial, err := w.SealDaysV4(ctx, g, task)
		if err == nil || partial.ErrorCode != "cohort_incomplete" {
			t.Fatalf("partial correction was sealable: %+v %v", partial, err)
		}
		task.Dates = []string{"2026-08-31", r.Date}
		for i := 0; i < 4; i++ {
			s, err := w.SealDaysV4(ctx, g, task)
			if err != nil || s.State != "running" {
				t.Fatalf("staging: %+v %v", s, err)
			}
		}
		if _, err := w.target.ExecContext(ctx, `CREATE TRIGGER fail_publish BEFORE UPDATE ON archive_days FOR EACH ROW BEGIN IF NEW.log_date='2026-09-01' AND NEW.state='sealed' THEN SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT='injected publication failure'; END IF; END`); err != nil {
			t.Fatal(err)
		}
		failed, err := w.SealDaysV4(ctx, g, task)
		if err == nil || failed.State != "running" {
			t.Fatalf("expected resumable publish failure: %+v %v", failed, err)
		}
		if writerTestScalar(t, ctx, w.target, "SELECT COUNT(*) FROM archive_days WHERE current_version_id IS NOT NULL") != 0 || writerTestScalar(t, ctx, w.target, "SELECT COUNT(*) FROM archive_day_versions WHERE state='published'") != 0 {
			t.Fatal("cross-month version partially published")
		}
		if _, err := w.target.ExecContext(ctx, "DROP TRIGGER fail_publish"); err != nil {
			t.Fatal(err)
		}
		last := completeSealV4(t, w, ctx, g, task)
		if len(last.Versions) != 2 || last.State != "succeeded" {
			t.Fatalf("cohort retry: %+v", last)
		}
		if writerTestScalar(t, ctx, w.target, "SELECT COUNT(DISTINCT publish_revision) FROM archive_day_versions WHERE state='published'") != 1 {
			t.Fatal("cohort published at different revisions")
		}
	})
	t.Run("staged_subject_index_visible_only_through_published_version", func(t *testing.T) {
		w, ctx := foundationTestWorker(t)
		for _, db := range []*sql.DB{w.source, w.target} {
			if _, err := db.ExecContext(ctx, `ALTER TABLE logs ADD prompt_tokens BIGINT NULL,ADD completion_tokens BIGINT NULL,ADD user_id BIGINT NULL,ADD token_id BIGINT NULL,ADD username VARCHAR(255) NULL,ADD token_name VARCHAR(255) NULL,ADD INDEX idx_created(created_at)`); err != nil {
				t.Fatal(err)
			}
		}
		identity := foundationTestIdentity()
		if _, err := w.PrepareFoundation(ctx, identity); err != nil {
			t.Fatal(err)
		}
		g := af.WriterGrant{Identity: identity, ProtocolVersion: af.ProtocolVersion, WriterEpoch: 1, Session: strings.Repeat("3", 32), ConfigVersion: 1, LeaseSeconds: 120}
		if err := w.AcquireWriter(ctx, g, 90*time.Second); err != nil {
			t.Fatal(err)
		}
		policy := af.DefaultCoveragePolicy()
		policy.CoverageFrom = "2026-08-01"
		policy.SourceRetainedFrom = policy.CoverageFrom
		policy.Evidence = "isolated retained source"
		date := "2026-09-01"
		from, to, _ := af.DateBounds(date)
		if _, err := w.source.ExecContext(ctx, `INSERT INTO logs(id,created_at,type,quota,other,prompt_tokens,completion_tokens,user_id,token_id,username,token_name) VALUES(1,?,2,50,'{}',5,10,123,456,'historical user','historical token')`, from+1); err != nil {
			t.Fatal(err)
		}
		if _, err := w.PassV2(ctx, g); err != nil {
			t.Fatal(err)
		}
		r := af.ReconcileTask{Identity: identity, TaskID: strings.Repeat("c", 32), Date: date, Attempt: 1, Policy: policy, Assurance: af.VerificationAssurance{StableBeforeUnix: to, ValidUntilUnix: time.Now().Add(time.Hour).Unix(), Evidence: "immutable retention assertion"}}
		if s := completeReconcileV4(t, w, ctx, g, r); s.State != "matched" {
			t.Fatal(s)
		}
		task := af.SealTask{Identity: identity, TaskID: strings.Repeat("e", 32), Dates: []string{date}, Attempt: 1, Policy: policy}
		if _, err := w.SealDaysV4(ctx, g, task); err != nil {
			t.Fatal(err)
		}
		if writerTestScalar(t, ctx, w.target, "SELECT COUNT(*) FROM archive_subject_index") != 2 {
			t.Fatal("subject page did not stage")
		}
		q := "SELECT COUNT(*) FROM archive_subject_index s JOIN archive_day_versions v ON v.day_version_id=s.day_version_id WHERE v.state='published'"
		if writerTestScalar(t, ctx, w.target, q) != 0 {
			t.Fatal("building subjects were visible")
		}
		if s := completeSealV4(t, w, ctx, g, task); s.State != "succeeded" {
			t.Fatal(s)
		}
		if writerTestScalar(t, ctx, w.target, q) != 2 {
			t.Fatal("published subjects not visible")
		}
	})
}
