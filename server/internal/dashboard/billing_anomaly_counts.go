package dashboard

import (
	"context"
	"math/big"
	"time"

	"controltower/server/internal/billing"
)

func addAnomalyAmount(total *big.Rat, amount string) {
	if value, ok := new(big.Rat).SetString(amount); ok {
		total.Add(total, value)
	}
}

func formatAnomalyAmount(amount *big.Rat) string {
	if amount == nil {
		return "0.000000"
	}
	return billing.FormatAmount(amount, 6)
}

type billingAnomalyCounter interface {
	BillingAnomalyCountsForJob(context.Context, string) ([]billing.AnomalyCount, error)
}
type billingAnomalyRangeCounter interface {
	BillingAnomalyCountsForJobRange(context.Context, string, time.Time, time.Time) ([]billing.AnomalyCount, error)
}

func anomalyCounts(store any, ctx context.Context, jobID string) ([]billing.AnomalyCount, error) {
	counter, ok := store.(billingAnomalyCounter)
	if !ok || jobID == "" {
		return nil, nil
	}
	return counter.BillingAnomalyCountsForJob(ctx, jobID)
}
func anomalyCountsForRange(store any, ctx context.Context, jobID string, from, to time.Time) ([]billing.AnomalyCount, error) {
	counter, ok := store.(billingAnomalyRangeCounter)
	if !ok || jobID == "" {
		return nil, nil
	}
	return counter.BillingAnomalyCountsForJobRange(ctx, jobID, from, to)
}

func applyUserAnomalyCounts(items []billing.UserSummary, counts []billing.AnomalyCount) {
	byUser := map[int64]int64{}
	byAmount := map[int64]*big.Rat{}
	for _, count := range counts {
		byUser[count.UserID] += count.Count
		if byAmount[count.UserID] == nil {
			byAmount[count.UserID] = new(big.Rat)
		}
		addAnomalyAmount(byAmount[count.UserID], count.Amount)
	}
	for i := range items {
		items[i].AbnormalRows = byUser[items[i].UserID]
		items[i].AbnormalAmount = formatAnomalyAmount(byAmount[items[i].UserID])
	}
}

func applyChannelAnomalyCounts(items []billing.ChannelSummary, counts []billing.AnomalyCount) {
	byChannel := map[int64]int64{}
	byAmount := map[int64]*big.Rat{}
	for _, count := range counts {
		byChannel[count.ChannelID] += count.Count
		if byAmount[count.ChannelID] == nil {
			byAmount[count.ChannelID] = new(big.Rat)
		}
		addAnomalyAmount(byAmount[count.ChannelID], count.Amount)
	}
	for i := range items {
		items[i].AbnormalRows = byChannel[items[i].ChannelID]
		items[i].AbnormalAmount = formatAnomalyAmount(byAmount[items[i].ChannelID])
	}
}

func applyTokenAnomalyCounts(items []billing.TokenSummary, counts []billing.AnomalyCount) []billing.TokenSummary {
	byToken := map[int64]int64{}
	byAmount := map[int64]*big.Rat{}
	names := map[int64]string{}
	for _, count := range counts {
		byToken[count.TokenID] += count.Count
		if byAmount[count.TokenID] == nil {
			byAmount[count.TokenID] = new(big.Rat)
		}
		addAnomalyAmount(byAmount[count.TokenID], count.Amount)
		if count.TokenName != "" {
			names[count.TokenID] = count.TokenName
		}
	}
	seen := map[int64]bool{}
	for i := range items {
		seen[items[i].TokenID] = true
		items[i].AbnormalRows = byToken[items[i].TokenID]
		items[i].AbnormalAmount = formatAnomalyAmount(byAmount[items[i].TokenID])
	}
	for tokenID, count := range byToken {
		if !seen[tokenID] {
			items = append(items, billing.TokenSummary{TokenID: tokenID, TokenName: names[tokenID], AbnormalRows: count, AbnormalAmount: formatAnomalyAmount(byAmount[tokenID]), CTAmount: "0.000000"})
		}
	}
	return items
}

func applyDetailAnomalyCounts(items []billing.DetailItem, counts []billing.AnomalyCount, userID, channelID int64) []billing.DetailItem {
	remaining := map[string]int64{}
	remainingAmount := map[string]*big.Rat{}
	for _, count := range counts {
		if userID > 0 && count.UserID != userID || channelID > 0 && count.ChannelID != channelID {
			continue
		}
		key := count.Day.Format("2006-01-02") + "\x00" + count.ModelName + "\x00" + count.GroupName
		remaining[key] += count.Count
		if remainingAmount[key] == nil {
			remainingAmount[key] = new(big.Rat)
		}
		addAnomalyAmount(remainingAmount[key], count.Amount)
	}
	for i := range items {
		key := items[i].Day + "\x00" + items[i].ModelName + "\x00" + items[i].GroupName
		if count := remaining[key]; count > 0 {
			items[i].AbnormalRows = count
			items[i].AbnormalAmount = billing.FormatAmount(remainingAmount[key], 6)
			delete(remaining, key)
		}
	}
	for _, count := range counts {
		if userID > 0 && count.UserID != userID || channelID > 0 && count.ChannelID != channelID {
			continue
		}
		key := count.Day.Format("2006-01-02") + "\x00" + count.ModelName + "\x00" + count.GroupName
		if remaining[key] > 0 {
			items = append(items, billing.DetailItem{Day: count.Day.Format("2006-01-02"), ModelName: count.ModelName, GroupName: count.GroupName, AbnormalRows: remaining[key], AbnormalAmount: formatAnomalyAmount(remainingAmount[key]), Amount: "0.000000"})
			delete(remaining, key)
		}
	}
	return items
}
