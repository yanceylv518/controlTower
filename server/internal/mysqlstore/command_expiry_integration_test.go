package mysqlstore

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"controltower/server/internal/tuning"
	"github.com/go-sql-driver/mysql"
)

func commandExpiryTestDB(t *testing.T) (*sql.DB, string, time.Time) {
	t.Helper()
	dsn := os.Getenv("CT_MYSQL_TEST_DSN")
	if dsn == "" {
		t.Skip("set CT_MYSQL_TEST_DSN to run command expiry integration test")
	}
	db, err := Open(dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	db.SetMaxOpenConns(16)
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	if err := ApplyDir(ctx, db, "../../migrations"); err != nil {
		t.Fatal(err)
	}
	prefix := fmt.Sprintf("expiry-%d-", time.Now().UnixNano())
	t.Cleanup(func() {
		for _, table := range []string{"operation_audits", "channel_commands", "tuning_recommendations"} {
			_, _ = db.Exec("DELETE FROM "+table+" WHERE instance_id LIKE ?", prefix+"%")
		}
		_, _ = db.Exec("DELETE FROM instances WHERE id LIKE ?", prefix+"%")
	})
	return db, prefix, time.Now().UTC().Truncate(time.Millisecond)
}

func TestCommandExpiryConcurrentBatches(t *testing.T) {
	db, prefix, before := commandExpiryTestDB(t)
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	const expired = 250
	for i := 0; i < expired+1000+2; i++ {
		id := fmt.Sprintf("%s%04d", prefix, i)
		// Use different primary-key and creation-time orders, as real command
		// IDs are random and cannot determine the index lock acquisition order.
		status, at := "pending", before.Add(-time.Hour-time.Duration(i)*time.Millisecond)
		if i >= expired {
			status = "succeeded"
		}
		if i == expired+1000 {
			status, at = "pending", before
		}
		if i == expired+1001 {
			status = "delivered"
		}
		if _, err = tx.ExecContext(ctx, `INSERT INTO channel_commands(id,instance_id,channel_id,command_type,payload_json,status,created_by,error_summary,created_at,updated_at)
VALUES(?,?,1,'channel.update','{}',?,'test','',?,?)`, id, prefix, status, at, at); err != nil {
			t.Fatal(err)
		}
		if i < expired {
			if _, err = tx.ExecContext(ctx, `INSERT INTO operation_audits(id,instance_id,operation_type,target_type,target_id,actor_id,before_summary,after_summary,status,error_summary,created_at,updated_at)
VALUES(?,?,'channel.update','channel','1','test','','','submitted','',?,?)`, id, prefix, at, at); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}

	// A locked historical completed command must not block pending expiration.
	blocker, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer blocker.Rollback()
	var held string
	if err = blocker.QueryRowContext(ctx, "SELECT id FROM channel_commands WHERE id=? FOR UPDATE", fmt.Sprintf("%s%04d", prefix, expired)).Scan(&held); err != nil {
		t.Fatal(err)
	}
	type result struct {
		n   int
		err error
	}
	results := make(chan result, 6)
	var workers sync.WaitGroup
	start := make(chan struct{})
	for i := 0; i < 6; i++ {
		workers.Add(1)
		go func() {
			defer workers.Done()
			<-start
			n, e := New(db).ExpireStaleCommands(before)
			results <- result{n, e}
		}()
	}
	close(start)
	done := make(chan struct{})
	go func() { workers.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		_ = blocker.Rollback()
		<-done
		t.Fatal("expiration blocked on an unrelated completed command")
	}
	total := 0
	for i := 0; i < 6; i++ {
		r := <-results
		if r.err != nil {
			t.Fatal(r.err)
		}
		total += r.n
	}
	if total != expired {
		t.Fatalf("expired=%d, want %d", total, expired)
	}
	var commands, audits, untouched int
	if err = db.QueryRow("SELECT COUNT(*) FROM channel_commands WHERE id LIKE ? AND status='expired'", prefix+"%").Scan(&commands); err != nil {
		t.Fatal(err)
	}
	if err = db.QueryRow("SELECT COUNT(*) FROM operation_audits WHERE id LIKE ? AND status='expired'", prefix+"%").Scan(&audits); err != nil {
		t.Fatal(err)
	}
	if err = db.QueryRow("SELECT COUNT(*) FROM channel_commands WHERE id LIKE ? AND status IN ('pending','delivered')", prefix+"%").Scan(&untouched); err != nil {
		t.Fatal(err)
	}
	if commands != expired || audits != expired || untouched != 2 {
		t.Fatalf("commands=%d audits=%d untouched=%d", commands, audits, untouched)
	}
}

func insertExpiryTestCommand(t *testing.T, tx *sql.Tx, id, instance, status string, at time.Time, audit bool) {
	t.Helper()
	if _, err := tx.Exec(`INSERT INTO channel_commands(id,instance_id,channel_id,command_type,payload_json,status,created_by,created_at,updated_at)
VALUES(?,?,1,'channel.update','{}',?,'test',?,?)`, id, instance, status, at, at); err != nil {
		t.Fatal(err)
	}
	if audit {
		if _, err := tx.Exec(`INSERT INTO operation_audits(id,instance_id,operation_type,target_type,target_id,actor_id,before_summary,after_summary,status,error_summary,created_at,updated_at)
VALUES(?,?,'channel.update','channel','1','test','','','submitted','',?,?)`, id, instance, at, at); err != nil {
			t.Fatal(err)
		}
	}
}

func TestCommandExpiryWithClaimsAndDirectWrites(t *testing.T) {
	db, prefix, before := commandExpiryTestDB(t)
	tx, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	for i := 0; i < 3; i++ {
		instance := fmt.Sprintf("%sinst%d", prefix, i)
		if _, err := tx.Exec(`INSERT INTO instances(id,site_id,name,env,region,base_url,enabled,created_at,updated_at)
VALUES(?,?,?,'test','local','',1,?,?)`, instance, instance, instance, before, before); err != nil {
			t.Fatal(err)
		}
		for j := 0; j < 150; j++ {
			at := before.Add(-time.Hour - time.Duration(j)*time.Millisecond)
			if j >= 100 {
				at = before.Add(time.Duration(j) * time.Millisecond)
			}
			insertExpiryTestCommand(t, tx, fmt.Sprintf("%s%d-%03d", prefix, i, j), instance, "pending", at, true)
		}
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	store := New(db)
	start := make(chan struct{})
	type result struct {
		expired int
		claimed []string
		err     error
	}
	results := make(chan result, 7)
	// Follow the heartbeat's expire-then-claim sequence, with two concurrent
	// callers per instance. No command may be delivered twice or after expiry.
	for worker := 0; worker < 6; worker++ {
		instance := fmt.Sprintf("%sinst%d", prefix, worker%3)
		go func() {
			<-start
			n, err := store.ExpireStaleCommands(before)
			if err != nil {
				results <- result{err: fmt.Errorf("expire: %w", err)}
				return
			}
			commands, err := store.ClaimPendingCommands(instance, before)
			if err != nil {
				err = fmt.Errorf("claim: %w", err)
			}
			ids := make([]string, len(commands))
			for i, cmd := range commands {
				ids[i] = cmd.ID
			}
			results <- result{n, ids, err}
		}()
	}
	go func() {
		<-start
		for i := 0; i < 50; i++ {
			rec := tuning.Recommendation{ID: fmt.Sprintf("%srec%d", prefix, i), InstanceID: prefix + "inst0", ChannelID: 1, Rule: "continuous", ModeAtCreation: "auto", ProposedWeight: 10, CreatedAt: before}
			if _, err := store.RecordDirectWeightChange(rec, "system:auto", before); err != nil {
				results <- result{err: fmt.Errorf("direct write: %w", err)}
				return
			}
		}
		results <- result{}
	}()
	close(start)
	total, claimed := 0, map[string]bool{}
	for i := 0; i < 7; i++ {
		r := <-results
		if r.err != nil {
			t.Errorf("concurrent command work failed: %v", r.err)
		}
		total += r.expired
		for _, id := range r.claimed {
			if claimed[id] {
				t.Errorf("duplicate delivery: %s", id)
			}
			claimed[id] = true
		}
	}
	if total != 300 || len(claimed) != 150 {
		t.Fatalf("expired=%d claimed=%d, want 300 and 150", total, len(claimed))
	}
	var expired, delivered, succeeded, audits int
	if err := db.QueryRow(`SELECT COALESCE(SUM(status='expired'),0),COALESCE(SUM(status='delivered'),0),COALESCE(SUM(status='succeeded'),0)
FROM channel_commands WHERE instance_id LIKE ?`, prefix+"%").Scan(&expired, &delivered, &succeeded); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(`SELECT COUNT(*) FROM operation_audits WHERE instance_id LIKE ? AND status='expired'`, prefix+"%").Scan(&audits); err != nil {
		t.Fatal(err)
	}
	if expired != 300 || delivered != 150 || succeeded != 50 || audits != 300 {
		t.Fatalf("expired=%d delivered=%d direct writes=%d audits=%d", expired, delivered, succeeded, audits)
	}
}

func TestCommandExpiryRechecksCandidatesAfterConcurrentDelivery(t *testing.T) {
	db, prefix, before := commandExpiryTestDB(t)
	tx, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	for i := 0; i < 101; i++ {
		insertExpiryTestCommand(t, tx, fmt.Sprintf("%s%03d", prefix, i), prefix, "pending", before.Add(-time.Hour), true)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	blocker, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer blocker.Rollback()
	var blockerID int64
	if err := blocker.QueryRow("SELECT CONNECTION_ID()").Scan(&blockerID); err != nil {
		t.Fatal(err)
	}
	// Discovery still sees these 100 commands as pending, but the subsequent
	// primary-key lock must wait and recheck the committed delivery transition.
	if _, err := blocker.Exec(`UPDATE channel_commands SET status='delivered' WHERE instance_id=? AND id<>?`, prefix, prefix+"100"); err != nil {
		t.Fatal(err)
	}
	type result struct {
		n   int
		err error
	}
	done := make(chan result, 1)
	go func() {
		n, err := New(db).ExpireStaleCommands(before)
		done <- result{n, err}
	}()
	// Wait for actual lock contention, not an arbitrary delay that could let
	// the cleaner miss the uncommitted candidates entirely on a slow machine.
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	for {
		select {
		case r := <-done:
			t.Fatalf("cleaner finished before candidate transition committed: n=%d err=%v", r.n, r.err)
		default:
		}
		var waiting int
		err = db.QueryRowContext(ctx, `SELECT COUNT(*) FROM performance_schema.data_lock_waits w
JOIN performance_schema.threads t ON t.THREAD_ID=w.BLOCKING_THREAD_ID WHERE t.PROCESSLIST_ID=?`, blockerID).Scan(&waiting)
		if err != nil {
			_ = blocker.Rollback()
			<-done
			t.Fatalf("waiting for candidate recheck: %v", err)
		}
		if waiting > 0 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if err := blocker.Commit(); err != nil {
		t.Fatal(err)
	}
	r := <-done
	if r.err != nil || r.n != 1 {
		t.Fatalf("must continue beyond consumed batch: n=%d err=%v", r.n, r.err)
	}
	var wrongAudit int
	if err := db.QueryRow(`SELECT COUNT(*) FROM channel_commands c JOIN operation_audits a ON a.id=c.id
WHERE c.instance_id=? AND ((c.status='delivered' AND a.status<>'submitted') OR (c.status='expired' AND a.status<>'expired'))`, prefix).Scan(&wrongAudit); err != nil {
		t.Fatal(err)
	}
	if wrongAudit != 0 {
		t.Fatalf("incorrect audits after candidate recheck: %d", wrongAudit)
	}
}

func TestCommandExpiryRollsBackFailedBatch(t *testing.T) {
	db, prefix, before := commandExpiryTestDB(t)
	tx, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	for i := 0; i < 150; i++ {
		insertExpiryTestCommand(t, tx, fmt.Sprintf("%s%03d", prefix, i), prefix, "pending", before.Add(-time.Hour), true)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	trigger := strings.ReplaceAll(prefix, "-", "_") + "audit_error"
	if _, err := db.Exec(fmt.Sprintf(`CREATE TRIGGER %s BEFORE UPDATE ON operation_audits FOR EACH ROW
BEGIN IF NEW.id='%s100' THEN SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT='injected audit failure'; END IF; END`, trigger, prefix)); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = db.Exec("DROP TRIGGER IF EXISTS " + trigger) })
	n, err := New(db).ExpireStaleCommands(before)
	var dbErr *mysql.MySQLError
	if n != 100 || !errors.As(err, &dbErr) || dbErr.Number != 1644 {
		t.Fatalf("committed=%d error=%v, want 100 and audit failure", n, err)
	}
	var commands, audits int
	if err := db.QueryRow(`SELECT COUNT(*) FROM channel_commands WHERE instance_id=? AND status='expired'`, prefix).Scan(&commands); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(`SELECT COUNT(*) FROM operation_audits WHERE instance_id=? AND status='expired'`, prefix).Scan(&audits); err != nil {
		t.Fatal(err)
	}
	if commands != 100 || audits != 100 {
		t.Fatalf("failed batch partially committed: commands=%d audits=%d", commands, audits)
	}
	if _, err := db.Exec("DROP TRIGGER " + trigger); err != nil {
		t.Fatal(err)
	}
	if n, err := New(db).ExpireStaleCommands(before); err != nil || n != 50 {
		t.Fatalf("resume after audit recovery: n=%d err=%v", n, err)
	}
}

func TestCommandExpiryRetryPolicy(t *testing.T) {
	db, prefix, before := commandExpiryTestDB(t)
	// Session variables survive transaction rollback. A single connection lets
	// the trigger count attempts without adding nontransactional test tables.
	db.SetMaxOpenConns(1)
	for i, tc := range []struct {
		name                 string
		code, fail, attempts int
		wantExpired          int
	}{
		{"deadlock then success", 1213, 2, 3, 1},
		{"persistent deadlock", 1213, 10, 3, 0},
		{"audit error", 1644, 10, 1, 0},
		{"lock wait timeout", 1205, 10, 1, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			id := fmt.Sprintf("%sretry%d", prefix, i)
			tx, err := db.Begin()
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback()
			insertExpiryTestCommand(t, tx, id, prefix, "pending", before.Add(-time.Hour), true)
			if err := tx.Commit(); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _, _ = db.Exec("DELETE FROM channel_commands WHERE id=?", id) })
			if _, err := db.Exec("SET @expiry_test_attempts=0"); err != nil {
				t.Fatal(err)
			}
			trigger := strings.ReplaceAll(prefix, "-", "_") + "retry"
			if _, err := db.Exec(fmt.Sprintf(`CREATE TRIGGER %s BEFORE UPDATE ON operation_audits FOR EACH ROW
BEGIN IF NEW.id='%s' THEN
SET @expiry_test_attempts=COALESCE(@expiry_test_attempts,0)+1;
IF @expiry_test_attempts<=%d THEN SIGNAL SQLSTATE '45000' SET MYSQL_ERRNO=%d,MESSAGE_TEXT='injected expiry failure'; END IF;
END IF; END`, trigger, id, tc.fail, tc.code)); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _, _ = db.Exec("DROP TRIGGER IF EXISTS " + trigger) })
			n, err := New(db).ExpireStaleCommands(before)
			if n != tc.wantExpired {
				t.Fatalf("expired=%d, want %d (err=%v)", n, tc.wantExpired, err)
			}
			if tc.wantExpired == 1 && err != nil {
				t.Fatal(err)
			}
			if tc.wantExpired == 0 {
				var dbErr *mysql.MySQLError
				if !errors.As(err, &dbErr) || int(dbErr.Number) != tc.code {
					t.Fatalf("error=%v, want MySQL %d", err, tc.code)
				}
			}
			var attempts int
			if err := db.QueryRow("SELECT @expiry_test_attempts").Scan(&attempts); err != nil {
				t.Fatal(err)
			}
			if attempts != tc.attempts {
				t.Fatalf("attempts=%d, want %d", attempts, tc.attempts)
			}
			var command, audit string
			if err := db.QueryRow(`SELECT c.status,a.status FROM channel_commands c JOIN operation_audits a ON a.id=c.id WHERE c.id=?`, id).Scan(&command, &audit); err != nil {
				t.Fatal(err)
			}
			if tc.wantExpired == 1 && (command != "expired" || audit != "expired") || tc.wantExpired == 0 && (command != "pending" || audit != "submitted") {
				t.Fatalf("command=%s audit=%s after retries", command, audit)
			}
		})
	}
}
