package ingest

import (
	"testing"
	"time"

	"controltower/server/internal/agentgateway"
	"controltower/server/internal/storage"
)

func TestCommandStoreLifecycleAndFilters(t *testing.T) {
	s := NewMemoryStore()
	now := time.Now().UTC()
	for _, v := range []storage.ChannelCommand{{ID: "a", InstanceID: "i", Status: "pending", CreatedAt: now}, {ID: "old", InstanceID: "i", Status: "pending", CreatedAt: now.Add(-time.Hour)}, {ID: "other", InstanceID: "j", Status: "pending", CreatedAt: now}} {
		if e := s.CreateChannelCommand(v); e != nil {
			t.Fatal(e)
		}
	}
	if n, _ := s.ExpireStaleCommands(now.Add(-10 * time.Minute)); n != 1 {
		t.Fatalf("expired=%d", n)
	}
	got, _ := s.ClaimPendingCommands("i", now)
	if len(got) != 1 || got[0].ID != "a" {
		t.Fatalf("claim=%v", got)
	}
	again, _ := s.ClaimPendingCommands("i", now)
	if len(again) != 0 {
		t.Fatalf("second claim=%v", again)
	}
	if _, ok, _ := s.CompleteChannelCommand("a", "succeeded", "", now); !ok {
		t.Fatal("complete")
	}
	if _, ok, _ := s.CompleteChannelCommand("a", "failed", "late", now); ok {
		t.Fatal("terminal command completed twice")
	}
	items, _ := s.QueryChannelCommands(storage.ChannelCommandQuery{InstanceID: "i", Status: "succeeded"})
	if len(items) != 1 {
		t.Fatalf("filtered=%v", items)
	}
}

func TestHeartbeatClaimsCommandsAndReportAuditsOnce(t *testing.T) {
	s := NewMemoryStore()
	now := time.Now().UTC()
	_ = s.CreateChannelCommand(storage.ChannelCommand{ID: "cmd", InstanceID: "inst", ChannelID: 9, CommandType: "channel.update", PayloadJSON: `{"status":2}`, Status: "pending", CreatedBy: "admin", CreatedAt: now, UpdatedAt: now})
	_ = s.CreateChannelCommand(storage.ChannelCommand{ID: "expired", InstanceID: "inst", ChannelID: 8, CommandType: "channel.update", PayloadJSON: `{"status":2}`, Status: "pending", CreatedAt: now.Add(-time.Hour), UpdatedAt: now})
	svc := NewServiceWithCommandExpiry(s, 10*time.Minute)
	_, commands, e := svc.SaveHeartbeatWithCommands(agentgateway.AgentHeartbeatRequest{InstanceID: "inst", AgentID: "agent", ReportedAt: now})
	if e != nil || len(commands) != 1 || commands[0].ID != "cmd" || commands[0].Status == nil || *commands[0].Status != 2 {
		t.Fatalf("commands=%v err=%v", commands, e)
	}
	report := agentgateway.AgentReportRequest{InstanceID: "inst", AgentID: "agent", ReportedAt: now, CommandResults: []agentgateway.ChannelCommandResult{{ID: "cmd", ChannelID: 9, Status: "succeeded", AppliedAt: now}}}
	if e = svc.SaveReport(report); e != nil {
		t.Fatal(e)
	}
	if e = svc.SaveReport(report); e != nil {
		t.Fatal(e)
	}
	audits, _ := s.QueryOperationAudits(storage.OperationAuditQuery{InstanceID: "inst"})
	if len(audits) != 1 || audits[0].ActorID != "admin" {
		t.Fatalf("audits=%v", audits)
	}
}

func TestPruneBeforeStrictBoundaryAndKinds(t *testing.T) {
	s := NewMemoryStore()
	cutoff := time.Now().UTC()
	_, _ = s.InsertLogEvent(storage.LogEvent{InstanceID: "i", SourceLogID: 1, CreatedAt: cutoff.Add(-time.Second)})
	_, _ = s.InsertLogEvent(storage.LogEvent{InstanceID: "i", SourceLogID: 2, CreatedAt: cutoff})
	n, e := s.PruneBefore("log_events", cutoff)
	if e != nil || n != 1 {
		t.Fatalf("n=%d e=%v", n, e)
	}
	items, _ := s.QueryLogEvents(storage.LogQuery{InstanceID: "i"})
	if len(items) != 1 || !items[0].CreatedAt.Equal(cutoff) {
		t.Fatalf("items=%v", items)
	}
	if _, e = s.PruneBefore("unknown", cutoff); e == nil {
		t.Fatal("unknown kind accepted")
	}
}
