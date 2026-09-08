package mysqlstore

import (
	"context"
	cl "controltower/internal/containerlog"
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
	if _, e = s.PollContainerLogs(ctx, id, p); e != nil {
		t.Fatal(e)
	}
	value, e = s.GetContainerLog(ctx, id, task.ActorID)
	if e != nil || value.Result.Status != "succeeded" {
		t.Fatal("replay overwrote result", e)
	}
	var actor, status string
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
