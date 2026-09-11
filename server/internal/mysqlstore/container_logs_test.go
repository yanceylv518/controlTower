package mysqlstore

import (
	"context"
	cl "controltower/internal/containerlog"
	"controltower/server/internal/storage"
	"fmt"
	"os"
	"testing"
	"time"
)

func TestContainerLogsLifecycle(t *testing.T) {
	dsn := os.Getenv("CT_MYSQL_TEST_DSN")
	if dsn == "" {
		t.Skip("requires test MySQL")
	}
	db, err := Open(dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ctx := context.Background()
	if err = ApplyDir(ctx, db, "../../migrations"); err != nil {
		t.Fatal(err)
	}
	s := New(db)
	id := fmt.Sprintf("clog-test-%d", time.Now().UnixNano())
	if err := s.CreateInstance(storage.Instance{ID: id, Enabled: true, CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC()}); err != nil {
		t.Fatal(err)
	}
	defer db.Exec("DELETE FROM instances WHERE id=?", id)
	agent := "agent-test"
	defer func() {
		for _, table := range []string{"container_log_tasks", "container_log_targets", "operation_audits"} {
			_, _ = db.Exec("DELETE FROM "+table+" WHERE instance_id=?", id)
		}
	}()
	p := cl.Poll{AgentID: agent, Sources: []cl.Source{{ID: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", Container: "new-api", Available: true}}}
	if task, e := s.PollContainerLogs(ctx, id, p); e != nil || task != nil {
		t.Fatal(task, e)
	}
	now := time.Now().UTC()
	task := cl.Task{ID: id, InstanceID: id, AgentID: agent, ActorID: 987654321, Actor: "test-operator", ActorName: "Test Operator", CreatedAt: now, Query: cl.Query{SourceID: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", Container: "new-api", From: now.Add(-time.Minute), To: now}}
	invalid := task
	invalid.Query.SourceID = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	if e := s.CreateContainerLog(ctx, invalid); e == nil {
		t.Fatal("stale source accepted")
	}
	targets, e := s.ContainerLogTargets(ctx)
	if e != nil {
		t.Fatal(e)
	}
	found := false
	for _, target := range targets {
		if target.InstanceID == id && len(target.Sources) == 1 && target.Sources[0].ID == task.Query.SourceID {
			found = true
		}
	}
	if !found {
		t.Fatal("discovered source not persisted")
	}
	if e := s.CreateContainerLog(ctx, task); e != nil {
		t.Fatal(e)
	}
	if _, e := s.GetContainerLog(ctx, id, task.ActorID+1); e == nil {
		t.Fatal("another user can read task")
	}
	if got, e := s.GetContainerLog(ctx, id, 0); e != nil || got.ActorID != task.ActorID {
		t.Fatal("full administrator cannot read another actor's task", e)
	}
	all, e := s.ListContainerLogs(ctx, 0)
	if e != nil {
		t.Fatal(e)
	}
	found = false
	for _, item := range all {
		if item.ID == id && item.ActorID == task.ActorID {
			found = true
		}
	}
	if !found {
		t.Fatal("full administrator history omitted another actor's task")
	}
	wrong := cl.Poll{AgentID: "other-agent", Sources: p.Sources}
	if got, e := s.PollContainerLogs(ctx, id, wrong); e != nil || got != nil {
		t.Fatal("another agent claimed task", e)
	}
	got, e := s.PollContainerLogs(ctx, id, p)
	if e != nil || got == nil || got.ID != id {
		t.Fatal("claim failed", e)
	}
	if got, e = s.PollContainerLogs(ctx, id, p); e != nil || got != nil {
		t.Fatal("duplicate claim", e)
	}
	result := &cl.Result{Phase: "indexing", IndexedBytes: 64 * 1024 * 1024, TotalScannedBytes: 64 * 1024 * 1024, Status: "running", Lines: []string{"redacted log"}}
	wrong.TaskID = id
	wrong.Result = result
	if ack, err := s.PollContainerLogs(ctx, id, wrong); err != nil || ack != nil {
		t.Fatal("foreign progress acknowledged", ack, err)
	}
	value, e := s.GetContainerLog(ctx, id, task.ActorID)
	if e != nil || value.Result.Status != "running" {
		t.Fatal("foreign result changed task", e)
	}
	p.TaskID = id
	p.Result = result
	if _, e = db.Exec(`UPDATE container_log_tasks SET claimed_at=UTC_TIMESTAMP()-INTERVAL 60 SECOND WHERE id=?`, id); e != nil {
		t.Fatal(e)
	}
	if ack, err := s.PollContainerLogs(ctx, id, p); err != nil || ack == nil || ack.ID != id {
		t.Fatal("progress not acknowledged on same task", ack, err)
	}
	var renewed bool
	if e = db.QueryRow(`SELECT claimed_at > UTC_TIMESTAMP()-INTERVAL 10 SECOND FROM container_log_tasks WHERE id=?`, id).Scan(&renewed); e != nil || !renewed {
		t.Fatal("progress did not renew lease", e)
	}
	result.Status = "succeeded"
	result.Complete = true
	result.Phase = "complete"
	result.TotalScannedBytes *= 2
	if _, e = s.PollContainerLogs(ctx, id, p); e != nil {
		t.Fatal(e)
	}
	value, e = s.GetContainerLog(ctx, id, task.ActorID)
	if e != nil || value.Result.Status != "succeeded" || len(value.Result.Lines) != 1 || value.Result.TotalScannedBytes != result.TotalScannedBytes || value.Result.IndexedBytes != result.IndexedBytes || !value.Result.Complete {
		t.Fatal("result not stored", value, e)
	}
	p.Result = &cl.Result{Status: "failed"}
	repeated := task.Query
	repeated.BatchID = "new-batch"
	if cached, err := s.FindReusableContainerLog(ctx, task.InstanceID, task.AgentID, task.ActorID, repeated); err != nil || cached.ID != task.ID {
		t.Fatalf("complete query not reused: %+v %v", cached, err)
	}
	if _, err := s.FindReusableContainerLog(ctx, task.InstanceID, task.AgentID, task.ActorID+1, repeated); err == nil {
		t.Fatal("another actor reused private logs")
	}
	repeated.Keyword = "different-condition"
	if _, err := s.FindReusableContainerLog(ctx, task.InstanceID, task.AgentID, task.ActorID, repeated); err == nil {
		t.Fatal("different query reused")
	}
	if _, e = s.PollContainerLogs(ctx, id, p); e != nil {
		t.Fatal(e)
	}
	value, e = s.GetContainerLog(ctx, id, task.ActorID)
	if e != nil || value.Result.Status != "succeeded" {
		t.Fatal("replay overwrote result", e)
	}
	var actor, status string
	if e = s.UpdateInstance(id, "", "", false, now); e != nil {
		t.Fatal(e)
	}
	visible, err := s.ContainerLogTargets(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, target := range visible {
		if target.InstanceID == id {
			t.Fatal("disabled target visible")
		}
	}
	blocked := task
	blocked.ID += "-disabled"
	if err := s.CreateContainerLog(ctx, blocked); err == nil {
		t.Fatal("disabled target accepted query")
	}
	if e = s.UpdateInstance(id, "", "", true, now); e != nil {
		t.Fatal(e)
	}
	if e = db.QueryRow(`SELECT actor_id,status FROM operation_audits WHERE id=?`, id).Scan(&actor, &status); e != nil || actor != "test-operator" || status != "succeeded" {
		t.Fatal("audit missing", actor, status, e)
	}
	task.ID = id + "-expire"
	if e = s.CreateContainerLog(ctx, task); e != nil {
		t.Fatal(e)
	}
	if _, e = db.Exec(`UPDATE container_log_tasks SET created_at=UTC_TIMESTAMP()-INTERVAL 61 MINUTE WHERE id=?`, task.ID); e != nil {
		t.Fatal(e)
	}
	value, e = s.GetContainerLog(ctx, task.ID, task.ActorID)
	if e != nil || value.Result.Status != "timed_out" {
		t.Fatal("queued task not expired", e)
	}
}

// Multi-batch tasks upload cumulative partial results; losing the lease must
// mark the task timed out without discarding the lines already uploaded.
func TestContainerLogsLeaseExpiryKeepsPartialLines(t *testing.T) {
	dsn := os.Getenv("CT_MYSQL_TEST_DSN")
	if dsn == "" {
		t.Skip("requires test MySQL")
	}
	db, err := Open(dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ctx := context.Background()
	if err = ApplyDir(ctx, db, "../../migrations"); err != nil {
		t.Fatal(err)
	}
	s := New(db)
	id := fmt.Sprintf("clog-lease-%d", time.Now().UnixNano())
	if err := s.CreateInstance(storage.Instance{ID: id, Enabled: true, CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC()}); err != nil {
		t.Fatal(err)
	}
	defer db.Exec("DELETE FROM instances WHERE id=?", id)
	defer func() {
		for _, table := range []string{"container_log_tasks", "container_log_targets", "operation_audits"} {
			_, _ = db.Exec("DELETE FROM "+table+" WHERE instance_id=?", id)
		}
	}()
	source := "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	p := cl.Poll{AgentID: "agent-lease", Sources: []cl.Source{{ID: source, Container: "new-api", Available: true}}}
	if _, e := s.PollContainerLogs(ctx, id, p); e != nil {
		t.Fatal(e)
	}
	now := time.Now().UTC()
	task := cl.Task{ID: id + "-t", InstanceID: id, AgentID: "agent-lease", ActorID: 7, Actor: "u", Query: cl.Query{SourceID: source, Container: "new-api", From: now.Add(-10 * time.Minute), To: now}, CreatedAt: now, Result: cl.Result{Status: "pending", Lines: []string{}}}
	if e := s.CreateContainerLog(ctx, task); e != nil {
		t.Fatal(e)
	}
	claimed, e := s.PollContainerLogs(ctx, id, p)
	if e != nil || claimed == nil {
		t.Fatalf("claim: %v %v", claimed, e)
	}
	progress := p
	progress.TaskID = task.ID
	progress.Result = &cl.Result{Status: "running", Lines: []string{"a.log [byte 1] first batch"}, NextCursor: source, Note: "后台正在自动处理下一批"}
	if _, e = s.PollContainerLogs(ctx, id, progress); e != nil {
		t.Fatal(e)
	}
	if _, e = db.Exec(`UPDATE container_log_tasks SET claimed_at=UTC_TIMESTAMP()-INTERVAL 120 SECOND WHERE id=?`, task.ID); e != nil {
		t.Fatal(e)
	}
	if e = s.expireContainerLogs(ctx); e != nil {
		t.Fatal(e)
	}
	got, e := s.GetContainerLog(ctx, task.ID, 7)
	if e != nil {
		t.Fatal(e)
	}
	if got.Result.Status != "timed_out" || got.Result.Complete || len(got.Result.Lines) != 1 || got.Result.Error == "" {
		t.Fatalf("lease expiry discarded partial lines or status wrong: %+v", got.Result)
	}
	var status string
	if e = db.QueryRow(`SELECT status FROM container_log_tasks WHERE id=?`, task.ID).Scan(&status); e != nil || status != "timed_out" {
		t.Fatalf("row status = %s %v", status, e)
	}
}
