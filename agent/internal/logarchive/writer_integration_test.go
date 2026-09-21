package logarchive

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"controltower/internal/archivecontract"
)

func writerTestDate(t *testing.T, batch writerBatch) string {
	t.Helper()
	_, c, err := rowContribution(batch.Columns, batch.Rows[0])
	if err != nil {
		t.Fatal(err)
	}
	return c.Day
}
func writerTestScalar(t *testing.T, ctx context.Context, db *sql.DB, query string, args ...any) int64 {
	t.Helper()
	var value int64
	if err := db.QueryRowContext(ctx, query, args...).Scan(&value); err != nil {
		t.Fatal(err)
	}
	return value
}
func writerTestNextBatch(batch writerBatch, id string) writerBatch {
	batch.ID = strings.Repeat(id, 32)
	batch.BeforeID = batch.AfterID
	batch.Rows = append([][]any(nil), batch.Rows...)
	for i := range batch.Rows {
		batch.Rows[i] = append([]any(nil), batch.Rows[i]...)
	}
	return batch
}

func TestWriterAtomicMySQL(t *testing.T) {
	t.Run("source_id_above_javascript_precision_round_trips_exactly", func(t *testing.T) {
		w, ctx, grant := reviewWriterFixture(t)
		const largeID int64 = 9007199254740993
		if _, err := w.source.ExecContext(ctx, "INSERT INTO logs(id,created_at,type,quota,other,prompt_tokens,completion_tokens) VALUES(?,1788278400,2,50,'large id',5,10)", largeID); err != nil {
			t.Fatal(err)
		}
		result, err := w.PassV2(ctx, grant)
		if err != nil || result.AfterID != largeID {
			t.Fatalf("large ID pass: %+v %v", result, err)
		}
		progress, err := w.ProgressV2(ctx, grant.Identity)
		if err != nil || progress.AfterID != largeID {
			t.Fatalf("large ID progress: %+v %v", progress, err)
		}
		if got := writerTestScalar(t, ctx, w.target, "SELECT id FROM logs_202609"); got != largeID {
			t.Fatalf("large ID raw row changed: %d", got)
		}
		var cursorID string
		if err := w.target.QueryRowContext(ctx, "SELECT JSON_UNQUOTE(JSON_EXTRACT(cursor_after_json,'$.after_id')) FROM archive_batch_receipts").Scan(&cursorID); err != nil || cursorID != strconv.FormatInt(largeID, 10) {
			t.Fatalf("large ID receipt changed: %q %v", cursorID, err)
		}
	})
	t.Run("competing_connections_cannot_advance_same_cursor_twice", func(t *testing.T) {
		w, ctx, grant := reviewWriterFixture(t)
		w.target.SetMaxOpenConns(3)
		start := make(chan struct{})
		results := make(chan error, 2)
		var ready sync.WaitGroup
		ready.Add(2)
		for _, id := range []string{"4", "5"} {
			go func(id string) {
				batch := reviewWriterBatch()
				batch.ID = strings.Repeat(id, 32)
				ready.Done()
				<-start
				_, err := w.commitWriterBatch(ctx, grant, batch, writerFaultHooks{})
				results <- err
			}(id)
		}
		ready.Wait()
		close(start)
		success, conflict := 0, 0
		for i := 0; i < 2; i++ {
			err := <-results
			if err == nil {
				success++
			} else if errors.Is(err, ErrWriterCheckpoint) {
				conflict++
			} else {
				t.Fatal(err)
			}
		}
		if success != 1 || conflict != 1 {
			t.Fatalf("competing writers successes=%d conflicts=%d", success, conflict)
		}
		if writerTestScalar(t, ctx, w.target, "SELECT COUNT(*) FROM archive_batch_receipts") != 1 || writerTestScalar(t, ctx, w.target, "SELECT log_rows FROM log_daily_stats") != 1 || writerTestScalar(t, ctx, w.target, "SELECT mutation_revision FROM archive_days") != 1 {
			t.Fatal("competing writer double counted or advanced revision")
		}
	})
	for _, corruption := range []struct{ name, query string }{
		{"cursor_past_receipt", "UPDATE archive_checkpoints SET after_id=999"},
		{"missing_target_checkpoint", "DELETE FROM archive_checkpoints"},
		{"wrong_stream_type", "UPDATE archive_checkpoints SET stream_type='unknown'"},
		{"wrong_cursor_version", "UPDATE archive_checkpoints SET cursor_version=99"},
		{"receipt_cursor_mismatch", "UPDATE archive_batch_receipts SET cursor_after_json=JSON_OBJECT('after_id',999,'catalog_revision','1')"},
	} {
		t.Run("reject_"+corruption.name, func(t *testing.T) {
			w, ctx, grant := reviewWriterFixture(t)
			if _, err := w.commitWriterBatch(ctx, grant, reviewWriterBatch(), writerFaultHooks{}); err != nil {
				t.Fatal(err)
			}
			if _, err := w.target.ExecContext(ctx, corruption.query); err != nil {
				t.Fatal(err)
			}
			if _, err := w.ProgressV2(ctx, grant.Identity); err == nil {
				t.Fatal("inconsistent target checkpoint accepted")
			}
			if _, err := w.PassV2(ctx, grant); err == nil {
				t.Fatal("inconsistent target checkpoint advanced")
			}
		})
	}
	t.Run("target_cursor_ignores_missing_or_corrupt_local_cache", func(t *testing.T) {
		w, ctx, grant := reviewWriterFixture(t)
		if _, err := w.source.ExecContext(ctx, "INSERT INTO logs(id,created_at,type,quota,other,prompt_tokens,completion_tokens) VALUES(1,1788278400,2,50,NULL,5,10),(3,1788278400,2,75,'',2,4)"); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(w.path, []byte(`{"after_id":999999}`), 0600); err != nil {
			t.Fatal(err)
		}
		first, err := w.PassV2(ctx, grant)
		if err != nil {
			t.Fatal(err)
		}
		if first.AfterID != 3 || first.Rows != 2 || first.CatalogRevision != 1 || first.CommittedAt.IsZero() {
			t.Fatalf("incorrect first batch: %+v", first)
		}
		if err := os.Remove(w.path); err != nil {
			t.Fatal(err)
		}
		progress, err := w.ProgressV2(ctx, grant.Identity)
		if err != nil || progress.AfterID != 3 || progress.BatchID != first.BatchID {
			t.Fatalf("lost local cache lost progress: %+v %v", progress, err)
		}
		if err := os.WriteFile(w.path, []byte(`not a checkpoint`), 0600); err != nil {
			t.Fatal(err)
		}
		next, err := w.PassV2(ctx, grant)
		if err != nil || next.Rows != 0 || next.AfterID != 3 {
			t.Fatalf("bad idle progress: %+v %v", next, err)
		}
		if got := writerTestScalar(t, ctx, w.target, "SELECT quota FROM log_monthly_stats WHERE period_key='202609'"); got != 125 {
			t.Fatalf("quota=%d", got)
		}
		if got := writerTestScalar(t, ctx, w.target, "SELECT COUNT(*) FROM archive_batch_receipts"); got != 1 {
			t.Fatalf("receipt count=%d", got)
		}
	})
	t.Run("rollback_then_exact_replay_and_payload_conflict", func(t *testing.T) {
		w, ctx, grant := reviewWriterFixture(t)
		batch := reviewWriterBatch()
		injected := errors.New("injected precommit disconnect")
		_, err := w.commitWriterBatch(ctx, grant, batch, writerFaultHooks{BeforeCommit: func(*sql.Tx) error { return injected }})
		if !errors.Is(err, injected) {
			t.Fatalf("fault not propagated: %v", err)
		}
		assertReviewWriterRolledBack(t, ctx, w.target)
		first, err := w.commitWriterBatch(ctx, grant, batch, writerFaultHooks{})
		if err != nil {
			t.Fatal(err)
		}
		replayed, err := w.commitWriterBatch(ctx, grant, batch, writerFaultHooks{})
		if err != nil || !replayed.Replayed || replayed.BatchID != first.BatchID || replayed.CatalogRevision != first.CatalogRevision || !replayed.CommittedAt.Equal(first.CommittedAt) {
			t.Fatalf("receipt replay: %+v %v", replayed, err)
		}
		batch.Rows[0][4] = "different payload"
		if _, err := w.commitWriterBatch(ctx, grant, batch, writerFaultHooks{}); !errors.Is(err, ErrWriterReceipt) {
			t.Fatalf("changed batch accepted: %v", err)
		}
		if got := writerTestScalar(t, ctx, w.target, "SELECT quota FROM log_monthly_stats WHERE period_key='202609'"); got != 50 {
			t.Fatalf("replay counted quota again: %d", got)
		}
		if got := writerTestScalar(t, ctx, w.target, "SELECT mutation_revision FROM archive_days"); got != 1 {
			t.Fatalf("replay advanced revision: %d", got)
		}
	})
	t.Run("lost_commit_response_recovers_from_receipt", func(t *testing.T) {
		w, ctx, grant := reviewWriterFixture(t)
		result, err := w.commitWriterBatch(ctx, grant, reviewWriterBatch(), writerFaultHooks{AfterCommit: func() error { return errors.New("response lost") }})
		if err != nil || !result.Replayed || result.AfterID != 1 {
			t.Fatalf("commit recovery: %+v %v", result, err)
		}
		if got := writerTestScalar(t, ctx, w.target, "SELECT COUNT(*) FROM archive_batch_receipts"); got != 1 {
			t.Fatalf("receipts=%d", got)
		}
		if got := writerTestScalar(t, ctx, w.target, "SELECT log_rows FROM log_daily_stats"); got != 1 {
			t.Fatalf("rows=%d", got)
		}
	})
	t.Run("raw_only_change_invalidates_day_and_exact_bytes_do_not", func(t *testing.T) {
		w, ctx, grant := reviewWriterFixture(t)
		batch := reviewWriterBatch()
		if _, err := w.commitWriterBatch(ctx, grant, batch, writerFaultHooks{}); err != nil {
			t.Fatal(err)
		}
		batch = writerTestNextBatch(batch, "5")
		batch.Rows[0][4] = "changed other only"
		if _, err := w.commitWriterBatch(ctx, grant, batch, writerFaultHooks{}); err != nil {
			t.Fatal(err)
		}
		batch = writerTestNextBatch(batch, "6")
		result, err := w.commitWriterBatch(ctx, grant, batch, writerFaultHooks{})
		if err != nil {
			t.Fatal(err)
		}
		if result.CatalogRevision != 2 || writerTestScalar(t, ctx, w.target, "SELECT mutation_revision FROM archive_days") != 2 {
			t.Fatal("raw-only revision or unchanged-row handling failed")
		}
		if got := writerTestScalar(t, ctx, w.target, "SELECT quota FROM log_daily_stats"); got != 50 {
			t.Fatalf("raw change affected quota: %d", got)
		}
	})
	t.Run("cross_month_move_invalidates_both_days_atomically", func(t *testing.T) {
		w, ctx, grant := reviewWriterFixture(t)
		batch := reviewWriterBatch()
		oldDate := writerTestDate(t, batch)
		if _, err := w.commitWriterBatch(ctx, grant, batch, writerFaultHooks{}); err != nil {
			t.Fatal(err)
		}
		batch = writerTestNextBatch(batch, "5")
		batch.Rows[0][1] = strconv.FormatInt(time.Date(2026, 10, 1, 0, 0, 0, 0, archiveLocation).Unix(), 10)
		result, err := w.commitWriterBatch(ctx, grant, batch, writerFaultHooks{})
		if err != nil {
			t.Fatal(err)
		}
		if got := writerTestScalar(t, ctx, w.target, "SELECT COUNT(*) FROM logs_202609"); got != 0 {
			t.Fatalf("old month retained row: %d", got)
		}
		if got := writerTestScalar(t, ctx, w.target, "SELECT COUNT(*) FROM logs_202610"); got != 1 {
			t.Fatalf("new month missing row: %d", got)
		}
		if writerTestScalar(t, ctx, w.target, "SELECT mutation_revision FROM archive_days WHERE log_date=?", oldDate) != 2 || writerTestScalar(t, ctx, w.target, "SELECT mutation_revision FROM archive_days WHERE log_date='2026-10-01'") != 1 {
			t.Fatal("both dates were not invalidated")
		}
		if writerTestScalar(t, ctx, w.target, "SELECT COUNT(*) FROM archive_days WHERE state='dirty' AND catalog_revision=?", result.CatalogRevision) != 2 {
			t.Fatal("dates published in different catalog revisions")
		}
		if writerTestScalar(t, ctx, w.target, "SELECT quota FROM log_monthly_stats WHERE period_key='202609'") != 0 || writerTestScalar(t, ctx, w.target, "SELECT quota FROM log_monthly_stats WHERE period_key='202610'") != 50 {
			t.Fatal("cross month contribution not conserved")
		}
	})
	t.Run("nontransactional_previous_month_rejects_relocation", func(t *testing.T) {
		w, ctx, grant := reviewWriterFixture(t)
		batch := reviewWriterBatch()
		if _, err := w.commitWriterBatch(ctx, grant, batch, writerFaultHooks{}); err != nil {
			t.Fatal(err)
		}
		if _, err := w.target.ExecContext(ctx, "ALTER TABLE logs_202609 ENGINE=MyISAM"); err != nil {
			t.Fatal(err)
		}
		batch = writerTestNextBatch(batch, "5")
		batch.Rows[0][1] = strconv.FormatInt(time.Date(2026, 10, 1, 0, 0, 0, 0, archiveLocation).Unix(), 10)
		if _, err := w.commitWriterBatch(ctx, grant, batch, writerFaultHooks{}); err == nil {
			t.Fatalf("nontransactional old month accepted: %v", err)
		}
		if writerTestScalar(t, ctx, w.target, "SELECT COUNT(*) FROM logs_202609") != 1 || writerTestScalar(t, ctx, w.target, "SELECT COUNT(*) FROM logs_202610") != 0 || writerTestScalar(t, ctx, w.target, "SELECT COUNT(*) FROM archive_batch_receipts") != 1 {
			t.Fatal("nontransactional relocation escaped rollback")
		}
	})
	for _, side := range []string{"old", "new"} {
		t.Run("freeze_"+side+"_date_rejects_cross_month_move", func(t *testing.T) {
			w, ctx, grant := reviewWriterFixture(t)
			batch := reviewWriterBatch()
			oldDate := writerTestDate(t, batch)
			if _, err := w.commitWriterBatch(ctx, grant, batch, writerFaultHooks{}); err != nil {
				t.Fatal(err)
			}
			date := oldDate
			if side == "new" {
				date = "2026-10-01"
			}
			if _, err := w.target.ExecContext(ctx, `INSERT INTO archive_days(log_date,state,freeze_until,updated_at) VALUES(?,'verifying',UTC_TIMESTAMP(6)-INTERVAL 1 SECOND,UTC_TIMESTAMP(6)) ON DUPLICATE KEY UPDATE freeze_until=VALUES(freeze_until)`, date); err != nil {
				t.Fatal(err)
			}
			batch = writerTestNextBatch(batch, "5")
			batch.Rows[0][1] = strconv.FormatInt(time.Date(2026, 10, 1, 0, 0, 0, 0, archiveLocation).Unix(), 10)
			if _, err := w.commitWriterBatch(ctx, grant, batch, writerFaultHooks{}); !errors.Is(err, ErrWriterFrozen) {
				t.Fatalf("expired freeze marker ignored: %v", err)
			}
			if writerTestScalar(t, ctx, w.target, "SELECT COUNT(*) FROM logs_202609") != 1 || writerTestScalar(t, ctx, w.target, "SELECT COUNT(*) FROM logs_202610") != 0 || writerTestScalar(t, ctx, w.target, "SELECT COUNT(*) FROM archive_batch_receipts") != 1 {
				t.Fatal("frozen relocation mutated rows or receipt")
			}
		})
	}
	t.Run("unscoped_blockers_follow_rows_without_replay_inflation", func(t *testing.T) {
		w, ctx, grant := reviewWriterFixture(t)
		batch := reviewWriterBatch()
		batch.Rows[0][1] = nil
		if _, err := w.commitWriterBatch(ctx, grant, batch, writerFaultHooks{}); err != nil {
			t.Fatal(err)
		}
		if _, err := w.commitWriterBatch(ctx, grant, batch, writerFaultHooks{}); err != nil {
			t.Fatal(err)
		}
		if writerTestScalar(t, ctx, w.target, "SELECT unscoped_blocking_issues FROM archive_dataset_meta") != 1 {
			t.Fatal("undated replay changed blockers")
		}
		batch = writerTestNextBatch(batch, "5")
		batch.Rows[0][1] = "1788278400"
		if _, err := w.commitWriterBatch(ctx, grant, batch, writerFaultHooks{}); err != nil {
			t.Fatal(err)
		}
		if writerTestScalar(t, ctx, w.target, "SELECT unscoped_blocking_issues FROM archive_dataset_meta") != 0 {
			t.Fatal("dated correction retained blocker")
		}
		batch = writerTestNextBatch(batch, "6")
		batch.Rows[0][2] = "999"
		if _, err := w.commitWriterBatch(ctx, grant, batch, writerFaultHooks{}); err != nil {
			t.Fatal(err)
		}
		if writerTestScalar(t, ctx, w.target, "SELECT unscoped_blocking_issues FROM archive_dataset_meta") != 1 {
			t.Fatal("unknown charged type did not block")
		}
	})
	t.Run("takeover_fences_old_writer_and_release", func(t *testing.T) {
		w, ctx, old := reviewWriterFixture(t)
		next := old
		next.WriterEpoch++
		next.Session = strings.Repeat("7", 32)
		if err := w.AcquireWriter(ctx, next, 90*time.Second); err != nil {
			t.Fatal(err)
		}
		if _, err := w.commitWriterBatch(ctx, old, reviewWriterBatch(), writerFaultHooks{}); !errors.Is(err, ErrWriterLease) {
			t.Fatalf("old writer wrote: %v", err)
		}
		if err := w.ReleaseWriter(ctx, old); !errors.Is(err, ErrWriterLease) {
			t.Fatalf("old writer released successor: %v", err)
		}
		if _, err := w.commitWriterBatch(ctx, next, reviewWriterBatch(), writerFaultHooks{}); err != nil {
			t.Fatal(err)
		}
		if err := w.ReleaseWriter(ctx, next); err != nil {
			t.Fatal(err)
		}
		if _, err := w.PassV2(ctx, next); !errors.Is(err, ErrWriterLease) {
			t.Fatalf("released writer resumed without fresh claim: %v", err)
		}
		if err := w.AcquireWriter(ctx, next, 90*time.Second); err != nil {
			t.Fatalf("fresh same-epoch authorization could not renew expired target lease: %v", err)
		}
	})
	t.Run("populated_legacy_target_and_lost_target_checkpoint_are_not_adopted", func(t *testing.T) {
		w, ctx, grant := reviewWriterFixture(t)
		if _, err := w.target.ExecContext(ctx, "INSERT INTO logs(id,created_at,type,quota) VALUES(9000,1788278400,2,50)"); err != nil {
			t.Fatal(err)
		}
		if err := w.AcquireWriter(ctx, grant, 90*time.Second); !errors.Is(err, ErrWriterLegacyData) {
			t.Fatalf("legacy target adopted: %v", err)
		}
		if _, err := w.target.ExecContext(ctx, "DELETE FROM logs"); err != nil {
			t.Fatal(err)
		}
		if _, err := w.commitWriterBatch(ctx, grant, reviewWriterBatch(), writerFaultHooks{}); err != nil {
			t.Fatal(err)
		}
		if _, err := w.target.ExecContext(ctx, "DELETE FROM archive_checkpoints"); err != nil {
			t.Fatal(err)
		}
		if err := w.AcquireWriter(ctx, grant, 90*time.Second); !errors.Is(err, ErrWriterLegacyData) {
			t.Fatalf("lost target cursor silently reset: %v", err)
		}
	})
}

