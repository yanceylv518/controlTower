package dashboard

import (
	"context"
	"os"
	"runtime"
	"sort"
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
	head     int64
}

// The initial cursor starts immediately before the minimum id in the
// coverage window; the captured head independently uses the greatest visible
// source id.
func (f *fakeRollupSource) readonlyLogInitialCursor(_ context.Context, _ string, cutoff time.Time) (int64, error) {
	var minimum, maximum int64
	for _, item := range f.logs {
		if item.ID > maximum {
			maximum = item.ID
		}
		if item.CreatedAt >= cutoff.Unix() && (minimum == 0 || item.ID < minimum) {
			minimum = item.ID
		}
	}
	if minimum == 0 {
		return maximum, nil
	}
	return minimum - 1, nil
}

func (f *fakeRollupSource) readonlyLogHead(context.Context, string) (int64, error) {
	if f.head > 0 {
		return f.head, nil
	}
	var maximum int64
	for _, item := range f.logs {
		if item.ID > maximum {
			maximum = item.ID
		}
	}
	return maximum, nil
}

func (f *fakeRollupSource) readonlyLogBatch(_ context.Context, _ string, afterID, maxID int64, limit int) ([]readonlySourceLog, error) {
	items := []readonlySourceLog{}
	for _, item := range f.logs {
		if item.ID > afterID && item.ID <= maxID {
			items = append(items, item)
		}
	}
	sort.Slice(items, func(i, j int) bool { return items[i].ID < items[j].ID })
	if len(items) > limit {
		items = items[:limit]
	}
	for _, item := range items {
		if item.ID > f.lastSeen {
			f.lastSeen = item.ID
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
	source := &fakeRollupSource{head: 2, logs: []readonlySourceLog{
		{ID: 1, CreatedAt: now - 3600, LogType: 2, UserID: 1, Quota: 5},
		{ID: 2, CreatedAt: now - 3600, LogType: 2, UserID: 1, Quota: 7},
		{ID: 3, CreatedAt: now - 2, LogType: 2, UserID: 1, Quota: 11},
	}}
	store := &fakeRollupStore{}
	runner := ReadonlyLogRollupRunner{Source: source, Store: store, SettleDelay: -1}
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
	// The next pass captures the new source head and must pick up the row.
	source.logs[2].CreatedAt = now - 3600
	source.head = 3
	if err := runner.syncSite(context.Background(), "cn"); err != nil {
		t.Fatalf("second pass: %v", err)
	}
	if store.cursor.LastLogID != 3 || !store.caughtUp {
		t.Fatalf("late row not synced: cursor=%d caughtUp=%v", store.cursor.LastLogID, store.caughtUp)
	}
}

// An id whose created_at is older than a lower id must be consumed even if
// the site becomes quiet and no future row arrives to raise the water mark.
func TestSyncSiteToleratesIDOrderInversionWithoutFutureLogs(t *testing.T) {
	now := time.Now().Unix()
	source := &fakeRollupSource{logs: []readonlySourceLog{
		{ID: 1, CreatedAt: now - 500, LogType: 2, UserID: 1, Quota: 1},
		{ID: 3, CreatedAt: now - 400, LogType: 2, UserID: 1, Quota: 2},
		{ID: 2, CreatedAt: now - 100, LogType: 2, UserID: 1, Quota: 4},
	}}
	store := &fakeRollupStore{}
	runner := ReadonlyLogRollupRunner{Source: source, Store: store, SettleDelay: -1}
	if err := runner.syncSite(context.Background(), "cn"); err != nil {
		t.Fatalf("first pass: %v", err)
	}
	if store.cursor.LastLogID != 3 || !store.caughtUp {
		t.Fatalf("cursor = %d caughtUp=%v, want settled head 3", store.cursor.LastLogID, store.caughtUp)
	}
	var quota int64
	for _, value := range store.applied {
		quota += value.QuotaSum
	}
	if quota != 7 {
		t.Fatalf("applied quota = %d, want 7 (row 3 must not be stranded)", quota)
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
		// initial cursor: immediately before the minimum id in coverage
		"GREATEST(MIN(id)-1,0) FROM logs WHERE created_at>=?",
		// captured source head is independent of created_at ordering
		"SELECT COALESCE(MAX(id),0) FROM logs",
		// batch: strictly cursor-bounded on both sides, scanned in id order
		"WHERE id>? AND id<=? ORDER BY id LIMIT ?",
	} {
		if !strings.Contains(text, required) {
			t.Fatalf("missing SQL contract %q", required)
		}
	}
}
