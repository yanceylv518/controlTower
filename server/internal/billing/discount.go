package billing

import (
	"errors"
	"time"
)

var ErrDiscountOverlap = errors.New("billing discount effective range overlaps")

const (
	DiscountUserModel       = "user_model"
	DiscountUpstreamChannel = "upstream_channel"
)

type DiscountRule struct {
	ID            int64      `json:"id"`
	InstanceID    string     `json:"instance_id"`
	DiscountType  string     `json:"discount_type"`
	SubjectID     int64      `json:"subject_id"`
	SubjectName   string     `json:"subject_name,omitempty"`
	ChannelID     int64      `json:"channel_id"`
	ChannelName   string     `json:"channel_name,omitempty"`
	ModelName     string     `json:"model_name"`
	Discount      string     `json:"discount"`
	EffectiveFrom time.Time  `json:"effective_from"`
	EffectiveTo   *time.Time `json:"effective_to,omitempty"`
	Remark        string     `json:"remark"`
	CreatedAt     time.Time  `json:"created_at"`
	UpdatedAt     time.Time  `json:"updated_at"`
	UpdatedBy     string     `json:"updated_by"`
}

type StatementDiscount struct {
	DiscountType, ChannelName, ModelName, Discount string
	SubjectID, ChannelID, SourceRuleID             int64
	EffectiveFrom                                  time.Time
	EffectiveTo                                    *time.Time
}

type StatementAggregateRow struct {
	AggregateRow
	ChannelID   int64
	ChannelName string
}

func DiscountForDay(items []StatementDiscount, jobType string, channelID int64, model string, day time.Time) string {
	dayKey := day.In(BusinessLocation).Format("2006-01-02")
	for _, item := range items {
		if jobType == "user_statement" && (item.DiscountType != DiscountUserModel || item.ModelName != model) {
			continue
		}
		if jobType == "upstream_statement" && (item.DiscountType != DiscountUpstreamChannel || item.ChannelID != channelID) {
			continue
		}
		if dayKey < item.EffectiveFrom.Format("2006-01-02") || (item.EffectiveTo != nil && dayKey >= item.EffectiveTo.Format("2006-01-02")) {
			continue
		}
		return item.Discount
	}
	return "1.000000"
}
