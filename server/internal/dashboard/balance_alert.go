package dashboard

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"

	"controltower/server/internal/settings"
	"controltower/server/internal/storage"
)

type BalanceSource interface {
	ListUserBalances(context.Context, string) ([]PassthroughUser, error)
}

type BalanceUsageSource interface {
	QueryUserQuotaUsage(context.Context, time.Time) ([]storage.UserQuotaUsage, error)
}

type BalanceAlertSettingsStore interface {
	ListBalanceAlertUserSettings(context.Context, string) (map[int64]storage.BalanceAlertUserSetting, error)
	PutBalanceAlertUserSetting(context.Context, storage.BalanceAlertUserSetting) error
}

type balanceAlertCache struct {
	mu     sync.Mutex
	loaded time.Time
	items  []AlertItem
}

func (h Handler) balanceAlerts(values settings.Values, now time.Time) ([]AlertItem, error) {
	if !values.BalanceAlertEnabled || h.balanceSource == nil || h.balanceUsage == nil || h.instanceStore == nil || h.balanceSettings == nil {
		return nil, nil
	}
	if h.balanceCache != nil {
		h.balanceCache.mu.Lock()
		defer h.balanceCache.mu.Unlock()
		if now.Sub(h.balanceCache.loaded) < 5*time.Minute {
			return append([]AlertItem(nil), h.balanceCache.items...), nil
		}
	}
	instances, err := h.instanceStore.ListInstances()
	if err != nil {
		return nil, err
	}
	usageRows, err := h.balanceUsage.QueryUserQuotaUsage(context.Background(), now.Add(-time.Duration(values.BalanceLookbackHours)*time.Hour))
	if err != nil {
		return nil, err
	}

	type siteInfo struct {
		alertInstance string
		instanceIDs   map[string]bool
	}
	sites := map[string]*siteInfo{}
	instanceSite := map[string]string{}
	for _, instance := range instances {
		if !instance.Enabled {
			continue
		}
		site := siteOf(instance)
		instanceSite[instance.ID] = site
		if sites[site] == nil {
			sites[site] = &siteInfo{alertInstance: instance.ID, instanceIDs: map[string]bool{}}
		}
		sites[site].instanceIDs[instance.ID] = true
	}
	type usage struct {
		requests, quota int64
		firstBucket     time.Time
	}
	bySite := map[string]map[int64]usage{}
	for _, row := range usageRows {
		site := instanceSite[row.InstanceID]
		if site == "" {
			continue
		}
		userID, ok := userIDFromDimension(row.DimensionKey)
		if !ok {
			continue
		}
		if bySite[site] == nil {
			bySite[site] = map[int64]usage{}
		}
		v := bySite[site][userID]
		v.requests += row.RequestCount
		v.quota += row.Quota
		if v.firstBucket.IsZero() || (!row.FirstBucket.IsZero() && row.FirstBucket.Before(v.firstBucket)) {
			v.firstBucket = row.FirstBucket
		}
		bySite[site][userID] = v
	}

	alerts := []AlertItem{}
	for site, info := range sites {
		enabledUsers, err := h.balanceSettings.ListBalanceAlertUserSettings(context.Background(), site)
		if err != nil {
			return nil, fmt.Errorf("balance settings for site %s: %w", site, err)
		}
		users, err := h.balanceSource.ListUserBalances(context.Background(), site)
		if err != nil {
			return nil, fmt.Errorf("balance users for site %s: %w", site, err)
		}
		for _, user := range users {
			if setting, ok := enabledUsers[user.ID]; !ok || !setting.Enabled {
				continue
			}
			v := bySite[site][user.ID]
			if user.Status != 1 {
				continue
			}
			if user.Quota > 0 && (v.requests < int64(values.BalanceMinRequests) || v.quota <= 0) {
				continue
			}
			hours := float64(values.BalanceLookbackHours)
			if !v.firstBucket.IsZero() {
				if covered := now.Sub(v.firstBucket).Hours(); covered >= 1 && covered < hours {
					hours = covered
				}
			}
			dailyQuota := float64(v.quota) * 24 / hours
			runwayDays := 0.0
			if user.Quota > 0 {
				runwayDays = float64(user.Quota) / dailyQuota
			}
			severity := ""
			if runwayDays <= values.BalanceCritDays {
				severity = "critical"
			} else if runwayDays <= values.BalanceWarnDays {
				severity = "warning"
			}
			if severity == "" {
				continue
			}
			name := user.DisplayName
			if name == "" {
				name = user.Username
			}
			if name == "" {
				name = strconv.FormatInt(user.ID, 10)
			}
			balance := float64(user.Quota) / float64(values.QuotaPerUnit)
			daily := dailyQuota / float64(values.QuotaPerUnit)
			key := info.alertInstance + ":user:" + strconv.FormatInt(user.ID, 10)
			summary := fmt.Sprintf("用户：%s（ID %d）\n当前余额：%s%.2f\n近 %d 小时日均消费：%s%.2f\n预计可用：%.1f 天\n请求样本：%d 次", name, user.ID, values.CurrencySymbol, balance, values.BalanceLookbackHours, values.CurrencySymbol, daily, runwayDays, v.requests)
			if user.Quota <= 0 {
				summary = fmt.Sprintf("用户：%s（ID %d）\n当前余额：%s%.2f\n状态：余额已耗尽", name, user.ID, values.CurrencySymbol, balance)
			}
			alerts = append(alerts, AlertItem{
				ID: alertID(info.alertInstance, "user_low_balance", strconv.FormatInt(user.ID, 10)), InstanceID: info.alertInstance,
				DimensionType: "instance_user", DimensionKey: key, RuleKey: "user_low_balance", Severity: severity, Status: "firing",
				Title:   "余额预计即将耗尽",
				Summary: summary,
				SeenAt:  now, FirstSeenAt: now, LastSeenAt: now,
			})
		}
	}
	if h.balanceCache != nil {
		h.balanceCache.loaded = now
		h.balanceCache.items = append([]AlertItem(nil), alerts...)
	}
	return alerts, nil
}

func userIDFromDimension(key string) (int64, bool) {
	idx := strings.LastIndex(key, ":user:")
	if idx < 0 {
		return 0, false
	}
	id, err := strconv.ParseInt(key[idx+6:], 10, 64)
	return id, err == nil && id > 0
}
