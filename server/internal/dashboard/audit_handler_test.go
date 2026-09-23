package dashboard

import (
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"testing"
	"time"

	"controltower/server/internal/ingest"
	"controltower/server/internal/storage"
)

func TestAuditsServerPaginationSiteFilterAndDetails(t *testing.T) {
	store := ingest.NewMemoryStore()
	now := time.Date(2026, 9, 23, 3, 0, 0, 0, time.UTC)
	for _, instance := range []storage.Instance{
		{ID: "instance-a", SiteID: "site-a", Enabled: true, CreatedAt: now, UpdatedAt: now},
		{ID: "instance-b", SiteID: "site-b", Enabled: true, CreatedAt: now, UpdatedAt: now},
	} {
		if err := store.CreateInstance(instance); err != nil {
			t.Fatal(err)
		}
	}
	for _, audit := range []storage.OperationAudit{
		{ID: "a1", InstanceID: "instance-a", OperationType: "billing.discount.update", TargetType: "billing", TargetID: "9", ActorID: "alice", ActorType: "human", ActorRole: "admin", SourceComponent: "billing", TriggerType: "manual", BeforeSummary: `{"discount":"0.8"}`, AfterSummary: `{"discount":"0.9"}`, Status: "succeeded", CreatedAt: now, UpdatedAt: now},
		{ID: "a2", InstanceID: "instance-a", OperationType: "billing.discount.delete", TargetType: "billing", TargetID: "10", ActorID: "bob", SourceComponent: "billing", TriggerType: "manual", Status: "succeeded", CreatedAt: now.Add(time.Second), UpdatedAt: now.Add(time.Second)},
		{ID: "b1", InstanceID: "instance-b", OperationType: "billing.discount.update", TargetType: "billing", TargetID: "11", ActorID: "alice", SourceComponent: "billing", TriggerType: "manual", Status: "succeeded", CreatedAt: now.Add(2 * time.Second), UpdatedAt: now.Add(2 * time.Second)},
	} {
		if err := store.InsertOperationAudit(audit); err != nil {
			t.Fatal(err)
		}
	}
	handler := CommandHandler{Store: store, Instances: store}
	w := httptest.NewRecorder()
	r := httptest.NewRequest("GET", "/api/dashboard/operation-audits?site_id=site-a&actor=alice&limit=1&offset=0", nil)
	handler.Audits(w, r)
	if w.Code != 200 {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	var response struct {
		Items          []operationAuditItem `json:"items"`
		Total          int64                `json:"total"`
		OperationTypes []string             `json:"operation_types"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if response.Total != 1 || len(response.Items) != 1 || response.Items[0].ID != "a1" || response.Items[0].BeforeSummary != `{"discount":"0.8"}` || response.Items[0].ActorRole != "admin" {
		t.Fatalf("unexpected filtered page: %+v", response)
	}
	wantOperationTypes := storage.OperationAuditFilterTypes()
	if len(response.OperationTypes) != len(wantOperationTypes) {
		t.Fatalf("operation type catalog should be fixed: got %d options, want %d", len(response.OperationTypes), len(wantOperationTypes))
	}
	for index, operationType := range wantOperationTypes {
		if response.OperationTypes[index] != operationType {
			t.Fatalf("operation type catalog changed at %d: got %q, want %q", index, response.OperationTypes[index], operationType)
		}
	}
}

func TestAuditsRejectsMalformedTimeAndStatus(t *testing.T) {
	store := ingest.NewMemoryStore()
	handler := CommandHandler{Store: store}
	for _, query := range []string{"?from=not-a-date", "?status=made-up"} {
		w := httptest.NewRecorder()
		handler.Audits(w, httptest.NewRequest("GET", "/api/dashboard/operation-audits"+query, nil))
		if w.Code != 400 {
			t.Fatalf("query %q status=%d body=%s", query, w.Code, w.Body.String())
		}
	}
}

func TestAuditsFiltersAndPaginatesLargerMockHistory(t *testing.T) {
	store := ingest.NewMemoryStore()
	now := time.Date(2026, 9, 23, 3, 0, 0, 0, time.UTC)
	actors := []string{"root-admin", "audit-admin", "billing-admin", "tuning-admin", "system:auto"}
	for index := 0; index < 60; index++ {
		actor := actors[index%len(actors)]
		status := []string{"succeeded", "failed", "submitted"}[index%3]
		source, operation, trigger := "settings", "settings.update", "manual"
		if index%2 == 1 {
			source, operation = "tuning", "tuning.manual_execute"
		}
		if actor == "system:auto" {
			trigger = "automatic"
		}
		instanceID := "instance-a"
		if index%2 == 1 {
			instanceID = "instance-b"
		}
		at := now.Add(time.Duration(index) * time.Second)
		if err := store.InsertOperationAudit(storage.OperationAudit{
			ID: fmt.Sprintf("mock-%02d", index), InstanceID: instanceID,
			OperationType: operation, TargetType: "configuration", TargetID: fmt.Sprintf("target-%02d", index),
			ActorID: actor, SourceComponent: source, TriggerType: trigger,
			RequestID: fmt.Sprintf("request-%02d", index), CorrelationID: fmt.Sprintf("correlation-%02d", index),
			BeforeSummary: `{"enabled":false}`, AfterSummary: `{"enabled":true}`,
			Status: status, CreatedAt: at, UpdatedAt: at,
		}); err != nil {
			t.Fatalf("insert mock audit %d: %v", index, err)
		}
	}
	handler := CommandHandler{Store: store}
	query := func(value string) (int, struct {
		Items []struct {
			ID        string `json:"id"`
			ActorID   string `json:"actor_id"`
			Operation string `json:"operation_type"`
		} `json:"items"`
		Total          int64    `json:"total"`
		OperationTypes []string `json:"operation_types"`
	}) {
		w := httptest.NewRecorder()
		handler.Audits(w, httptest.NewRequest("GET", "/api/dashboard/operation-audits?"+value, nil))
		var response struct {
			Items []struct {
				ID        string `json:"id"`
				ActorID   string `json:"actor_id"`
				Operation string `json:"operation_type"`
			} `json:"items"`
			Total          int64    `json:"total"`
			OperationTypes []string `json:"operation_types"`
		}
		if w.Code != 200 {
			t.Fatalf("query %q status=%d body=%s", value, w.Code, w.Body.String())
		}
		if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
			t.Fatalf("query %q decode: %v", value, err)
		}
		return w.Code, response
	}
	_, actorPage := query("actor=billing-admin&limit=4&offset=3")
	if actorPage.Total != 12 || len(actorPage.Items) != 4 || actorPage.Items[0].ID != "mock-42" {
		t.Fatalf("unexpected actor page: total=%d items=%+v", actorPage.Total, actorPage.Items)
	}
	_, failedPage := query("status=failed&limit=100")
	if failedPage.Total != 16 || len(failedPage.Items) != 16 {
		t.Fatalf("unexpected failed status count: total=%d items=%d", failedPage.Total, len(failedPage.Items))
	}
	_, correlated := query("request_id=request-17&source=tuning&trigger=manual")
	if correlated.Total != 1 || len(correlated.Items) != 1 || correlated.Items[0].ActorID != "billing-admin" || correlated.Items[0].Operation != "tuning.manual_execute" {
		t.Fatalf("unexpected correlated operation: %+v", correlated)
	}
	_, timeFiltered := query("from=2026-09-23T03:00:10Z&to=2026-09-23T03:00:20Z&limit=100")
	if timeFiltered.Total != 8 || len(timeFiltered.Items) != 8 || timeFiltered.Items[0].ID != "mock-18" || timeFiltered.Items[7].ID != "mock-10" {
		t.Fatalf("time filters should include the start and exclude the end: %+v", timeFiltered)
	}
	_, emptyPage := query("actor=billing-admin&limit=4&offset=100")
	if emptyPage.Total != 12 || len(emptyPage.Items) != 0 {
		t.Fatalf("out-of-range page should keep total and return no records: %+v", emptyPage)
	}
	if len(emptyPage.OperationTypes) != len(storage.OperationAuditFilterTypes()) {
		t.Fatalf("fixed operation type catalog should remain available when a filter returns no rows: %v", emptyPage.OperationTypes)
	}
}
