package dashboard

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"controltower/server/internal/ingest"
	"controltower/server/internal/storage"
	"controltower/server/internal/voicealert"
)

func TestRobotNotificationTimesUseBeijingTimezone(t *testing.T) {
	previous := time.Local
	t.Cleanup(func() { time.Local = previous })
	at := time.Date(2026, 12, 31, 18, 30, 0, 0, time.UTC)
	for _, local := range []*time.Location{time.UTC, time.FixedZone("host", -7*60*60)} {
		time.Local = local
		for _, source := range []*time.Location{time.UTC, time.FixedZone("source", 8*60*60)} {
			for _, channel := range []string{"wecom", "dingtalk"} {
				for _, rule := range []string{"high_cpu", "high_memory", "high_disk", "agent_offline", "recent_errors", "user_low_balance"} {
					alert := testAlert()
					alert.RuleKey, alert.LastSeenAt = rule, at.In(source)
					content := notificationPayload(alert, storage.NotificationChannel{ChannelType: channel})["text"].(map[string]string)["content"]
					if !strings.Contains(content, "2027-01-01 02:30:00") || strings.Contains(content, "UTC+8") {
						t.Fatalf("%s/%s host=%s source=%s: wrong notification time: %s", rule, channel, local, source, content)
					}
				}
			}
		}
	}
}

func TestTrialNotificationUsesBeijingCallAndDetectionTimes(t *testing.T) {
	previous := time.Local
	time.Local = time.UTC
	t.Cleanup(func() { time.Local = previous })
	for _, channel := range []string{"wecom", "dingtalk"} {
		t.Run(channel, func(t *testing.T) {
			var content string
			hook := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var payload struct {
					Text struct{ Content string } `json:"text"`
				}
				if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
					t.Error(err)
					w.WriteHeader(http.StatusBadRequest)
					return
				}
				content = payload.Text.Content
				_, _ = w.Write([]byte(`{"errcode":0}`))
			}))
			defer hook.Close()
			store := ingest.NewMemoryStore()
			if err := store.UpsertNotificationChannel(storage.NotificationChannel{ID: "trial", SiteID: "site", ChannelType: channel, WebhookURL: hook.URL, RuleKeys: []string{"trial_started"}, Enabled: true}); err != nil {
				t.Fatal(err)
			}
			at := time.Date(2026, 12, 31, 18, 30, 0, 0, time.UTC)
			status, err := TrialMessageSender(store)(context.Background(), voicealert.TrialEvent{ID: "event", Site: "site", Customer: "客户", Log: voicealert.TrialLog{Type: 2, CreatedAt: at}, DetectedAt: at.Add(time.Minute)})
			if err != nil || status != "sent" {
				t.Fatalf("send status=%s error=%v", status, err)
			}
			for _, want := range []string{"调用时间：2027-01-01 02:30:00", "时间: 2027-01-01 02:31:00"} {
				if !strings.Contains(content, want) || strings.Contains(content, "UTC+8") {
					t.Fatalf("missing %q in %s", want, content)
				}
			}
		})
	}
}

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

func TestBalanceNotificationUsesBusinessTemplate(t *testing.T) {
	alert := testAlert()
	alert.RuleKey = "user_low_balance"
	alert.Severity = "critical"
	alert.Summary = "用户：张三（ID 123）\n当前余额：¥108.50\n预计可用：1.7 天"
	payload := notificationPayload(alert, storage.NotificationChannel{ChannelType: "wecom"})
	text := payload["text"].(map[string]string)["content"]
	for _, want := range []string{"【余额严重告警】", "站点：inst-a", "用户：张三", "请及时联系用户充值"} {
		if !strings.Contains(text, want) {
			t.Fatalf("missing %q in %s", want, text)
		}
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
	if received["last_seen_at"] != testAlert().LastSeenAt.Format(time.RFC3339) {
		t.Fatalf("generic webhook timestamp changed: %v", received["last_seen_at"])
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
	base := NotificationChannelRequest{SiteID: "site-a", Name: "ops", WebhookURL: "https://example.com/hook", Enabled: true}

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
