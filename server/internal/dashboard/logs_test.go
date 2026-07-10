package dashboard

import (
	"testing"
	"time"

	"controltower/server/internal/storage"
)

func TestFilterLogsByInstanceModelAndType(t *testing.T) {
	base := time.Date(2026, 7, 2, 13, 0, 0, 0, time.UTC)
	logs := FilterLogs([]storage.LogEvent{
		{
			InstanceID:   "inst-1",
			SourceLogID: 1,
			CreatedAt:   base,
			LogType:     "consume",
			ModelName:   "gpt-4o",
		},
		{
			InstanceID:   "inst-1",
			SourceLogID: 2,
			CreatedAt:   base.Add(time.Minute),
			LogType:     "error",
			ModelName:   "gpt-4o",
		},
		{
			InstanceID:   "inst-2",
			SourceLogID: 3,
			CreatedAt:   base.Add(2 * time.Minute),
			LogType:     "error",
			ModelName:   "claude-sonnet-4",
		},
	}, LogFilter{
		InstanceID: "inst-1",
		ModelName:  "gpt-4o",
		LogType:    "error",
		Limit:      10,
	})

	if len(logs) != 1 {
		t.Fatalf("expected one log, got %#v", logs)
	}
	if logs[0].SourceLogID != 2 {
		t.Fatalf("unexpected log: %#v", logs[0])
	}
}

func TestFilterLogsSortsNewestFirstAndPaginates(t *testing.T) {
	base := time.Date(2026, 7, 2, 13, 10, 0, 0, time.UTC)
	logs := FilterLogs([]storage.LogEvent{
		{InstanceID: "inst-1", SourceLogID: 1, CreatedAt: base},
		{InstanceID: "inst-1", SourceLogID: 2, CreatedAt: base.Add(time.Minute)},
		{InstanceID: "inst-1", SourceLogID: 3, CreatedAt: base.Add(2 * time.Minute)},
	}, LogFilter{
		Limit:  1,
		Offset: 1,
	})

	if len(logs) != 1 {
		t.Fatalf("expected one paginated log, got %#v", logs)
	}
	if logs[0].SourceLogID != 2 {
		t.Fatalf("expected second newest log, got %#v", logs[0])
	}
}

func TestFilterLogsCapsLimit(t *testing.T) {
	base := time.Date(2026, 7, 2, 13, 20, 0, 0, time.UTC)
	var events []storage.LogEvent
	for i := int64(1); i <= 250; i++ {
		events = append(events, storage.LogEvent{
			InstanceID:   "inst-1",
			SourceLogID: i,
			CreatedAt:   base.Add(time.Duration(i) * time.Second),
		})
	}

	logs := FilterLogs(events, LogFilter{Limit: 1000})
	if len(logs) != 200 {
		t.Fatalf("limit should be capped at 200, got %d", len(logs))
	}
}

