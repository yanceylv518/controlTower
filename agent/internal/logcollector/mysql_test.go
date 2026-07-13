package logcollector

import (
	"strings"
	"testing"
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