func TestWriterMigrationMySQL(t *testing.T) {
	t.Run("resume_alter_after_ddl_before_receipt", func(t *testing.T) {
		w, ctx := foundationTestWorker(t)
		if _, err := w.PrepareFoundation(ctx, foundationTestIdentity()); err != nil {
			t.Fatal(err)
		}
		if _, err := w.target.ExecContext(ctx, "DELETE FROM archive_schema_migrations WHERE version>=7"); err != nil {
			t.Fatal(err)
		}
		if _, err := w.PrepareFoundation(ctx, foundationTestIdentity()); err != nil {
			t.Fatalf("repeated ALTER failed: %v", err)
		}
		if _, err := w.InspectFoundation(ctx, foundationTestIdentity()); err != nil {
			t.Fatal(err)
		}
	})
	t.Run("conflicting_existing_alter_column_is_not_adopted", func(t *testing.T) {
		w, ctx := foundationTestWorker(t)
		if _, err := w.PrepareFoundation(ctx, foundationTestIdentity()); err != nil {
			t.Fatal(err)
		}
		if _, err := w.target.ExecContext(ctx, "ALTER TABLE archive_log_state MODIFY raw_row_hash BINARY(16) NULL"); err != nil {
			t.Fatal(err)
		}
		if _, err := w.target.ExecContext(ctx, "DELETE FROM archive_schema_migrations WHERE version>=7"); err != nil {
			t.Fatal(err)
		}
		if _, err := w.PrepareFoundation(ctx, foundationTestIdentity()); err == nil {
			t.Fatal("wrong hash column silently adopted")
		}
	})
}

