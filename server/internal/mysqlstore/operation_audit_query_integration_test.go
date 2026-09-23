package mysqlstore

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"controltower/server/internal/storage"
)

func TestQueryOperationAuditsTypesAndTimeRangeMySQLIntegration(t *testing.T) {
	dsn := os.Getenv("CT_MYSQL_TEST_DSN")
	if dsn == "" {
		t.Skip("set CT_MYSQL_TEST_DSN to run operation audit query integration test")
	}
	db, err := Open(dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := ApplyDir(context.Background(), db, "../../migrations"); err != nil {
		t.Fatal(err)
	}

	instanceID := fmt.Sprintf("audit-query-%d", time.Now().UnixNano())
	now := time.Now().UTC().Truncate(time.Microsecond)
	defer func() {
		if _, err := db.Exec(`DELETE FROM operation_audits WHERE instance_id=?`, instanceID); err != nil {
			t.Errorf("cleanup operation audits: %v", err)
		}
	}()

	operations := []storage.OperationAudit{
		{ID: "audit-query-start-" + instanceID, InstanceID: instanceID, OperationType: "settings.update", TargetType: "configuration", TargetID: "start", ActorID: "test", Status: "succeeded", CreatedAt: now, UpdatedAt: now},
		{ID: "audit-query-middle-" + instanceID, InstanceID: instanceID, OperationType: "billing.price_update", TargetType: "configuration", TargetID: "middle", ActorID: "test", Status: "succeeded", CreatedAt: now.Add(time.Second), UpdatedAt: now.Add(time.Second)},
		{ID: "audit-query-end-" + instanceID, InstanceID: instanceID, OperationType: "http.system.settings.put", TargetType: "configuration", TargetID: "end", ActorID: "test", Status: "succeeded", CreatedAt: now.Add(2 * time.Second), UpdatedAt: now.Add(2 * time.Second)},
		{ID: "audit-query-channel-groups-" + instanceID, InstanceID: instanceID, OperationType: "http.channel_group_presets.group_presets.put", TargetType: "configuration", TargetID: "groups", ActorID: "test", Status: "succeeded", CreatedAt: now.Add(3 * time.Second), UpdatedAt: now.Add(3 * time.Second)},
	}
	store := New(db)
	automatic := storage.OperationAudit{ID: "auto-insert-" + instanceID, InstanceID: instanceID, OperationType: "tuning.auto_execute", ActorID: "system:tuning", Status: "succeeded", CreatedAt: now, UpdatedAt: now}
	if err := store.InsertOperationAudit(automatic); err != nil {
		t.Fatal(err)
	}
	tx, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if err := insertOperationAuditTx(tx, automatic); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	var automaticCount int
	if err := db.QueryRow(`SELECT COUNT(*) FROM operation_audits WHERE id=?`, automatic.ID).Scan(&automaticCount); err != nil || automaticCount != 0 {
		t.Fatalf("automatic audit persisted: count=%d err=%v", automaticCount, err)
	}
	if err := store.UpdateOperationAuditHTTPStatus("missing-"+instanceID, 200); err != nil {
		t.Fatal(err)
	}
	for _, operation := range operations {
		if err := store.InsertOperationAudit(operation); err != nil {
			t.Fatalf("insert audit %s: %v", operation.ID, err)
		}
	}
	for _, audit := range []struct {
		id, operationType, actorID, actorType, triggerType string
	}{
		{"audit-query-auto-operation-" + instanceID, "tuning.auto_execute", "test", "human", "manual"},
		{"audit-query-auto-trigger-" + instanceID, "settings.update", "test", "human", "automatic"},
		{"audit-query-system-actor-" + instanceID, "settings.update", "system", "human", "manual"},
	} {
		if _, err := db.Exec(`INSERT INTO operation_audits(id,instance_id,operation_type,target_type,target_id,actor_id,actor_type,trigger_type,before_summary,after_summary,status,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?)`, audit.id, instanceID, audit.operationType, "configuration", audit.id, audit.actorID, audit.actorType, audit.triggerType, "{}", "{}", "succeeded", now.Add(time.Second), now.Add(time.Second)); err != nil {
			t.Fatalf("insert historical automatic audit %s: %v", audit.id, err)
		}
	}

	options, err := store.QueryOperationAudits(storage.OperationAuditQuery{InstanceID: instanceID, ActorOptions: true, Actor: "es", Limit: 1, Offset: 99})
	if err != nil || len(options.Actors) != 1 || options.Actors[0] != "test" {
		t.Fatalf("actor candidates=%v err=%v", options.Actors, err)
	}
	literal, err := store.QueryOperationAudits(storage.OperationAuditQuery{InstanceID: instanceID, ActorOptions: true, Actor: "%"})
	if err != nil || len(literal.Actors) != 0 {
		t.Fatalf("actor wildcard must be literal: %v %v", literal.Actors, err)
	}
	for _, query := range []storage.OperationAuditQuery{
		{InstanceID: instanceID, Actor: "tes", ActorExact: true},
		{InstanceID: instanceID, Search: "%"},
	} {
		result, err := store.QueryOperationAudits(query)
		if err != nil || result.Total != 0 {
			t.Fatalf("unexpected filter match: %+v %v", result, err)
		}
	}
	page, err := store.QueryOperationAudits(storage.OperationAuditQuery{
		InstanceID: instanceID,
		From:       now,
		To:         now.Add(2 * time.Second),
		Limit:      1,
	})
	if err != nil {
		t.Fatal(err)
	}
	if page.Total != 2 || len(page.Items) != 1 || page.Items[0].TargetID != "middle" {
		t.Fatalf("time range should include from and exclude to while preserving pagination: %+v", page)
	}
	operationTypes := make(map[string]bool, len(page.OperationTypes))
	for _, operationType := range page.OperationTypes {
		operationTypes[operationType] = true
	}
	for _, operationType := range []string{"settings.update", "billing.price_update", "http.system.*", "http.billing.*", "http.channel_group_presets.*"} {
		if !operationTypes[operationType] {
			t.Fatalf("fixed operation type list is missing %q: %v", operationType, page.OperationTypes)
		}
	}
	if operationTypes["http.system.settings.put"] {
		t.Fatalf("catalog should expose module filters, not data-dependent route types: %v", page.OperationTypes)
	}
	if len(page.OperationTypes) != len(storage.OperationAuditFilterTypes()) {
		t.Fatalf("catalog size changed with inserted rows: got %d, want %d", len(page.OperationTypes), len(storage.OperationAuditFilterTypes()))
	}

	modulePage, err := store.QueryOperationAudits(storage.OperationAuditQuery{
		InstanceID:    instanceID,
		OperationType: "http.system.*",
		Limit:         20,
	})
	if err != nil {
		t.Fatal(err)
	}
	if modulePage.Total != 1 || len(modulePage.Items) != 1 || modulePage.Items[0].OperationType != "http.system.settings.put" {
		t.Fatalf("module filter should match historic route-specific type: %+v", modulePage)
	}

	underscoreModulePage, err := store.QueryOperationAudits(storage.OperationAuditQuery{
		InstanceID:    instanceID,
		OperationType: "http.channel_group_presets.*",
		Limit:         20,
	})
	if err != nil {
		t.Fatal(err)
	}
	if underscoreModulePage.Total != 1 || len(underscoreModulePage.Items) != 1 {
		t.Fatalf("module filters should escape underscores literally: %+v", underscoreModulePage)
	}
	verified := storage.OperationAudit{ID: "verified-" + instanceID, InstanceID: instanceID, OperationType: "settings.update", ActorID: "system", ActorType: "human", ActorRole: "admin", AuthMethod: "session", Status: "succeeded", CreatedAt: now, UpdatedAt: now}
	if err := store.InsertOperationAudit(verified); err != nil {
		t.Fatal(err)
	}
	verifiedPage, err := store.QueryOperationAudits(storage.OperationAuditQuery{InstanceID: instanceID, Actor: "system", ActorExact: true})
	if err != nil || verifiedPage.Total != 1 || verifiedPage.Items[0].ID != verified.ID {
		t.Fatalf("verified human hidden by username: %+v %v", verifiedPage, err)
	}
}
