package mysqlstore

import (
	"context"
	cl "controltower/internal/containerlog"
	"controltower/server/internal/storage"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"
)

func TestNginxLogInstanceSources(t *testing.T) {
	if os.Getenv("CT_MYSQL_TEST_DSN") == "" {
		t.Skip("requires test MySQL")
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
	s := New(db)
	id := fmt.Sprintf("nginx-%d", time.Now().UnixNano())
	now := time.Now().UTC()
	if err = s.CreateInstance(storage.Instance{ID: id, BaseURL: "http://127.0.0.1:3000", Enabled: true, CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
	defer db.Exec("DELETE FROM instances WHERE id=?", id)
	defer func() {
		for _, table := range []string{"container_log_tasks", "container_log_targets", "operation_audits"} {
			db.Exec("DELETE FROM "+table+" WHERE instance_id=?", id)
		}
	}()
	source := cl.Source{ID: strings.Repeat("a", 64), Container: "nginx", Kind: "nginx_access", Domains: []string{"one.example", "two.example"}, Fields: []string{"status", "host", "time"}, Available: true}
	if _, err = s.PollContainerLogs(ctx, id, cl.Poll{AgentID: "agent", Sources: []cl.Source{source}}); err != nil {
		t.Fatal(err)
	}
	targets, err := s.ContainerLogTargets(ctx)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, target := range targets {
		if target.InstanceID == id {
			found = true
			if !target.Sources[0].Available || target.Sources[0].QueryHost != "" {
				t.Fatal("local source should not require a domain binding")
			}
		}
	}
	if !found {
		t.Fatal("missing target")
	}
	task := cl.Task{ID: id, InstanceID: id, AgentID: "agent", ActorID: 1, Actor: "admin", CreatedAt: now, Query: cl.Query{Kind: "nginx_access", Host: "unknown.example", SourceID: source.ID, Container: source.Container, From: now.Add(-time.Minute), To: now}}
	if s.CreateContainerLog(ctx, task) == nil {
		t.Fatal("undiscovered domain filter accepted")
	}
	task.Query.Host = ""
	task.Query.Path = "/unsupported"
	if s.CreateContainerLog(ctx, task) == nil {
		t.Fatal("unsupported filter accepted")
	}
	task.Query.Path = ""
	if err = s.CreateContainerLog(ctx, task); err != nil {
		t.Fatal(err)
	}
}
