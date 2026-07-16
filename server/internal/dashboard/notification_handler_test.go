package dashboard

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"controltower/server/internal/storage"
)

func testAlert() storage.Alert {
	return storage.Alert{
		ID:         "alert-1",
		InstanceID: "inst-a",
		RuleKey:    "recent_errors",
		Severity:   "warning",
		Status:     "firing",
		Title:      "渠道错误激增",
		Summary:    "渠道 18 最近 10 条请求中 4 条失败",
		LastSeenAt: time.Date(2026, 7, 10, 12, 0, 0, 0, time.UTC),
	}
}

func TestSendDingTalkNotificationBuildsTextMessage(t *testing.T) {
	var received map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(body, &received)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"errcode":0,"errmsg":"ok"}`))
	}))
	defer server.Close()

	channel := storage.NotificationChannel{ID: "chan-1", ChannelType: "dingtalk", WebhookURL: server.URL}
	delivery := sendWebhookNotification(http.Client{Timeout: time.Second}, testAlert(), channel, time.Now().UTC())

	if delivery.Status != "sent" {
		t.Fatalf("expected sent, got %s (%s)", delivery.Status, delivery.ErrorSummary)
	}
	if received["msgtype"] != "text" {
		t.Fatalf("expected msgtype text, got %v", received["msgtype"])
	}
	text, ok := received["text"].(map[string]any)
	if !ok {
		t.Fatalf("expected text object, got %v", received["text"])
	}
	content, _ := text["content"].(string)
	if !strings.Contains(content, "告警") || !strings.Contains(content, "渠道错误激增") || !strings.Contains(content, "inst-a") {
		t.Fatalf("unexpected dingtalk content: %s", content)
	}
}

func TestSendDingTalkNotificationFailsOnErrcode(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"errcode":310000,"errmsg":"keywords not in content"}`))
	}))
	defer server.Close()

	channel := storage.NotificationChannel{ID: "chan-1", ChannelType: "dingtalk", WebhookURL: server.URL}
	delivery := sendWebhookNotification(http.Client{Timeout: time.Second}, testAlert(), channel, time.Now().UTC())

	if delivery.Status != "failed" {
		t.Fatalf("expected failed, got %s", delivery.Status)
	}
	if !strings.Contains(delivery.ErrorSummary, "310000") {
		t.Fatalf("expected errcode in summary, got %s", delivery.ErrorSummary)
	}
}

func TestSendGenericWebhookNotificationKeepsJSONPayload(t *testing.T) {
	var received map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(body, &received)
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	channel := storage.NotificationChannel{ID: "chan-1", ChannelType: "webhook", WebhookURL: server.URL}
	delivery := sendWebhookNotification(http.Client{Timeout: time.Second}, testAlert(), channel, time.Now().UTC())

	if delivery.Status != "sent" {
		t.Fatalf("expected sent, got %s (%s)", delivery.Status, delivery.ErrorSummary)
	}
	if received["alert_id"] != "alert-1" || received["rule_key"] != "recent_errors" {
		t.Fatalf("unexpected webhook payload: %v", received)
	}
}

func TestSendWeComNotificationValidatesErrcodeAndAttempt(t *testing.T) {
	var content string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var payload struct {
			Text struct {
				Content string `json:"content"`
			} `json:"text"`
		}
		_ = json.NewDecoder(r.Body).Decode(&payload)
		content = payload.Text.Content
		_, _ = w.Write([]byte(`{"errcode":0,"errmsg":"ok"}`))
	}))
	defer server.Close()
	channel := storage.NotificationChannel{ID: "wecom", ChannelType: "wecom", WebhookURL: server.URL}
	delivery := sendWebhookNotificationAttempt(http.Client{Timeout: time.Second}, testAlert(), channel, time.Now().UTC(), 2, 8)
	if delivery.Status != "sent" || delivery.Attempts != 2 {
		t.Fatalf("unexpected delivery: %#v", delivery)
	}
	if !strings.Contains(content, "[告警]") || !strings.Contains(content, "inst-a") {
		t.Fatalf("unexpected content: %s", content)
	}
}

func TestSendWeComNotificationFailsOnErrcode(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"errcode":93000,"errmsg":"invalid webhook"}`))
	}))
	defer server.Close()
	delivery := sendWebhookNotification(http.Client{Timeout: time.Second}, testAlert(), storage.NotificationChannel{ID: "wecom", ChannelType: "wecom", WebhookURL: server.URL}, time.Now().UTC())
	if delivery.Status != "failed" || !strings.Contains(delivery.ErrorSummary, "93000") {
		t.Fatalf("unexpected delivery: %#v", delivery)
	}
}

func TestNotificationChannelFromRequestChannelTypes(t *testing.T) {
	now := time.Now().UTC()
	base := NotificationChannelRequest{Name: "ops", WebhookURL: "https://example.com/hook", Enabled: true}

	channel, ok := notificationChannelFromRequest(base, now)
	if !ok || channel.ChannelType != "webhook" {
		t.Fatalf("expected default webhook type, got %+v ok=%v", channel, ok)
	}

	base.ChannelType = "dingtalk"
	channel, ok = notificationChannelFromRequest(base, now)
	if !ok || channel.ChannelType != "dingtalk" {
		t.Fatalf("expected dingtalk type, got %+v ok=%v", channel, ok)
	}
	base.ChannelType = "wecom"
	channel, ok = notificationChannelFromRequest(base, now)
	if !ok || channel.ChannelType != "wecom" {
		t.Fatalf("expected wecom type, got %+v ok=%v", channel, ok)
	}

	base.ChannelType = "carrier-pigeon"
	if _, ok = notificationChannelFromRequest(base, now); ok {
		t.Fatalf("expected unknown channel type to be rejected")
	}
}
