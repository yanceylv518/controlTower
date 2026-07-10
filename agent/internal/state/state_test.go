package state

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestStoreRoundTrip(t *testing.T) {
	dir := t.TempDir()
	store := NewFileStore(filepath.Join(dir, "state.json"))
	now := time.Date(2026, 7, 2, 12, 0, 0, 0, time.UTC)

	original := State{
		LastLogID:                 123,
		LastSuccessReportAt:       now,
		ConsecutiveReportFailures: 2,
	}
	if err := store.Save(original); err != nil {
		t.Fatalf("save state: %v", err)
	}

	loaded, err := store.Load()
	if err != nil {
		t.Fatalf("load state: %v", err)
	}
	if loaded.LastLogID != original.LastLogID {
		t.Fatalf("unexpected last log id: %d", loaded.LastLogID)
	}
	if !loaded.LastSuccessReportAt.Equal(now) {
		t.Fatalf("unexpected report time: %s", loaded.LastSuccessReportAt)
	}
	if loaded.ConsecutiveReportFailures != 2 {
		t.Fatalf("unexpected failures: %d", loaded.ConsecutiveReportFailures)
	}
}

func TestLoadMissingFileReturnsZeroState(t *testing.T) {
	store := NewFileStore(filepath.Join(t.TempDir(), "missing.json"))
	loaded, err := store.Load()
	if err != nil {
		t.Fatalf("load missing state: %v", err)
	}
	if loaded.LastLogID != 0 {
		t.Fatalf("expected zero last log id, got %d", loaded.LastLogID)
	}
}

func TestLoadAcceptsUTF8BOM(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	data := append([]byte{0xEF, 0xBB, 0xBF}, []byte(`{"last_log_id":456}`)...)
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatalf("write state: %v", err)
	}
	loaded, err := NewFileStore(path).Load()
	if err != nil {
		t.Fatalf("load state: %v", err)
	}
	if loaded.LastLogID != 456 {
		t.Fatalf("unexpected last log id: %d", loaded.LastLogID)
	}
}
