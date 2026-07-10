package mysqlstore

import (
	"context"
	"os"
	"testing"
	"time"

	"controltower/server/internal/storage"
)

func TestMySQLStoreIntegration(t *testing.T) {
	dsn := os.Getenv("CT_MYSQL_TEST_DSN")
	if dsn == "" {
		t.Skip("set CT_MYSQL_TEST_DSN to run MySQL integration test")
	}
	db, err := Open(dsn)
	if err != nil {
		t.Fatalf("open mysql: %v", err)
	}
	defer db.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	migration, err := os.ReadFile("../../migrations/001_init.sql")
	if err != nil {
		t.Fatalf("read migration: %v", err)
	}
	if err := ApplySQL(ctx, db, string(migration)); err != nil {
		t.Fatalf("apply migration: %v", err)
	}

	store := New(db)
	event := storage.LogEvent{
		InstanceID:   "integration-inst",
		SourceLogID:  time.Now().UnixNano(),
		CreatedAt:    time.Now().UTC(),
		LogType:      "consume",
		UserID:       7,
		Username:     "integration-user",
		ChannelID:    18,
		ModelName:    "gpt-4o",
		TotalTokens:  10,
		Quota:        20,
		RequestID:    "integration-request",
		ErrorSummary: "",
	}
	inserted, err := store.InsertLogEvent(event)
	if err != nil {
		t.Fatalf("insert log event: %v", err)
	}
	if !inserted {
		t.Fatal("first insert should affect one row")
	}
	inserted, err = store.InsertLogEvent(event)
	if err != nil {
		t.Fatalf("insert duplicate log event: %v", err)
	}
	if inserted {
		t.Fatal("duplicate insert should be ignored")
	}

	logs, err := store.QueryLogEvents(storage.LogQuery{InstanceID: event.InstanceID, RequestID: event.RequestID, Limit: 10})
	if err != nil {
		t.Fatalf("query log events: %v", err)
	}
	if len(logs) != 1 || logs[0].SourceLogID != event.SourceLogID {
		t.Fatalf("unexpected logs: %#v", logs)
	}
}
