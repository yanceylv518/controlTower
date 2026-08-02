package mysqlstore

import (
	"context"
	"fmt"
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
	if err := ApplyDir(ctx, db, "../../migrations"); err != nil {
		t.Fatalf("apply migrations: %v", err)
	}

	store := New(db)
	sourceLogID := time.Now().UnixNano()
	event := storage.LogEvent{
		InstanceID:   "integration-inst",
		SourceLogID:  sourceLogID,
		CreatedAt:    time.Now().UTC(),
		LogType:      "consume",
		UserID:       7,
		Username:     "integration-user",
		ChannelID:    18,
		ModelName:    "gpt-4o",
		TotalTokens:  10,
		Quota:        20,
		RequestID:    fmt.Sprintf("integration-request-%d", sourceLogID),
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

	snapshot := storage.ChannelSnapshot{
		ID:          "integration-channel-current",
		InstanceID:  "integration-inst",
		ChannelID:   18,
		ChannelName: "integration-channel",
		Status:      "enabled",
		Weight:      10,
		ModelsText:  "gpt-4o",
		CapturedAt:  time.Now().UTC(),
	}
	if err := store.InsertChannelSnapshot(snapshot); err != nil {
		t.Fatalf("upsert channel current: %v", err)
	}
	channels, err := store.QueryChannelSnapshots(storage.ChannelSnapshotQuery{InstanceID: snapshot.InstanceID, LatestOnly: true, Limit: 10})
	if err != nil {
		t.Fatalf("query channel current: %v", err)
	}
	if len(channels) != 1 || channels[0].ChannelID != snapshot.ChannelID || channels[0].ChannelName != snapshot.ChannelName {
		t.Fatalf("unexpected channel current: %#v", channels)
	}

	if _, err := db.Exec(`INSERT INTO channel_snapshots
  (id, instance_id, channel_id, channel_name, status, weight, models_text, group_name, priority, captured_at)
VALUES
  ('integration-history-migrated', 'integration-inst', 18, 'old', 'enabled', 1, '', NULL, NULL, NOW() - INTERVAL 1 DAY),
  ('integration-history-protected', 'integration-inst', 19, 'unmigrated', 'enabled', 1, '', NULL, NULL, NOW() - INTERVAL 1 DAY)
ON DUPLICATE KEY UPDATE captured_at=VALUES(captured_at)`); err != nil {
		t.Fatalf("seed channel history cleanup: %v", err)
	}
	if _, err := store.DeleteChannelSnapshotHistoryBatch(5000); err != nil {
		t.Fatalf("delete migrated channel history batch: %v", err)
	}
	var migratedRows, protectedRows int
	if err := db.QueryRow(`SELECT COUNT(*) FROM channel_snapshots WHERE id='integration-history-migrated'`).Scan(&migratedRows); err != nil {
		t.Fatalf("count migrated history: %v", err)
	}
	if err := db.QueryRow(`SELECT COUNT(*) FROM channel_snapshots WHERE id='integration-history-protected'`).Scan(&protectedRows); err != nil {
		t.Fatalf("count protected history: %v", err)
	}
	if migratedRows != 0 || protectedRows != 1 {
		t.Fatalf("history cleanup migrated=%d protected=%d, want 0/1", migratedRows, protectedRows)
	}
	_, _ = db.Exec(`DELETE FROM channel_snapshots WHERE id='integration-history-protected'`)
}
