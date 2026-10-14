package dashboard

import (
	"encoding/json"
	"errors"
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

type failedCircuitImport struct {
	event   storage.Alert
	queried bool
}

func (s *failedCircuitImport) SyncCircuitAlerts(time.Time) error {
	return errors.New("candidate timeout")
}
func (s *failedCircuitImport) CircuitNotificationAlerts(time.Time) ([]storage.Alert, error) {
	s.queried = true
	return []storage.Alert{s.event}, nil
}

func TestNotificationRunnerImportFailureStillDeliversExistingCircuit(t *testing.T) {
	capture := &dingTalkCapture{}
	endpoint := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		capture.add(string(body))
		_, _ = w.Write([]byte(`{"errcode":0,"errmsg":"ok"}`))
	}))
	defer endpoint.Close()
	store := ingest.NewMemoryStore()
	now := time.Now().UTC()
	if err := store.CreateInstance(storage.Instance{ID: "node", SiteID: "site", Enabled: true}); err != nil {
		t.Fatal(err)
	}
	if err := store.UpsertNotificationChannel(storage.NotificationChannel{ID: "group", SiteID: "site", ChannelType: "dingtalk", Name: "test", WebhookURL: endpoint.URL, Enabled: true, CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
	source := &failedCircuitImport{event: storage.Alert{ID: "existing", InstanceID: "node", RuleKey: "channel_circuit_recovered", Status: "resolved", Severity: "info", Title: "existing recovery", FirstSeenAt: now, LastSeenAt: now}}
	runner := NewAlertNotificationRunner(store, store, store, store, store, time.Second).WithInstanceStore(store).WithCircuitAlerts(source)
	if err := runner.RunOnce(); err == nil || !strings.Contains(err.Error(), "import circuit events") {
		t.Fatalf("missing stage error: %v", err)
	}
	if !source.queried || capture.matching("existing recovery") != 1 {
		t.Fatalf("existing circuit delivery blocked: %+v", capture.contents)
	}
}

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
	if err := store.CreateInstance(storage.Instance{ID: "inst-a", SiteID: "site-a", Enabled: true}); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 7, 10, 12, 0, 0, 0, time.UTC)
	if err := store.UpsertNotificationChannel(storage.NotificationChannel{ID: "chan-dingtalk", SiteID: "site-a", ChannelType: "dingtalk", Name: "ops", WebhookURL: dingTalk.URL, Enabled: true, CreatedAt: now, UpdatedAt: now}); err != nil {
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

	runner := NewAlertNotificationRunner(store, store, store, store, store, time.Second).WithInstanceStore(store)

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
