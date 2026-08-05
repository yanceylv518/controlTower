package dashboard

import (
	"testing"
	"time"

	"controltower/server/internal/billing"
)

func TestApplyAnomalyCounts(t *testing.T) {
	counts := []billing.AnomalyCount{
		{UserID: 7, ChannelID: 11, Day: time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC), ModelName: "model-a", GroupName: "default", Count: 3},
		{UserID: 8, ChannelID: 11, Day: time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC), ModelName: "model-a", GroupName: "default", Count: 2},
	}

	users := []billing.UserSummary{{UserID: 7}, {UserID: 8}}
	applyUserAnomalyCounts(users, counts)
	if users[0].AbnormalRows != 3 || users[1].AbnormalRows != 2 {
		t.Fatalf("unexpected user anomaly counts: %+v", users)
	}

	channels := []billing.ChannelSummary{{ChannelID: 11}}
	applyChannelAnomalyCounts(channels, counts)
	if channels[0].AbnormalRows != 5 {
		t.Fatalf("unexpected channel anomaly count: %+v", channels[0])
	}

	// Multiple pricing tiers can produce multiple detail rows for one day/model/group.
	// The anomaly count belongs to that daily group and must only be shown once.
	details := []billing.DetailItem{
		{Day: "2026-08-01", ModelName: "model-a", GroupName: "default", TierFrom: 0},
		{Day: "2026-08-01", ModelName: "model-a", GroupName: "default", TierFrom: 1000},
	}
	applyDetailAnomalyCounts(details, counts, 7, 0)
	if details[0].AbnormalRows != 3 || details[1].AbnormalRows != 0 {
		t.Fatalf("unexpected detail anomaly counts: %+v", details)
	}
}
