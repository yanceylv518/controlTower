package ingest

import (
	"strings"
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
	_ = s.CreateChannelCommand(storage.ChannelCommand{ID: "cmd", InstanceID: "inst", ChannelID: 9, CommandType: "channel.update", PayloadJSON: `{"status":2,"group":"default,vip","before_group":"default"}`, Status: "pending", CreatedBy: "admin", CreatedAt: now, UpdatedAt: now})
	_ = s.CreateChannelCommand(storage.ChannelCommand{ID: "expired", InstanceID: "inst", ChannelID: 8, CommandType: "channel.update", PayloadJSON: `{"status":2}`, Status: "pending", CreatedAt: now.Add(-time.Hour), UpdatedAt: now})
	svc := NewServiceWithCommandExpiry(s, 10*time.Minute)
	_, commands, e := svc.SaveHeartbeatWithCommands(agentgateway.AgentHeartbeatRequest{InstanceID: "inst", AgentID: "agent", ReportedAt: now})
	if e != nil || len(commands) != 1 || commands[0].ID != "cmd" || commands[0].Status == nil || *commands[0].Status != 2 || commands[0].Group == nil || *commands[0].Group != "default,vip" {
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
	if len(audits.Items) != 1 || audits.Items[0].ActorID != "admin" {
		t.Fatalf("audits=%v", audits)
	}
	if audits.Items[0].BeforeSummary != `{"group":"default"}` || !strings.Contains(audits.Items[0].AfterSummary, `"group":"default,vip"`) || !strings.Contains(audits.Items[0].AfterSummary, `"status":"succeeded"`) {
		t.Fatalf("group audit must retain before/after/result: %#v", audits.Items[0])
	}
}

func TestOperationAuditSkipsAutomaticAndKeepsManualServiceToken(t *testing.T) {
	s := NewMemoryStore()
	now := time.Now().UTC()
	if err := s.InsertOperationAudit(storage.OperationAudit{ID: "cmd-audit", OperationType: "tuning.auto_execute", ActorID: "system:auto", SourceComponent: "tuning", TriggerType: "automatic", Status: "submitted", CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
	if err := s.InsertOperationAudit(storage.OperationAudit{ID: "cmd-audit", OperationType: "channel.update", ActorID: "system:auto", Status: "failed", ErrorSummary: "agent rejected update", HTTPStatus: 0, AfterSummary: `{"result":"failed"}`, CreatedAt: now.Add(time.Minute), UpdatedAt: now.Add(time.Minute)}); err != nil {
		t.Fatal(err)
	}
	if err := s.InsertOperationAudit(storage.OperationAudit{ID: "manual-audit", OperationType: "channel.update", ActorID: "release-bot", ActorType: "service_token", SourceComponent: "channels", Status: "submitted", CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
	if err := s.InsertOperationAudit(storage.OperationAudit{ID: "manual-audit", OperationType: "channel.update", ActorID: "release-bot", ActorType: "service_token", SourceComponent: "channels", Status: "failed", ErrorSummary: "agent rejected update", HTTPStatus: 502, AfterSummary: `{"result":"failed"}`, CreatedAt: now.Add(time.Minute), UpdatedAt: now.Add(time.Minute)}); err != nil {
		t.Fatal(err)
	}
	page, err := s.QueryOperationAudits(storage.OperationAuditQuery{Source: "channels", Trigger: "manual", Status: "failed", Search: "rejected", Limit: 20})
	if err != nil {
		t.Fatal(err)
	}
	if page.Total != 1 || len(page.Items) != 1 || page.Items[0].Status != "failed" || page.Items[0].ActorType != "service_token" || page.Items[0].AuthMethod != "bearer" || page.Items[0].ErrorSummary != "agent rejected update" {
		t.Fatalf("manual API operation was not retained correctly: %+v", page)
	}
}

func TestOperationAuditHTTPStatusUpdateIsIdempotent(t *testing.T) {
	store := NewMemoryStore()
	if err := store.UpdateOperationAuditHTTPStatus("missing-request", 200); err != nil {
		t.Fatalf("unchanged operations may have no audit: %v", err)
	}
	if err := store.InsertOperationAudit(storage.OperationAudit{ID: "status-audit", OperationType: "settings.update", ActorID: "admin", RequestID: "request-1", Status: "succeeded"}); err != nil {
		t.Fatal(err)
	}
	if err := store.UpdateOperationAuditHTTPStatus("request-1", 204); err != nil {
		t.Fatal(err)
	}
	page, err := store.QueryOperationAudits(storage.OperationAuditQuery{RequestID: "request-1", Limit: 1})
	if err != nil || page.Total != 1 || page.Items[0].HTTPStatus != 204 {
		t.Fatalf("semantic HTTP status update failed: page=%+v err=%v", page, err)
	}
}

func TestAuditActorOptionsSearchAllPagesAndExcludeAutomation(t *testing.T) {
	s := NewMemoryStore()
	for _, v := range []storage.OperationAudit{
		{ID: "a", OperationType: "settings.update", ActorID: "Admin-A", Status: "success"},
		{ID: "b", OperationType: "settings.update", ActorID: "Admin-A", Status: "succeeded"},
		{ID: "c", OperationType: "settings.update", ActorID: "Admin-B", Status: "failed"},
		{ID: "d", OperationType: "tuning.auto_execute", ActorID: "system:Admin"},
	} {
		if err := s.InsertOperationAudit(v); err != nil {
			t.Fatal(err)
		}
	}
	page, err := s.QueryOperationAudits(storage.OperationAuditQuery{ActorOptions: true, Actor: "admin", Limit: 1, Offset: 99})
	if err != nil || len(page.Actors) != 2 || page.Actors[0] != "Admin-A" || page.Actors[1] != "Admin-B" {
		t.Fatalf("actors=%v err=%v", page.Actors, err)
	}
	page, err = s.QueryOperationAudits(storage.OperationAuditQuery{Status: "succeeded"})
	if err != nil || page.Total != 2 {
		t.Fatalf("success aliases: %+v %v", page, err)
	}
	page, err = s.QueryOperationAudits(storage.OperationAuditQuery{Actor: "Admin", ActorExact: true})
	if err != nil || page.Total != 0 {
		t.Fatalf("exact actor matched partial name: %+v %v", page, err)
	}
	page, err = s.QueryOperationAudits(storage.OperationAuditQuery{Actor: "admin-a", ActorExact: true})
	if err != nil || page.Total != 2 {
		t.Fatalf("exact actor: %+v %v", page, err)
	}
}

func TestOperationAuditTypeModuleFilterMatchesHistoricHTTPEntries(t *testing.T) {
	s := NewMemoryStore()
	now := time.Now().UTC()
	for _, audit := range []storage.OperationAudit{
		{ID: "audit-system-1", OperationType: "http.system.instances.post", Status: "succeeded", CreatedAt: now, UpdatedAt: now},
		{ID: "audit-system-2", OperationType: "http.system.settings.put", Status: "failed", CreatedAt: now.Add(time.Second), UpdatedAt: now.Add(time.Second)},
		{ID: "audit-billing", OperationType: "http.billing.discounts.post", Status: "succeeded", CreatedAt: now.Add(2 * time.Second), UpdatedAt: now.Add(2 * time.Second)},
	} {
		if err := s.InsertOperationAudit(audit); err != nil {
			t.Fatal(err)
		}
	}

	page, err := s.QueryOperationAudits(storage.OperationAuditQuery{OperationType: "http.system.*", Limit: 20})
	if err != nil {
		t.Fatal(err)
	}
	wantTypes := storage.OperationAuditFilterTypes()
	if page.Total != 2 || len(page.Items) != 2 || len(page.OperationTypes) != len(wantTypes) {
		t.Fatalf("module filter should match historic entries and return the fixed catalog: %+v", page)
	}
	for index, want := range wantTypes {
		if page.OperationTypes[index] != want {
			t.Fatalf("query returned data-dependent operation types: got %v, want %v", page.OperationTypes, wantTypes)
		}
	}
}

func TestCommandResultAuditRedactsAgentError(t *testing.T) {
	s := NewMemoryStore()
	now := time.Now().UTC()
	command := storage.ChannelCommand{ID: "redacted-command", InstanceID: "instance-a", ChannelID: 3, CommandType: "channel.update", PayloadJSON: `{"weight":1}`, Status: "pending", CreatedBy: "operator", CreatedAt: now, UpdatedAt: now}
	if err := s.CreateChannelCommand(command); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ClaimPendingCommands("instance-a", now); err != nil {
		t.Fatal(err)
	}
	service := NewService(s)
	report := agentgateway.AgentReportRequest{InstanceID: "instance-a", AgentID: "agent-a", ReportedAt: now, CommandResults: []agentgateway.ChannelCommandResult{{ID: command.ID, Status: "failed", Error: "Authorization: Bearer hidden-value token=hidden-token"}}}
	if err := service.SaveReport(report); err != nil {
		t.Fatal(err)
	}
	page, err := s.QueryOperationAudits(storage.OperationAuditQuery{Limit: 20})
	if err != nil {
		t.Fatal(err)
	}
	if page.Total != 1 {
		t.Fatalf("command result audit missing: %+v", page.Items)
	}
	for _, secret := range []string{"hidden-value", "hidden-token"} {
		if strings.Contains(page.Items[0].ErrorSummary+page.Items[0].AfterSummary, secret) {
			t.Fatalf("agent error leaked %q: %+v", secret, page.Items[0])
		}
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
