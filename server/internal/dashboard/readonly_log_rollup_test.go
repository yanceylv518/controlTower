package dashboard

import (
	"context"
	"os"
	"runtime"
	"strings"
	"testing"
	"time"

	"controltower/server/internal/storage"
)

func TestAggregateReadonlyLogsGroupsDimensionsByUTC(t *testing.T) {
	items := []readonlySourceLog{
		{ID: 1, CreatedAt: time.Date(2026, 8, 5, 12, 1, 0, 0, time.UTC).Unix(), LogType: 2, UserID: 9, Username: "alice", ChannelID: 7, ModelName: "glm-5.2", TokenName: "prod", GroupName: "vip", PromptTokens: 10, CompletionTokens: 3, Quota: 4},
		{ID: 2, CreatedAt: time.Date(2026, 8, 5, 12, 59, 0, 0, time.UTC).Unix(), LogType: 2, UserID: 9, Username: "alice", ChannelID: 7, ModelName: "glm-5.2", TokenName: "prod", GroupName: "vip", PromptTokens: 20, CompletionTokens: 6, Quota: 8},
		{ID: 3, CreatedAt: time.Date(2026, 8, 5, 13, 0, 0, 0, time.UTC).Unix(), LogType: 5, UserID: 9, Username: "alice", ChannelID: 7, ModelName: "glm-5.2"},
	}
	values := aggregateReadonlyLogs("cn", items)
	if len(values) != 2 {
		t.Fatalf("rollup groups = %d, want 2", len(values))
	}
	for _, value := range values {
		if value.LogType == 2 {
			if value.RequestCount != 2 || value.PromptTokens != 30 || value.CompletionTokens != 9 || value.QuotaSum != 12 {
				t.Fatalf("consume rollup = %+v", value)
			}
			if value.Username != "alice" {
				t.Fatalf("username = %q", value.Username)
			}
		}
	}
}

func TestCompleteHourWindow(t *testing.T) {
	start := time.Date(2026, 8, 5, 0, 8, 0, 0, time.UTC)
	end := time.Date(2026, 8, 5, 12, 8, 0, 0, time.UTC)
	from, to, ok := completeHourWindow(start, end)
	if !ok || !from.Equal(time.Date(2026, 8, 5, 1, 0, 0, 0, time.UTC)) || !to.Equal(time.Date(2026, 8, 5, 12, 0, 0, 0, time.UTC)) {
		t.Fatalf("window = %s..%s ok=%v", from, to, ok)
	}
}

type fakeRollupSource struct {
	logs     []readonlySourceLog
	lastSeen int64
}

// Mirrors the real query's semantics — ORDER BY created_at DESC, id DESC
// LIMIT 1 — i.e. the id of the most recently created qualifying row, NOT the
// maximum qualifying id. The two differ when id order and created_at order
// invert; the inversion test below depends on this distinction.
func (f *fakeRollupSource) readonlyLogLastIDBefore(_ context.Context, _ string, cutoff time.Time) (int64, error) {
	var id, createdAt int64
	for _, item := range f.logs {
		if item.CreatedAt >= cutoff.Unix() {
			continue
		}
		if item.CreatedAt > createdAt || (item.CreatedAt == createdAt && item.ID > id) {
			id, createdAt = item.ID, item.CreatedAt
		}
	}
	return id, nil
}

func (f *fakeRollupSource) readonlyLogBatch(_ context.Context, _ string, afterID, maxID int64, limit int) ([]readonlySourceLog, error) {
	items := []readonlySourceLog{}
	for _, item := range f.logs {
		if item.ID > afterID && item.ID <= maxID && len(items) < limit {
			items = append(items, item)
			if item.ID > f.lastSeen {
				f.lastSeen = item.ID
			}
		}
	}
	return items, nil
}

type fakeRollupStore struct {
	cursor   storage.ReadonlyLogRollupCursor
	applied  []storage.ReadonlyLogRollup
	caughtUp bool
}

func (f *fakeRollupStore) ListInstances() ([]storage.Instance, error) { return nil, nil }
func (f *fakeRollupStore) ReadonlyLogRollupCursor(context.Context, string) (storage.ReadonlyLogRollupCursor, error) {
	return f.cursor, nil
}
func (f *fakeRollupStore) InitializeReadonlyLogRollupCursor(_ context.Context, site string, lastLogID int64, coverageFrom, _ time.Time) error {
	f.cursor = storage.ReadonlyLogRollupCursor{SiteID: site, LastLogID: lastLogID, Initialized: true, CoverageFrom: &coverageFrom}
	return nil
}
func (f *fakeRollupStore) ApplyReadonlyLogRollups(_ context.Context, _ string, lastLogID int64, values []storage.ReadonlyLogRollup, _ time.Time) error {
	f.applied = append(f.applied, values...)
	f.cursor.LastLogID = lastLogID
	return nil
}
func (f *fakeRollupStore) MarkReadonlyLogRollupCaughtUp(context.Context, string, time.Time) error {
	f.caughtUp = true
	return nil
}
func (f *fakeRollupStore) RecordReadonlyLogRollupError(context.Context, string, string, time.Time) error {
	return nil
}
func (f *fakeRollupStore) QueryReadonlyLogRollup(context.Context, storage.ReadonlyLogRollupFilter) (storage.ReadonlyLogRollupSummary, error) {
	return storage.ReadonlyLogRollupSummary{}, nil
}

