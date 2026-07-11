package dashboard

import (
	"bytes"
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"controltower/server/internal/storage"
)

type NotificationStore interface {
	UpsertNotificationChannel(channel storage.NotificationChannel) error
	QueryNotificationChannels(enabledOnly bool) ([]storage.NotificationChannel, error)
	InsertNotificationDelivery(delivery storage.NotificationDelivery) error
	NotificationDeliveryDue(alertID string, channelID string, now time.Time) (bool, error)
	QueryNotificationDeliveries(query storage.NotificationDeliveryQuery) ([]storage.NotificationDelivery, error)
	ExpireDeliveriesForResolvedAlerts(now time.Time) error
}

type NotificationChannelRequest struct {
	ID          string `json:"id"`
	ChannelType string `json:"channel_type"`
	Name        string `json:"name"`
	WebhookURL  string `json:"webhook_url"`
	Enabled     bool   `json:"enabled"`
}

type NotificationChannelItem struct {
	ID               string    `json:"id"`
	ChannelType      string    `json:"channel_type"`
	Name             string    `json:"name"`
	WebhookURLMasked string    `json:"webhook_url_masked"`
	Enabled          bool      `json:"enabled"`
	CreatedAt        time.Time `json:"created_at"`
	UpdatedAt        time.Time `json:"updated_at"`
}

type NotificationChannelListResponse struct {
	Items []NotificationChannelItem `json:"items"`
}

type NotificationDeliveryItem struct {
	ID            string    `json:"id"`
	AlertID       string    `json:"alert_id"`
	ChannelID     string    `json:"channel_id"`
	Status        string    `json:"status"`
	AttemptedAt   time.Time `json:"attempted_at"`
	NextAttemptAt time.Time `json:"next_attempt_at"`
	Attempts      int       `json:"attempts"`
	StatusCode    int       `json:"status_code"`
	ErrorSummary  string    `json:"error_summary"`
}

type NotificationDeliveryListResponse struct {
	Items []NotificationDeliveryItem `json:"items"`
}

func (h Handler) WithNotificationStore(store NotificationStore) Handler {
	h.notificationStore = store
	return h
}

func (h Handler) HandleNotificationChannels(w http.ResponseWriter, r *http.Request) {
	if h.notificationStore == nil {
		writeDashboardError(w, http.StatusInternalServerError, "notification_store_not_configured")
		return
	}
	switch r.Method {
	case http.MethodGet:
		channels, err := h.notificationStore.QueryNotificationChannels(false)
		if err != nil {
			writeDashboardError(w, http.StatusInternalServerError, "query_failed")
			return
		}
		writeDashboardJSON(w, http.StatusOK, NotificationChannelListResponse{Items: notificationChannelItems(channels)})
	case http.MethodPost:
		var request NotificationChannelRequest
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			writeDashboardError(w, http.StatusBadRequest, "invalid_json")
			return
		}
		channel, ok := notificationChannelFromRequest(request, time.Now().UTC())
		if !ok {
			writeDashboardError(w, http.StatusBadRequest, "invalid_notification_channel")
			return
		}
		if err := h.notificationStore.UpsertNotificationChannel(channel); err != nil {
			writeDashboardError(w, http.StatusInternalServerError, "query_failed")
			return
		}
		writeDashboardJSON(w, http.StatusOK, NotificationChannelListResponse{Items: notificationChannelItems([]storage.NotificationChannel{channel})})
	default:
		writeDashboardError(w, http.StatusMethodNotAllowed, "method_not_allowed")
	}
}

func (h Handler) HandleNotificationDeliveries(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeDashboardError(w, http.StatusMethodNotAllowed, "method_not_allowed")
		return
	}
	if h.notificationStore == nil {
		writeDashboardError(w, http.StatusInternalServerError, "notification_store_not_configured")
		return
	}
	deliveries, err := h.notificationStore.QueryNotificationDeliveries(parseNotificationDeliveryQuery(r))
	if err != nil {
		writeDashboardError(w, http.StatusInternalServerError, "query_failed")
		return
	}
	writeDashboardJSON(w, http.StatusOK, NotificationDeliveryListResponse{Items: notificationDeliveryItems(deliveries)})
}

