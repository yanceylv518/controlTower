package dashboard

import (
	"context"
	"controltower/server/internal/storage"
	"controltower/server/internal/voicealert"
	"fmt"
	"net/http"
	"time"
)

// Trial events are never inserted into the firing/resolved alert lifecycle.
// The trial outbox reserves the attempt before entering this sender.
func TrialMessageSender(store NotificationStore) func(context.Context, voicealert.TrialEvent) (string, error) {
	return func(ctx context.Context, e voicealert.TrialEvent) (string, error) {
		channels, err := store.QueryNotificationChannels(true)
		if err != nil {
			return "unknown", err
		}
		matched, sent := 0, 0
		for _, ch := range channels {
			if ch.SiteID != e.Site {
				continue
			}
			allowed := len(ch.RuleKeys) == 0
			for _, key := range ch.RuleKeys {
				if key == "trial_started" {
					allowed = true
				}
			}
			if !allowed {
				continue
			}
			matched++
			if err = ctx.Err(); err != nil {
				return "unknown", err
			}
			result := "成功"
			if e.Log.Type != 2 {
				result = "失败调用，请关注接入情况"
			}
			alert := storage.Alert{ID: e.ID, InstanceID: e.Site, RuleKey: "trial_started", Severity: "info", Status: "firing", Title: e.Customer + "已开始接口测试", Summary: fmt.Sprintf("站点：%s；客户：%s；账户：#%d；Key：#%d；模型：%s；调用结果：%s；调用时间：%s；请进入测试跟进查看。", e.SiteName, e.Customer, e.Log.UserID, e.Log.TokenID, e.Log.Model, result, e.Log.CreatedAt.Local().Format("2006-01-02 15:04:05")), LastSeenAt: e.DetectedAt}
			delivery := sendWebhookNotificationAttempt(http.Client{Timeout: 3 * time.Second}, alert, ch, time.Now().UTC(), 1, 1)
			if delivery.Status == "sent" {
				sent++
			}
		}
		if matched == 0 {
			return "no_channel", nil
		}
		if sent == matched {
			return "sent", nil
		}
		if sent > 0 {
			return "partial", nil
		}
		return "unknown", nil
	}
}
