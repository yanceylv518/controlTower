package billing

import (
	"math/big"
	"time"
)

type ChannelSetting struct {
	InstanceID string    `json:"instance_id"`
	ChannelID  int64     `json:"channel_id"`
	Discount   string    `json:"discount"`
	UpdatedAt  time.Time `json:"updated_at"`
	UpdatedBy  string    `json:"updated_by"`
}
type ConfiguredChannel struct {
	ChannelID   int64  `json:"channel_id"`
	ChannelName string `json:"channel_name"`
	Status      int    `json:"status"`
}
type ChannelSummary struct {
	ChannelID        int64    `json:"channel_id"`
	ChannelName      string   `json:"channel_name"`
	RequestCount     int64    `json:"request_count"`
	PromptTokens     int64    `json:"prompt_tokens"`
	CompletionTokens int64    `json:"completion_tokens"`
	CacheTokens      int64    `json:"cache_tokens"`
	CacheWriteTokens int64    `json:"cache_write_tokens"`
	AbnormalRows     int64    `json:"abnormal_rows"`
	Quota            int64    `json:"quota"`
	Amount           string   `json:"amount"`
	Discount         string   `json:"discount"`
	DiscountedAmount string   `json:"discounted_amount"`
	UnpricedModels   []string `json:"unpriced_models"`
}

func BuildChannelSummary(rows []AggregateRow, prices []PriceRecord, ratios []GroupRatio, snapshots map[string]string, settings map[int64]ChannelSetting) []ChannelSummary {
	users, _ := BuildSummary(rows, prices, ratios, snapshots, nil)
	out := make([]ChannelSummary, 0, len(users))
	for _, v := range users {
		discount := "1"
		if s, ok := settings[v.UserID]; ok && s.Discount != "" {
			discount = s.Discount
		}
		amount, _ := decimalRat(v.Amount)
		d, _ := decimalRat(discount)
		discounted := new(big.Rat).Mul(amount, d)
		out = append(out, ChannelSummary{ChannelID: v.UserID, ChannelName: v.Username, RequestCount: v.RequestCount, PromptTokens: v.PromptTokens, CompletionTokens: v.CompletionTokens, CacheTokens: v.CacheTokens, CacheWriteTokens: v.CacheWriteTokens, Quota: v.Quota, Amount: v.Amount, Discount: discount, DiscountedAmount: FormatAmount(discounted, 6), UnpricedModels: v.UnpricedModels})
	}
	return out
}
