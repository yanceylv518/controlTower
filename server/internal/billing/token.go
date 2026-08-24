package billing

type TokenSummary struct {
	TokenID          int64  `json:"token_id"`
	TokenName        string `json:"token_name"`
	RequestCount     int64  `json:"request_count"`
	PromptTokens     int64  `json:"prompt_tokens"`
	CompletionTokens int64  `json:"completion_tokens"`
	CacheTokens      int64  `json:"cache_tokens"`
	CacheWriteTokens int64  `json:"cache_write_tokens"`
	Quota            int64  `json:"quota"`
	BillingAmount    string `json:"billing_amount"`
	AbnormalRows     int64  `json:"abnormal_rows"`
	AbnormalAmount   string `json:"abnormal_amount"`
}

func TokenRowsAsAggregates(rows []TokenDailyRow) []AggregateRow {
	out := make([]AggregateRow, 0, len(rows))
	for _, v := range rows {
		out = append(out, AggregateRow{InstanceID: v.InstanceID, UserID: v.UserID, Username: v.Username, ModelName: v.ModelName, GroupName: v.GroupName, TierFrom: v.TierFrom, Day: v.Day, RequestCount: v.RequestCount, PromptTokens: v.PromptTokens, CompletionTokens: v.CompletionTokens, CacheTokens: v.CacheTokens, CacheWriteTokens: v.CacheWriteTokens, CacheWrite5mTokens: v.CacheWrite5mTokens, CacheWrite1hTokens: v.CacheWrite1hTokens, Quota: v.Quota})
	}
	return out
}

func BuildTokenSummaries(rows []TokenDailyRow, prices []PriceRecord, ratios []GroupRatio, snapshots map[string]string) []TokenSummary {
	byToken := map[int64][]AggregateRow{}
	names := map[int64]string{}
	for _, v := range rows {
		byToken[v.TokenID] = append(byToken[v.TokenID], TokenRowsAsAggregates([]TokenDailyRow{v})...)
		if v.TokenName != "" {
			names[v.TokenID] = v.TokenName
		}
	}
	out := make([]TokenSummary, 0, len(byToken))
	for tokenID, tokenRows := range byToken {
		_, total := BuildSummary(tokenRows, prices, ratios, snapshots, nil)
		out = append(out, TokenSummary{TokenID: tokenID, TokenName: names[tokenID], RequestCount: total.RequestCount, PromptTokens: total.PromptTokens, CompletionTokens: total.CompletionTokens, CacheTokens: total.CacheTokens, CacheWriteTokens: total.CacheWriteTokens, Quota: total.Quota, BillingAmount: total.Amount})
	}
	sortTokenSummaries(out)
	return out
}

func sortTokenSummaries(items []TokenSummary) {
	for i := 0; i < len(items); i++ {
		for j := i + 1; j < len(items); j++ {
			a, _ := decimalRat(items[i].BillingAmount)
			b, _ := decimalRat(items[j].BillingAmount)
			if b.Cmp(a) > 0 || (b.Cmp(a) == 0 && items[j].TokenID < items[i].TokenID) {
				items[i], items[j] = items[j], items[i]
			}
		}
	}
}
