package localbuffer

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"controltower/agent/internal/reporter"
)

func TestFileStoreAppendLoadAndDropFirst(t *testing.T) {
	store := NewFileStore(filepath.Join(t.TempDir(), "buffer.json"))
	now := time.Date(2026, 7, 7, 12, 0, 0, 0, time.UTC)

	first := Entry{CreatedAt: now, LastLogID: 10, Report: reportWithLog(10)}
	second := Entry{CreatedAt: now.Add(time.Second), LastLogID: 11, Report: reportWithLog(11)}
	if err := store.Append(first, 10); err != nil {
		t.Fatalf("append first: %v", err)
	}
	if err := store.Append(second, 10); err != nil {
		t.Fatalf("append second: %v", err)
	}

	entries, err := store.Load()
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if len(entries) != 2 || entries[0].LastLogID != 10 || entries[1].LastLogID != 11 {
		t.Fatalf("unexpected entries: %#v", entries)
	}

	dropped, ok, err := store.DropFirst()
	if err != nil {
		t.Fatalf("drop first: %v", err)
	}
	if !ok || dropped.LastLogID != 10 {
		t.Fatalf("unexpected dropped entry: %#v ok=%v", dropped, ok)
	}
	entries, err = store.Load()
	if err != nil {
		t.Fatalf("load after drop: %v", err)
	}
	if len(entries) != 1 || entries[0].LastLogID != 11 {
		t.Fatalf("unexpected remaining entries: %#v", entries)
	}
}

func TestLoadAcceptsUTF8BOM(t *testing.T) {
	path := filepath.Join(t.TempDir(), "buffer.json")
	data := append([]byte{0xEF, 0xBB, 0xBF}, []byte(`[{"last_log_id":42,"report":{"log_events":[{"source_log_id":42}]}}]`)...)
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatalf("write buffer: %v", err)
	}
	entries, err := NewFileStore(path).Load()
	if err != nil {
		t.Fatalf("load buffer: %v", err)
	}
	if len(entries) != 1 || entries[0].LastLogID != 42 {
		t.Fatalf("unexpected entries: %#v", entries)
	}
}

func TestAppendRejectsWhenBufferWouldExceedLimit(t *testing.T) {
	store := NewFileStore(filepath.Join(t.TempDir(), "buffer.json"))
	if err := store.Append(reportEntryWithLogs(2), 2); err != nil {
		t.Fatalf("append first: %v", err)
	}
	err := store.Append(reportEntryWithLogs(1), 2)
	if !errors.Is(err, ErrBufferFull) {
		t.Fatalf("expected ErrBufferFull, got %v", err)
	}
}

func TestAppendCountsAggregatedMetricsTowardLimit(t *testing.T) {
	store := NewFileStore(filepath.Join(t.TempDir(), "buffer.json"))
	entry := Entry{CreatedAt: time.Now().UTC(), LastLogID: 10, Report: reporter.AgentReportRequest{AggregatedMetrics: []reporter.AggregatedMetricPayload{{DimensionType: "instance"}, {DimensionType: "instance_model"}}}}
	if err := store.Append(entry, 2); err != nil {
		t.Fatalf("append first: %v", err)
	}
	err := store.Append(Entry{CreatedAt: time.Now().UTC(), LastLogID: 11, Report: reporter.AgentReportRequest{AggregatedMetrics: []reporter.AggregatedMetricPayload{{DimensionType: "instance"}}}}, 2)
	if !errors.Is(err, ErrBufferFull) {
		t.Fatalf("expected ErrBufferFull, got %v", err)
	}
}
func reportEntryWithLogs(count int) Entry {
	logs := make([]reporter.LogEventPayload, 0, count)
	for i := 0; i < count; i++ {
		logs = append(logs, reporter.LogEventPayload{SourceLogID: int64(i + 1)})
	}
	return Entry{CreatedAt: time.Now().UTC(), LastLogID: int64(count), Report: reporter.AgentReportRequest{LogEvents: logs}}
}

func reportWithLog(id int64) reporter.AgentReportRequest {
	return reporter.AgentReportRequest{LogEvents: []reporter.LogEventPayload{{SourceLogID: id}}}
}