// The cursor must never advance past the settled head: rows younger than the
// safety lag stay unread until a later pass, so a lower id whose insert
// commits late cannot be skipped forever.
func TestSyncSiteStopsAtSettledHead(t *testing.T) {
	now := time.Now().Unix()
	source := &fakeRollupSource{logs: []readonlySourceLog{
		{ID: 1, CreatedAt: now - 3600, LogType: 2, UserID: 1, Quota: 5},
		{ID: 2, CreatedAt: now - 3600, LogType: 2, UserID: 1, Quota: 7},
		{ID: 3, CreatedAt: now - 2, LogType: 2, UserID: 1, Quota: 11},
	}}
	store := &fakeRollupStore{}
	runner := ReadonlyLogRollupRunner{Source: source, Store: store}
	if err := runner.syncSite(context.Background(), "cn"); err != nil {
		t.Fatalf("first pass: %v", err)
	}
	if store.cursor.LastLogID != 2 || source.lastSeen != 2 {
		t.Fatalf("cursor should stop at settled head 2: cursor=%d seen=%d", store.cursor.LastLogID, source.lastSeen)
	}
	var quota int64
	for _, value := range store.applied {
		quota += value.QuotaSum
	}
	if quota != 12 {
		t.Fatalf("applied quota = %d, want 12", quota)
	}
	// Second pass with the young row now older than the lag: it must be
	// picked up rather than skipped.
	source.logs[2].CreatedAt = now - 3600
	if err := runner.syncSite(context.Background(), "cn"); err != nil {
		t.Fatalf("second pass: %v", err)
	}
	if store.cursor.LastLogID != 3 || !store.caughtUp {
		t.Fatalf("late row not synced: cursor=%d caughtUp=%v", store.cursor.LastLogID, store.caughtUp)
	}
}

// An id whose created_at is older than a later-inserted lower id (id order
// and time order inverted) sits above the settled head temporarily. It must
// be picked up once a newer settled row raises the head — delayed is fine,
// lost is not.
func TestSyncSiteToleratesIDOrderInversion(t *testing.T) {
	now := time.Now().Unix()
	source := &fakeRollupSource{logs: []readonlySourceLog{
		{ID: 1, CreatedAt: now - 500, LogType: 2, UserID: 1, Quota: 1},
		{ID: 3, CreatedAt: now - 400, LogType: 2, UserID: 1, Quota: 2},
		{ID: 2, CreatedAt: now - 100, LogType: 2, UserID: 1, Quota: 4},
	}}
	store := &fakeRollupStore{}
	runner := ReadonlyLogRollupRunner{Source: source, Store: store}
	if err := runner.syncSite(context.Background(), "cn"); err != nil {
		t.Fatalf("first pass: %v", err)
	}
	// Settled head is id 2 (latest created_at), so id 3 stays unread for now.
	if store.cursor.LastLogID != 2 {
		t.Fatalf("cursor = %d, want settled head 2", store.cursor.LastLogID)
	}
	source.logs = append(source.logs, readonlySourceLog{ID: 4, CreatedAt: now - 50, LogType: 2, UserID: 1, Quota: 8})
	if err := runner.syncSite(context.Background(), "cn"); err != nil {
		t.Fatalf("second pass: %v", err)
	}
	if store.cursor.LastLogID != 4 {
		t.Fatalf("cursor = %d, want 4", store.cursor.LastLogID)
	}
	var quota int64
	for _, value := range store.applied {
		quota += value.QuotaSum
	}
	if quota != 15 {
		t.Fatalf("applied quota = %d, want 15 (row 3 must not be lost)", quota)
	}
}

// Source-text SQL contract, same pattern as mysqlstore/*_contract_test.go:
// the fakes above model these queries, so their ordering and bounds must not
// drift silently.
func TestReadonlyLogRollupSQLContract(t *testing.T) {
	_, file, _, _ := runtime.Caller(0)
	data, err := os.ReadFile(strings.TrimSuffix(file, "_test.go") + ".go")
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	for _, required := range []string{
		// settled head: id of the most recently created row before cutoff
		"WHERE created_at<? ORDER BY created_at DESC,id DESC LIMIT 1",
		// batch: strictly cursor-bounded on both sides, scanned in id order
		"WHERE id>? AND id<=? ORDER BY id LIMIT ?",
	} {
		if !strings.Contains(text, required) {
			t.Fatalf("missing SQL contract %q", required)
		}
	}
}