func (h Handler) dispatchAlertNotifications(alerts []storage.Alert) error {
	if h.notificationStore == nil {
		return nil
	}
	// Release "sent" deliveries of resolved alerts so a later firing episode
	// of the same alert notifies again instead of being deduplicated forever.
	if err := h.notificationStore.ExpireDeliveriesForResolvedAlerts(time.Now().UTC()); err != nil {
		return err
	}
	channels, err := h.notificationStore.QueryNotificationChannels(true)
	if err != nil {
		return err
	}
	if len(channels) == 0 {
		return nil
	}
	client := http.Client{Timeout: 3 * time.Second}
	for _, alert := range alerts {
		if alert.Status != "firing" {
			continue
		}
		for _, channel := range channels {
			due, err := h.notificationStore.NotificationDeliveryDue(alert.ID, channel.ID, time.Now().UTC())
			if err != nil {
				return err
			}
			if !due {
				continue
			}
			delivery := sendWebhookNotification(client, alert, channel, time.Now().UTC())
			if err := h.notificationStore.InsertNotificationDelivery(delivery); err != nil {
				return err
			}
		}
	}
	return nil
}

func sendWebhookNotification(client http.Client, alert storage.Alert, channel storage.NotificationChannel, now time.Time) storage.NotificationDelivery {
	delivery := storage.NotificationDelivery{ID: notificationDeliveryID(alert.ID, channel.ID), AlertID: alert.ID, ChannelID: channel.ID, Status: "failed", AttemptedAt: now, NextAttemptAt: now.Add(5 * time.Minute), Attempts: 1}
	body, err := json.Marshal(notificationPayload(alert, channel))
	if err != nil {
		delivery.ErrorSummary = truncateSummary(err.Error())
		return delivery
	}
	req, err := http.NewRequest(http.MethodPost, channel.WebhookURL, bytes.NewReader(body))
	if err != nil {
		delivery.ErrorSummary = truncateSummary(err.Error())
		return delivery
	}
	req.Header.Set("Content-Type", "application/json")
	if channel.SecretValue != "" && channel.ChannelType != "dingtalk" {
		req.Header.Set("X-Control-Tower-Secret", channel.SecretValue)
	}
	resp, err := client.Do(req)
	if err != nil {
		delivery.ErrorSummary = truncateSummary(err.Error())
		return delivery
	}
	defer resp.Body.Close()
	delivery.StatusCode = resp.StatusCode
	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		if channel.ChannelType == "dingtalk" {
			// DingTalk robots answer HTTP 200 even on rejection; the real
			// outcome is in the errcode field of the response body.
			if err := checkDingTalkResponse(resp.Body); err != nil {
				delivery.ErrorSummary = truncateSummary(err.Error())
				return delivery
			}
		}
		delivery.Status = "sent"
		delivery.NextAttemptAt = time.Date(9999, 1, 1, 0, 0, 0, 0, time.UTC)
		return delivery
	}
	delivery.ErrorSummary = fmt.Sprintf("webhook returned HTTP %d", resp.StatusCode)
	return delivery
}

func notificationPayload(alert storage.Alert, channel storage.NotificationChannel) map[string]any {
	if channel.ChannelType == "dingtalk" {
		content := fmt.Sprintf("【Control Tower 告警】%s\n级别: %s\n实例: %s\n详情: %s\n时间: %s",
			alert.Title, alert.Severity, alert.InstanceID, alert.Summary, alert.LastSeenAt.Local().Format("2006-01-02 15:04:05"))
		return map[string]any{"msgtype": "text", "text": map[string]string{"content": content}}
	}
	return map[string]any{"alert_id": alert.ID, "instance_id": alert.InstanceID, "rule_key": alert.RuleKey, "severity": alert.Severity, "status": alert.Status, "title": alert.Title, "summary": alert.Summary, "last_seen_at": alert.LastSeenAt}
}

