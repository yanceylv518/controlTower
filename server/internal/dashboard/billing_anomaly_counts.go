package dashboard

import (
	"context"

	"controltower/server/internal/billing"
)

type billingAnomalyCounter interface {
	BillingAnomalyCountsForJob(context.Context, string) ([]billing.AnomalyCount, error)
}

func anomalyCounts(store any, ctx context.Context, jobID string) ([]billing.AnomalyCount, error) {
	counter, ok := store.(billingAnomalyCounter)
	if !ok || jobID == "" {
		return nil, nil
	}
	return counter.BillingAnomalyCountsForJob(ctx, jobID)
}

func applyUserAnomalyCounts(items []billing.UserSummary, counts []billing.AnomalyCount) {
	byUser := map[int64]int64{}
	for _, count := range counts {
		byUser[count.UserID] += count.Count
	}
	for i := range items {
		items[i].AbnormalRows = byUser[items[i].UserID]
	}
}

func applyChannelAnomalyCounts(items []billing.ChannelSummary, counts []billing.AnomalyCount) {
	byChannel := map[int64]int64{}
	for _, count := range counts {
		byChannel[count.ChannelID] += count.Count
	}
	for i := range items {
		items[i].AbnormalRows = byChannel[items[i].ChannelID]
	}
}

func applyDetailAnomalyCounts(items []billing.DetailItem, counts []billing.AnomalyCount, userID, channelID int64) {
	remaining := map[string]int64{}
	for _, count := range counts {
		if userID > 0 && count.UserID != userID || channelID > 0 && count.ChannelID != channelID {
			continue
		}
		key := count.Day.Format("2006-01-02") + "\x00" + count.ModelName + "\x00" + count.GroupName
		remaining[key] += count.Count
	}
	for i := range items {
		key := items[i].Day + "\x00" + items[i].ModelName + "\x00" + items[i].GroupName
		if count := remaining[key]; count > 0 {
			items[i].AbnormalRows = count
			delete(remaining, key)
		}
	}
}
