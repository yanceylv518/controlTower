package logcollector

import (
	"strings"
	"testing"
	"time"
)

func TestCollectLogsSQLUsesReadOnlyIncrementalQuery(t *testing.T) {
	sqlText := collectLogsSQL()
	for _, fragment := range []string{
		"SELECT id, COALESCE(created_at, 0), type, COALESCE(content, '')",
		"FROM logs",
		"WHERE id > ? AND type IN (2, 5)",
		"ORDER BY id ASC",
		"LIMIT ?",
		"`group`",
		"COALESCE(user_id, 0)",
		"COALESCE(username, '')",
		"COALESCE(channel_id, 0)",
		"COALESCE(other, '')",
	} {
		if !strings.Contains(sqlText, fragment) {
			t.Fatalf("collector SQL missing %q: %s", fragment, sqlText)
		}
	}
	for _, forbidden := range []string{"INSERT", "UPDATE", "DELETE", "content LIKE"} {
		if strings.Contains(strings.ToUpper(sqlText), forbidden) {
			t.Fatalf("collector SQL contains forbidden fragment %q: %s", forbidden, sqlText)
		}
	}
}

type fakeLogRowScanner struct {
	createdAtUnix int64
}

func (f fakeLogRowScanner) Scan(dest ...any) error {
	*dest[0].(*int64) = 42          // id
	*dest[1].(*int64) = f.createdAtUnix // created_at
	*dest[2].(*int) = 5             // type
	*dest[13].(*int64) = 3          // use_time
	return nil
}

func TestScanLogRowReplacesEpochCreatedAtWithCollectionTime(t *testing.T) {
	before := time.Now().UTC().Add(-time.Minute)
	row, err := scanLogRow(fakeLogRowScanner{createdAtUnix: 0})
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if row.CreatedAt.Before(before) {
		t.Fatalf("expected NULL created_at to become collection time, got %v", row.CreatedAt)
	}
}

func TestScanLogRowKeepsValidCreatedAt(t *testing.T) {
	row, err := scanLogRow(fakeLogRowScanner{createdAtUnix: 1783934163})
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if row.CreatedAt.Unix() != 1783934163 {
		t.Fatalf("expected original timestamp preserved, got %v", row.CreatedAt)
	}
}