func checkDingTalkResponse(body io.Reader) error {
	data, err := io.ReadAll(io.LimitReader(body, 4096))
	if err != nil {
		return fmt.Errorf("read dingtalk response: %w", err)
	}
	var result struct {
		ErrCode int    `json:"errcode"`
		ErrMsg  string `json:"errmsg"`
	}
	if err := json.Unmarshal(data, &result); err != nil {
		return nil
	}
	if result.ErrCode != 0 {
		return fmt.Errorf("dingtalk errcode %d: %s", result.ErrCode, result.ErrMsg)
	}
	return nil
}

func notificationChannelFromRequest(request NotificationChannelRequest, now time.Time) (storage.NotificationChannel, bool) {
	name := strings.TrimSpace(request.Name)
	webhookURL := strings.TrimSpace(request.WebhookURL)
	if name == "" || webhookURL == "" || !(strings.HasPrefix(webhookURL, "http://") || strings.HasPrefix(webhookURL, "https://")) {
		return storage.NotificationChannel{}, false
	}
	channelType := strings.ToLower(strings.TrimSpace(request.ChannelType))
	if channelType == "" {
		channelType = "webhook"
	}
	if channelType != "webhook" && channelType != "dingtalk" {
		return storage.NotificationChannel{}, false
	}
	id := strings.TrimSpace(request.ID)
	if id == "" {
		id = notificationChannelID(name, webhookURL, now)
	}
	return storage.NotificationChannel{ID: id, ChannelType: channelType, Name: name, WebhookURL: webhookURL, Enabled: request.Enabled, CreatedAt: now, UpdatedAt: now}, true
}

func notificationChannelItems(channels []storage.NotificationChannel) []NotificationChannelItem {
	items := make([]NotificationChannelItem, 0, len(channels))
	for _, channel := range channels {
		items = append(items, NotificationChannelItem{ID: channel.ID, ChannelType: channel.ChannelType, Name: channel.Name, WebhookURLMasked: maskWebhookURL(channel.WebhookURL), Enabled: channel.Enabled, CreatedAt: channel.CreatedAt, UpdatedAt: channel.UpdatedAt})
	}
	return items
}

func notificationDeliveryItems(deliveries []storage.NotificationDelivery) []NotificationDeliveryItem {
	items := make([]NotificationDeliveryItem, 0, len(deliveries))
	for _, delivery := range deliveries {
		items = append(items, NotificationDeliveryItem{ID: delivery.ID, AlertID: delivery.AlertID, ChannelID: delivery.ChannelID, Status: delivery.Status, AttemptedAt: delivery.AttemptedAt, NextAttemptAt: delivery.NextAttemptAt, Attempts: delivery.Attempts, StatusCode: delivery.StatusCode, ErrorSummary: delivery.ErrorSummary})
	}
	return items
}

func parseNotificationDeliveryQuery(r *http.Request) storage.NotificationDeliveryQuery {
	query := r.URL.Query()
	return storage.NotificationDeliveryQuery{AlertID: query.Get("alert_id"), ChannelID: query.Get("channel_id"), Status: query.Get("status"), Limit: parseInt(query.Get("limit")), Offset: parseInt(query.Get("offset"))}
}

func notificationChannelID(name string, webhookURL string, now time.Time) string {
	return shortHash(fmt.Sprintf("%s:%s:%d", name, webhookURL, now.UnixNano()))
}

func notificationDeliveryID(alertID string, channelID string) string {
	return shortHash(alertID + ":" + channelID)
}

func shortHash(value string) string {
	sum := sha1.Sum([]byte(value))
	return hex.EncodeToString(sum[:])
}

func maskWebhookURL(value string) string {
	if value == "" {
		return ""
	}
	if len(value) <= 18 {
		return "***"
	}
	return value[:12] + "..." + value[len(value)-6:]
}

func truncateSummary(value string) string {
	if len(value) > 300 {
		return value[:300]
	}
	return value
}
