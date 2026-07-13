package storage

import (
	"os"
	"strings"
	"testing"
)

func TestChannelCommandMigration(t *testing.T) {
	data, e := os.ReadFile("../../migrations/005_channel_commands.sql")
	if e != nil {
		t.Fatal(e)
	}
	sql := strings.ToLower(string(data))
	for _, fragment := range []string{"create table if not exists channel_commands", "payload_json text", "idx_channel_commands_instance"} {
		if !strings.Contains(sql, fragment) {
			t.Fatalf("missing %s", fragment)
		}
	}
}
