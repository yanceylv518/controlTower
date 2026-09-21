package logarchive

import (
	"context"
	"database/sql"
	"strings"
	"testing"
	"time"

	af "controltower/internal/archivecontract"
)

// These cases are deliberately outside the UTF-8-only billing fixtures: raw
// archive columns can contain arbitrary bytes and hashes must preserve them.
func TestWriterRawHashPreservesBytesAndFraming(t *testing.T) {
	columns := []string{"id", "a", "b"}
	cases := []struct {
		name    string
		columns []string
		row     []any
	}{
		{"null", columns, []any{"1", nil, "x"}},
		{"empty", columns, []any{"1", "", "x"}},
		{"literal_null", columns, []any{"1", "null", "x"}},
		{"binary_ff", columns, []any{"1", string([]byte{0xff}), "x"}},
		{"binary_fe", columns, []any{"1", string([]byte{0xfe}), "x"}},
		{"replacement_rune", columns, []any{"1", "\ufffd", "x"}},
		{"nul_in_first_field", columns, []any{"1", "a\x00", "b"}},
		{"nul_in_second_field", columns, []any{"1", "a", "\x00b"}},
		{"split_ab_c", columns, []any{"1", "ab", "c"}},
		{"split_a_bc", columns, []any{"1", "a", "bc"}},
		{"renamed_column", []string{"id", "aa", "b"}, []any{"1", nil, "x"}},
		{"reordered_column", []string{"id", "b", "a"}, []any{"1", nil, "x"}},
	}
	seen := make(map[[32]byte]string)
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h, err := writerRawHash(tc.columns, tc.row)
			if err != nil {
				t.Fatal(err)
			}
			if other, ok := seen[h]; ok {
				t.Fatalf("hash collides with %s", other)
			}
			seen[h] = tc.name
			again, err := writerRawHash(append([]string(nil), tc.columns...), append([]any(nil), tc.row...))
			if err != nil || again != h {
				t.Fatal("same bytes have an unstable hash")
			}
		})
	}
}

func reviewWriterFixture(t *testing.T) (*Worker, context.Context, af.WriterGrant) {
	t.Helper()
	w, ctx := foundationTestWorker(t)
	for _, db := range []*sql.DB{w.source, w.target} {
		if _, err := db.ExecContext(ctx, "ALTER TABLE logs ADD prompt_tokens BIGINT NULL, ADD completion_tokens BIGINT NULL"); err != nil {
			t.Fatal(err)
		}
	}
	identity := foundationTestIdentity()
	if _, err := w.PrepareFoundation(ctx, identity); err != nil {
		t.Fatal(err)
	}
	grant := af.WriterGrant{Identity: identity, ProtocolVersion: af.ProtocolVersion, WriterEpoch: 1, Session: strings.Repeat("3", 32), ConfigVersion: 1, LeaseSeconds: 120}
	if err := w.AcquireWriter(ctx, grant, 120*time.Second); err != nil {
		t.Fatal(err)
	}
	return w, ctx, grant
}

func reviewWriterBatch() writerBatch {
	return writerBatch{
		ID: strings.Repeat("4", 32), BeforeID: 0, AfterID: 1,
		Columns: []string{"id", "created_at", "type", "quota", "other", "prompt_tokens", "completion_tokens"},
		Rows:    [][]any{{"1", "1788278400", "2", "50", "original", "5", "10"}},
	}
}

func assertReviewWriterRolledBack(t *testing.T, ctx context.Context, db *sql.DB) {
	t.Helper()
	for _, table := range []string{"logs_202609", "archive_log_state", "log_daily_stats", "log_monthly_stats", "archive_batch_receipts", "archive_days"} {
		var n int
		if err := db.QueryRowContext(ctx, "SELECT COUNT(*) FROM "+quote(table)).Scan(&n); err != nil {
			t.Fatalf("%s: %v", table, err)
		}
		if n != 0 {
			t.Fatalf("failed batch left %d rows in %s", n, table)
		}
	}
	var advanced int
	if err := db.QueryRowContext(ctx, "SELECT COUNT(*) FROM archive_checkpoints WHERE after_id<>0 OR last_batch_id IS NOT NULL").Scan(&advanced); err != nil || advanced != 0 {
		t.Fatalf("failed batch advanced authoritative cursor: %d %v", advanced, err)
	}
	var revision, unscoped uint64
	if err := db.QueryRowContext(ctx, "SELECT catalog_revision,unscoped_blocking_issues FROM archive_dataset_meta WHERE singleton_id=1").Scan(&revision, &unscoped); err != nil || revision != 0 || unscoped != 0 {
		t.Fatalf("failed batch changed catalog: %d %d %v", revision, unscoped, err)
	}
}