func TestWriterPayloadHashBindsIdentityAndCursor(t *testing.T) {
	identity := foundationTestIdentity()
	batch := reviewWriterBatch()
	want, _, err := writerPayloadHash(identity, batch)
	if err != nil {
		t.Fatal(err)
	}
	other := identity
	other.SourceGenerationID = strings.Repeat("7", 32)
	changed, _, err := writerPayloadHash(other, batch)
	if err != nil || changed == want {
		t.Fatal("payload hash omits source generation")
	}
	batch.BeforeID = 1
	changed, _, err = writerPayloadHash(identity, batch)
	if err != nil || changed == want {
		t.Fatal("payload hash omits cursor")
	}
	batch.BeforeID = 0
	batch.AfterID = 999
	if _, _, err := writerPayloadHash(identity, batch); !errors.Is(err, ErrWriterCheckpoint) {
		t.Fatalf("cursor skipped beyond payload rows: %v", err)
	}
	bad := archivecontract.WriterGrant{Identity: identity, ProtocolVersion: 2, WriterEpoch: 1, Session: strings.Repeat("3", 32), LeaseSeconds: 120}
	w := new(Worker)
	if err := w.AcquireWriter(context.Background(), bad, 0); !errors.Is(err, ErrWriterLease) {
		t.Fatalf("empty lease budget touched database: %v", err)
	}
}
