package dashboard

import (
	"strings"

	"controltower/server/internal/storage"
)

var notificationRuleKeys = map[string]bool{
	"user_low_balance": true,
	"instance_offline": true,
	"agent_backlog":    true,
	"health_down":      true,
	"docker_stopped":   true,
	"high_error_rate":  true,
	"high_p95_latency": true,
	"high_cpu":         true,
	"high_memory":      true,
	"high_disk":        true,
	"recent_errors":    true,
}

func normalizeNotificationRules(keys []string) ([]string, bool) {
	result := make([]string, 0, len(keys))
	seen := map[string]bool{}
	for _, key := range keys {
		key = strings.TrimSpace(key)
		if !notificationRuleKeys[key] {
			return nil, false
		}
		if !seen[key] {
			seen[key] = true
			result = append(result, key)
		}
	}
	return result, true
}

func notificationChannelMatches(channel storage.NotificationChannel, alert storage.Alert, instanceSites map[string]string) bool {
	site := instanceSites[alert.InstanceID]
	if site == "" || channel.SiteID == "" || channel.SiteID != site {
		return false
	}
	if len(channel.RuleKeys) == 0 {
		return true
	}
	for _, key := range channel.RuleKeys {
		if key == alert.RuleKey {
			return true
		}
	}
	return false
}