func TestWriterReviewMySQL(t *testing.T) {
	t.Run("target_trigger_mutation_rolls_back_entire_batch", func(t *testing.T) {
		w, ctx, grant := reviewWriterFixture(t)
		if err := ensureMonthlyTables(ctx, w.target, []string{"202609"}); err != nil {
			t.Fatal(err)
		}
		if _, err := w.target.ExecContext(ctx, "CREATE TRIGGER archive_review_change BEFORE INSERT ON logs_202609 FOR EACH ROW SET NEW.other='changed by target'"); err != nil {
			t.Fatal(err)
		}
		_, err := w.commitWriterBatch(ctx, grant, reviewWriterBatch(), writerFaultHooks{})
		if err == nil || !strings.Contains(err.Error(), "verification") {
			t.Fatalf("target changed raw bytes without failing verification: %v", err)
		}
		assertReviewWriterRolledBack(t, ctx, w.target)
	})
	t.Run("lease_lost_after_writes_rolls_back_entire_batch", func(t *testing.T) {
		w, ctx, grant := reviewWriterFixture(t)
		hookCalled := false
		_, err := w.commitWriterBatch(ctx, grant, reviewWriterBatch(), writerFaultHooks{BeforeCommit: func(tx *sql.Tx) error {
			hookCalled = true
			_, err := tx.ExecContext(ctx, "UPDATE archive_dataset_meta SET writer_lease_until=UTC_TIMESTAMP(6)-INTERVAL 1 SECOND WHERE singleton_id=1")
			return err
		}})
		if !hookCalled {
			t.Fatalf("batch never reached final commit boundary: %v", err)
		}
		if err == nil {
			t.Fatal("expired lease committed a batch")
		}
		assertReviewWriterRolledBack(t, ctx, w.target)
	})
	t.Run("same_epoch_different_session_cannot_take_writer", func(t *testing.T) {
		w, ctx, grant := reviewWriterFixture(t)
		imposter := grant
		imposter.Session = strings.Repeat("5", 32)
		if err := w.AcquireWriter(ctx, imposter, 120*time.Second); err == nil {
			t.Fatal("same epoch reassigned to a different session")
		}
		var epoch uint64
		var session string
		if err := w.target.QueryRowContext(ctx, "SELECT writer_epoch,LOWER(HEX(writer_session)) FROM archive_dataset_meta WHERE singleton_id=1").Scan(&epoch, &session); err != nil || epoch != grant.WriterEpoch || session != grant.Session {
			t.Fatalf("rejected claim mutated writer identity: %d %q %v", epoch, session, err)
		}
		if _, err := w.commitWriterBatch(ctx, grant, reviewWriterBatch(), writerFaultHooks{}); err != nil {
			t.Fatalf("valid writer was disrupted by rejected claim: %v", err)
		}
	})
	t.Run("expired_epoch_still_cannot_change_session", func(t *testing.T) {
		w, ctx, grant := reviewWriterFixture(t)
		if _, err := w.target.ExecContext(ctx, "UPDATE archive_dataset_meta SET writer_lease_until=UTC_TIMESTAMP(6)-INTERVAL 1 SECOND WHERE singleton_id=1"); err != nil {
			t.Fatal(err)
		}
		imposter := grant
		imposter.Session = strings.Repeat("5", 32)
		if err := w.AcquireWriter(ctx, imposter, 120*time.Second); err == nil {
			t.Fatal("expired epoch was reassigned without a new CT epoch")
		}
		var epoch uint64
		var session string
		if err := w.target.QueryRowContext(ctx, "SELECT writer_epoch,LOWER(HEX(writer_session)) FROM archive_dataset_meta WHERE singleton_id=1").Scan(&epoch, &session); err != nil || epoch != grant.WriterEpoch || session != grant.Session {
			t.Fatalf("rejected expired claim mutated identity: %d %q %v", epoch, session, err)
		}
	})
	t.Run("lease_acquisition_lock_wait_consumes_authorization", func(t *testing.T) {
		w, ctx, grant := reviewWriterFixture(t)
		w.target.SetMaxOpenConns(2)
		if _, err := w.target.ExecContext(ctx, "UPDATE archive_dataset_meta SET writer_lease_until=UTC_TIMESTAMP(6)-INTERVAL 1 SECOND WHERE singleton_id=1"); err != nil {
			t.Fatal(err)
		}
		blocker, err := w.target.BeginTx(ctx, nil)
		if err != nil {
			t.Fatal(err)
		}
		defer blocker.Rollback()
		var epoch uint64
		if err := blocker.QueryRowContext(ctx, "SELECT writer_epoch FROM archive_dataset_meta WHERE singleton_id=1 FOR UPDATE").Scan(&epoch); err != nil {
			t.Fatal(err)
		}
		grant.WriterEpoch++
		const budget = 3 * time.Second
		started := time.Now()
		result := make(chan error, 1)
		go func() { result <- w.AcquireWriter(ctx, grant, budget) }()
		// The lock is the fault injection: target authorization must not start
		// afresh when a contended transaction finally obtains the meta row.
		time.Sleep(300 * time.Millisecond)
		if err := blocker.Rollback(); err != nil {
			t.Fatal(err)
		}
		if err := <-result; err != nil {
			t.Fatal(err)
		}
		var remainingMicros int64
		if err := w.target.QueryRowContext(ctx, "SELECT TIMESTAMPDIFF(MICROSECOND,UTC_TIMESTAMP(6),writer_lease_until) FROM archive_dataset_meta WHERE singleton_id=1").Scan(&remainingMicros); err != nil {
			t.Fatal(err)
		}
		if remainingMicros <= 0 {
			t.Fatal("lease unexpectedly expired during fixture")
		}
		if time.Duration(remainingMicros)*time.Microsecond > budget-time.Since(started)+100*time.Millisecond {
			t.Fatal("target lock wait extended CT authorization")
		}
	})
}
