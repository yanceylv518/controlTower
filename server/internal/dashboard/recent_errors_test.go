package dashboard

import (
	"strings"
	"testing"
	"time"

	"controltower/server/internal/storage"
)

func recentErrorEvent(id int64, logType string, channelID int64, userID int64, username string) storage.LogEvent {
	return storage.LogEvent{
		InstanceID:  "inst-a",
		SourceLogID: id,
		CreatedAt:   time.Date(2026, 7, 10, 12, 0, 0, 0, time.UTC).Add(time.Duration(id) * time.Second),
		LogType:     logType,
		ChannelID:   channelID,
		UserID:      userID,
		Username:    username,
	}
}

func newestFirst(events []storage.LogEvent) []storage.LogEvent {
	reversed := make([]storage.LogEvent, 0, len(events))
	for i := len(events) - 1; i >= 0; i-- {
		reversed = append(reversed, events[i])
	}
	return reversed
}

func findAlert(items []AlertItem, ruleKey string, title string) (AlertItem, bool) {
	for _, item := range items {
		if item.RuleKey == ruleKey && item.Title == title {
			return item, true
		}
	}
	return AlertItem{}, false
}

func TestRecentErrorAlertFiresForChannel(t *testing.T) {
	var events []storage.LogEvent
	for id := int64(1); id <= 7; id++ {
		events = append(events, recentErrorEvent(id, "consume", 18, 0, ""))
	}
	for id := int64(8); id <= 10; id++ {
		events = append(events, recentErrorEvent(id, "error", 18, 0, ""))
	}

	items := appendRecentErrorAlerts(nil, newestFirst(events))
	alert, ok := findAlert(items, "recent_errors", "渠道错误激增")
	if !ok {
		t.Fatalf("expected channel alert, got %+v", items)
	}
	if alert.Severity != "warning" {
		t.Fatalf("expected warning severity, got %s", alert.Severity)
	}
	if !strings.Contains(alert.Summary, "渠道 18") || !strings.Contains(alert.Summary, "10 条请求中 3 条失败") {
		t.Fatalf("unexpected summary: %s", alert.Summary)
	}
}

func TestRecentErrorAlertIgnoresErrorsOutsideWindow(t *testing.T) {
	var events []storage.LogEvent
	for id := int64(1); id <= 3; id++ {
		events = append(events, recentErrorEvent(id, "error", 18, 0, ""))
	}
	for id := int64(4); id <= 13; id++ {
		events = append(events, recentErrorEvent(id, "consume", 18, 0, ""))
	}

	items := appendRecentErrorAlerts(nil, newestFirst(events))
	if len(items) != 0 {
		t.Fatalf("expected no alerts when errors fall outside the last %d events, got %+v", recentErrorWindow, items)
	}
}

func TestRecentErrorAlertFiresForUserWithCriticalSeverity(t *testing.T) {
	var events []storage.LogEvent
	for id := int64(1); id <= 5; id++ {
		events = append(events, recentErrorEvent(id, "consume", 0, 7, "alice"))
	}
	for id := int64(6); id <= 10; id++ {
		events = append(events, recentErrorEvent(id, "error", 0, 7, "alice"))
	}

	items := appendRecentErrorAlerts(nil, newestFirst(events))
	alert, ok := findAlert(items, "recent_errors", "客户错误激增")
	if !ok {
		t.Fatalf("expected user alert, got %+v", items)
	}
	if alert.Severity != "critical" {
		t.Fatalf("expected critical severity, got %s", alert.Severity)
	}
	if !strings.Contains(alert.Summary, "alice(7)") {
		t.Fatalf("expected username in summary, got %s", alert.Summary)
	}
}

func TestRecentErrorAlertFiresWithFewerThanWindowEvents(t *testing.T) {
	events := []storage.LogEvent{
		recentErrorEvent(1, "error", 5, 0, ""),
		recentErrorEvent(2, "error", 5, 0, ""),
		recentErrorEvent(3, "error", 5, 0, ""),
		recentErrorEvent(4, "consume", 5, 0, ""),
	}

	items := appendRecentErrorAlerts(nil, newestFirst(events))
	alert, ok := findAlert(items, "recent_errors", "渠道错误激增")
	if !ok {
		t.Fatalf("expected channel alert, got %+v", items)
	}
	if !strings.Contains(alert.Summary, "4 条请求中 3 条失败") {
		t.Fatalf("unexpected summary: %s", alert.Summary)
	}
}

func TestRecentErrorAlertSeparatesDimensions(t *testing.T) {
	var events []storage.LogEvent
	for id := int64(1); id <= 3; id++ {
		events = append(events, recentErrorEvent(id, "error", 18, 7, "alice"))
	}
	for id := int64(4); id <= 6; id++ {
		events = append(events, recentErrorEvent(id, "consume", 21, 9, "bob"))
	}

	items := appendRecentErrorAlerts(nil, newestFirst(events))
	if len(items) != 2 {
		t.Fatalf("expected channel + user alerts for the failing pair only, got %+v", items)
	}
	if _, ok := findAlert(items, "recent_errors", "渠道错误激增"); !ok {
		t.Fatalf("missing channel alert: %+v", items)
	}
	if _, ok := findAlert(items, "recent_errors", "客户错误激增"); !ok {
		t.Fatalf("missing user alert: %+v", items)
	}
}
