package mysqlstore

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"controltower/server/internal/storage"
)

// Opt-in: creates 100k synthetic records in an isolated test database.
func TestOperationAuditPerformanceMySQLIntegration(t *testing.T) {
	if os.Getenv("CT_AUDIT_PERF_TEST") != "1" {
		t.Skip("set CT_AUDIT_PERF_TEST=1 on an isolated test database")
	}
	db, err := Open(os.Getenv("CT_MYSQL_TEST_DSN"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ctx := context.Background()
	if err = ApplyDir(ctx, db, "../../migrations"); err != nil {
		t.Fatal(err)
	}
	prefix := fmt.Sprintf("audit-perf-%d-", time.Now().UnixNano())
	defer db.Exec("DELETE FROM operation_audits WHERE instance_id=?", prefix)
	digits := `(SELECT 0 d UNION ALL SELECT 1 UNION ALL SELECT 2 UNION ALL SELECT 3 UNION ALL SELECT 4 UNION ALL SELECT 5 UNION ALL SELECT 6 UNION ALL SELECT 7 UNION ALL SELECT 8 UNION ALL SELECT 9)`
	seed := `INSERT INTO operation_audits(id,instance_id,operation_type,target_type,target_id,actor_id,actor_type,trigger_type,request_id,before_summary,after_summary,status,created_at,updated_at)
SELECT CONCAT(?,LPAD(n,6,'0')),?,'settings.update','configuration','global',IF(MOD(n,5)=0,'admin','system:engine'),IF(MOD(n,5)=0,'human','system'),IF(MOD(n,5)=0,'manual','automatic'),CONCAT(?,n),'{}','{}','succeeded',TIMESTAMPADD(SECOND,n,'2026-09-23 00:00:00'),'2026-09-24 00:00:00'
FROM (SELECT a.d+10*b.d+100*c.d+1000*d.d+10000*e.d n FROM ` + digits + ` a CROSS JOIN ` + digits + ` b CROSS JOIN ` + digits + ` c CROSS JOIN ` + digits + ` d CROSS JOIN ` + digits + ` e) numbers`
	if _, err = db.Exec(seed, prefix, prefix, prefix); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec("ANALYZE TABLE operation_audits"); err != nil {
		t.Fatal(err)
	}
	s := New(db)
	from := time.Date(2026, 9, 24, 0, 0, 0, 0, time.UTC)
	q := storage.OperationAuditQuery{From: from, To: from.Add(24 * time.Hour), ListOnly: true, Limit: 20}
	started := time.Now()
	first, err := s.QueryOperationAuditsContext(ctx, q)
	if err != nil || len(first.Items) != 20 || !first.HasMore || first.Total != -1 {
		t.Fatalf("first=%+v err=%v", first, err)
	}
	t.Logf("100k fixture first list: %s", time.Since(started))
	q.ListOnly, q.CountOnly = false, true
	started = time.Now()
	count, err := s.QueryOperationAuditsContext(ctx, q)
	if err != nil || count.Total != 2720 {
		t.Fatalf("count=%d err=%v", count.Total, err)
	}
	t.Logf("indexed day count: %s", time.Since(started))
	started = time.Now()
	_, err = s.QueryOperationAuditsContext(ctx, q)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("cached count: %s", time.Since(started))
	oldPredicate := `COALESCE(actor_type,'') <> 'system' AND COALESCE(trigger_type,'') <> 'automatic' AND COALESCE(operation_type,'') <> 'tuning.auto_execute' AND ((actor_type='human' AND actor_role IN ('admin','viewer') AND auth_method IN ('session','web_session')) OR (COALESCE(actor_id,'') NOT IN ('system','agent') AND COALESCE(actor_id,'') NOT LIKE 'system:%' AND COALESCE(actor_id,'') NOT LIKE 'agent:%'))`
	var mismatch int
	if err = db.QueryRow("SELECT COUNT(*) FROM operation_audits WHERE is_manual_audit <> (" + oldPredicate + ")").Scan(&mismatch); err != nil || mismatch != 0 {
		t.Fatalf("predicate mismatch=%d err=%v", mismatch, err)
	}
	started = time.Now()
	var oldCount int64
	if err = db.QueryRow("SELECT COUNT(*) FROM operation_audits WHERE "+oldPredicate+" AND created_at>=? AND created_at<?", q.From, q.To).Scan(&oldCount); err != nil || oldCount != count.Total {
		t.Fatalf("old count=%d err=%v", oldCount, err)
	}
	t.Logf("old predicate same day count: %s", time.Since(started))
	q.ListOnly, q.CountOnly = true, false
	q.BeforeTime, q.BeforeID = first.Items[19].CreatedAt, first.Items[19].ID
	next, err := s.QueryOperationAuditsContext(ctx, q)
	if err != nil || len(next.Items) != 20 || !next.Items[0].CreatedAt.Before(q.BeforeTime) {
		t.Fatalf("cursor err=%v next=%+v", err, next)
	}
	var plan string
	if err = db.QueryRow("EXPLAIN FORMAT=JSON SELECT id FROM operation_audits WHERE is_manual_audit=1 AND created_at>=? AND created_at<? ORDER BY created_at DESC,id DESC LIMIT 21", q.From, q.To).Scan(&plan); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(plan, `"key": "idx_operation_audits_manual_created"`) || strings.Contains(plan, `"using_filesort": true`) {
		t.Fatalf("unexpected plan: %s", plan)
	}
	t.Log("list uses manual_created index without filesort")
	if err = db.QueryRow("EXPLAIN FORMAT=JSON UPDATE operation_audits SET http_status=200 WHERE request_id=?", prefix+"10").Scan(&plan); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(plan, `"key": "idx_operation_audits_request"`) {
		t.Fatalf("request update plan: %s", plan)
	}
	cancelCtx, cancel := context.WithCancel(ctx)
	cancel()
	_, err = s.QueryOperationAuditsContext(cancelCtx, q)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled list: %v", err)
	}
}

func TestOperationAuditCursorCancellationMySQLIntegration(t *testing.T) {
	dsn := os.Getenv("CT_MYSQL_TEST_DSN")
	if dsn == "" {
		t.Skip("set CT_MYSQL_TEST_DSN")
	}
	db, err := Open(dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err = ApplyDir(context.Background(), db, "../../migrations"); err != nil {
		t.Fatal(err)
	}
	s := New(db)
	prefix := fmt.Sprintf("audit-cursor-%d", time.Now().UnixNano())
	defer db.Exec("DELETE FROM operation_audits WHERE instance_id=?", prefix)
	at := time.Now().UTC().Truncate(time.Microsecond)
	for i := 0; i < 4; i++ {
		if err = s.InsertOperationAudit(storage.OperationAudit{ID: fmt.Sprintf("%s-%d", prefix, i), InstanceID: prefix, OperationType: "settings.update", ActorID: "admin", CreatedAt: at}); err != nil {
			t.Fatal(err)
		}
	}
	q := storage.OperationAuditQuery{InstanceID: prefix, ListOnly: true, Limit: 2}
	first, err := s.QueryOperationAuditsContext(context.Background(), q)
	if err != nil || len(first.Items) != 2 || !first.HasMore {
		t.Fatalf("first=%+v err=%v", first, err)
	}
	if err = s.InsertOperationAudit(storage.OperationAudit{ID: prefix + "-new", InstanceID: prefix, OperationType: "settings.update", ActorID: "admin", CreatedAt: at.Add(time.Second)}); err != nil {
		t.Fatal(err)
	}
	q.BeforeTime, q.BeforeID = first.Items[1].CreatedAt, first.Items[1].ID
	next, err := s.QueryOperationAuditsContext(context.Background(), q)
	if err != nil || len(next.Items) != 2 || next.HasMore || next.Items[0].ID != prefix+"-1" || next.Items[1].ID != prefix+"-0" {
		t.Fatalf("next=%+v err=%v", next, err)
	}
	// Hold the sole connection: cancellation must interrupt pool waiting rather
	// than continuing with a background context after the HTTP request leaves.
	db.SetMaxOpenConns(1)
	conn, err := db.Conn(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	_, err = s.QueryOperationAuditsContext(ctx, q)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("pool wait cancellation=%v", err)
	}
}
