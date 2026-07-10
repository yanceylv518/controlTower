package dockercollector

import (
	"strings"
	"testing"
	"time"
)

func TestParseStatusesParsesRunningAndExitedContainers(t *testing.T) {
	collectedAt := time.Date(2026, 7, 7, 12, 0, 0, 0, time.UTC)
	output := "new-api\tUp 3 hours\trunning\nmysql\tExited (0) 2 hours ago\texited\n"
	statuses := ParseStatuses(output, collectedAt)
	if len(statuses) != 2 {
		t.Fatalf("statuses len = %d", len(statuses))
	}
	if statuses[0].ContainerName != "new-api" || !statuses[0].Running || statuses[0].CollectedAt != collectedAt {
		t.Fatalf("unexpected first status: %#v", statuses[0])
	}
	if statuses[1].ContainerName != "mysql" || statuses[1].Running {
		t.Fatalf("unexpected second status: %#v", statuses[1])
	}
}

func TestParseStatusesSkipsInvalidLines(t *testing.T) {
	statuses := ParseStatuses("\ninvalid\nvalid\tUp 1 second\trunning\n", time.Now())
	if len(statuses) != 1 || statuses[0].ContainerName != "valid" {
		t.Fatalf("unexpected statuses: %#v", statuses)
	}
}

func TestSanitizeStatusTrimsLengthAndNewlines(t *testing.T) {
	status := sanitizeStatus(strings.Repeat("x", 250) + "\n")
	if len(status) != 200 {
		t.Fatalf("status len = %d", len(status))
	}
	if strings.ContainsAny(status, "\r\n") {
		t.Fatalf("status contains newline: %q", status)
	}
}
