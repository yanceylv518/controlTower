package dashboard

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"controltower/server/internal/ingest"
	"controltower/server/internal/storage"
)

type dingTalkCapture struct {
	mu       sync.Mutex
	contents []string
}

func (c *dingTalkCapture) add(content string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.contents = append(c.contents, content)
}

func (c *dingTalkCapture) matching(substr string) int {
	c.mu.Lock()
	defer c.mu.Unlock()
	count := 0
	for _, content := range c.contents {
		if strings.Contains(content, substr) {
			count++
		}
	}
	return count
}

func TestNotificationRunnerSendsRecentErrorAlertToDingTalkAndRenotifiesAfterRecovery(t *testing.T) {
	capture := &dingTalkCapture{}
	dingTalk := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var payload struct {
			Text struct {
				Content string `json:"content"`
			} `json:"text"`
		}
		_ = json.Unmarshal(body, &payload)
		capture.add(payload.Text.Content)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"errcode":0,"errmsg":"ok"}`))
	}))
	defer dingTalk.Close()

	store := ingest.NewMemoryStore()
	now := time.Date(2026, 7, 10, 12, 0, 0, 0, time.UTC)
	if err := store.UpsertNotificationChannel(storage.NotificationChannel{ID: "chan-dingtalk", ChannelType: "dingtalk", Name: "ops", WebhookURL: dingTalk.URL, Enabled: true, CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatalf("upsert channel: %v", err)
	}

	nextLogID := int64(0)
	insertEvents := func(logType string, count int) {
		for i := 0; i < count; i++ {
			nextLogID++
			event := storage.LogEvent{
				InstanceID:  "inst-a",
				SourceLogID: nextLogID,
				CreatedAt:   now.Add(time.Duration(nextLogID) * time.Second),
				LogType:     logType,
				ChannelID:   18,
				UserID:      7,
				Username:    "alice",
			}
			if _, err := store.InsertLogEvent(event); err != nil {
				t.Fatalf("insert log event: %v", err)
			}
		}
	}

	runner := NewAlertNotificationRunner(store, store, store, store, store, time.Second)

	// Episode 1: 3 errors among the latest events fire channel + user alerts.
	insertEvents("consume", 2)
	insertEvents("error", 3)
	if err := runner.RunOnce(); err != nil {
		t.Fatalf("run once: %v", err)
	}
	if got := capture.matching("渠道错误激增"); got != 1 {
		t.Fatalf("expected 1 channel alert message, got %d (%v)", got, capture.contents)
	}
	if got := capture.matching("客户错误激增"); got != 1 {
		t.Fatalf("expected 1 user alert message, got %d", got)
	}

	// Still firing: a second pass must not send duplicates.
	if err := runner.RunOnce(); err != nil {
		t.Fatalf("run once: %v", err)
	}
	if got := capture.matching("渠道错误激增"); got != 1 {
		t.Fatalf("expected no duplicate while firing, got %d messages", got)
	}

	// Recovery: 10 newer successes push the errors out of the window.
	insertEvents("consume", 10)
	if err := runner.RunOnce(); err != nil {
		t.Fatalf("run once: %v", err)
	}

	// Episode 2: fresh errors must notify again.
	insertEvents("error", 3)
	if err := runner.RunOnce(); err != nil {
		t.Fatalf("run once: %v", err)
	}
	if got := capture.matching("渠道错误激增"); got != 2 {
		t.Fatalf("expected renotification after recovery, got %d messages", got)
	}
}
