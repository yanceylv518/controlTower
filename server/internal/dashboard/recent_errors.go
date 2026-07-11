package dashboard

import (
	"fmt"
	"strconv"
	"time"

	"controltower/server/internal/storage"
)

const (
	recentErrorWindow            = 10
	recentErrorWarningThreshold  = 3
	recentErrorCriticalThreshold = 5
)

// appendRecentErrorAlerts evaluates the recent-error rule for every channel
// and every user: within a dimension's most recent recentErrorWindow log
// events, recentErrorWarningThreshold or more errors fire an alert.
// Events must be ordered newest first; this rule only sees events uploaded to
// log_events, so the agent must run with CT_LOG_EVENT_MODE=full_debug.
func appendRecentErrorAlerts(items []AlertItem, events []storage.LogEvent) []AlertItem {
	type group struct {
		title      string
		label      string
		instanceID string
		target     string
		total      int
		errors     int
		seenAt     time.Time
	}
	groups := make(map[string]*group)
	order := make([]string, 0)
	add := func(instanceID string, target string, title string, label string, event storage.LogEvent) {
		key := instanceID + "\x00" + target
		item := groups[key]
		if item == nil {
			item = &group{title: title, label: label, instanceID: instanceID, target: target, seenAt: event.CreatedAt}
			groups[key] = item
			order = append(order, key)
		}
		if item.total >= recentErrorWindow {
			return
		}
		item.total++
		if event.LogType == "error" {
			item.errors++
		}
	}

	for _, event := range events {
		if event.ChannelID > 0 {
			target := "channel:" + strconv.FormatInt(event.ChannelID, 10)
			add(event.InstanceID, target, "渠道错误激增", fmt.Sprintf("渠道 %d", event.ChannelID), event)
		}
		if event.UserID > 0 {
			target := "user:" + strconv.FormatInt(event.UserID, 10)
			label := fmt.Sprintf("客户 %d", event.UserID)
			if event.Username != "" {
				label = fmt.Sprintf("客户 %s(%d)", event.Username, event.UserID)
			}
			add(event.InstanceID, target, "客户错误激增", label, event)
		}
	}

	for _, key := range order {
		item := groups[key]
		if item.errors < recentErrorWarningThreshold {
			continue
		}
		severity := "warning"
		if item.errors >= recentErrorCriticalThreshold {
			severity = "critical"
		}
		items = append(items, AlertItem{
			ID:          alertID(item.instanceID, "recent_errors", item.target),
			InstanceID:  item.instanceID,
			RuleKey:     "recent_errors",
			Severity:    severity,
			Status:      "firing",
			Title:       item.title,
			Summary:     fmt.Sprintf("%s 最近 %d 条请求中 %d 条失败", item.label, item.total, item.errors),
			SeenAt:      item.seenAt,
			FirstSeenAt: item.seenAt,
			LastSeenAt:  item.seenAt,
		})
	}
	return items
}
