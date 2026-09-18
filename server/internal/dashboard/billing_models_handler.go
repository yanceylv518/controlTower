package dashboard

import (
	"context"
	"controltower/server/internal/billing"
	"time"
)

type BillingModelSource interface {
	BillingRatioSource
	ConfiguredModels(context.Context, string) ([]string, error)
}

func billingSyncDay(now time.Time) time.Time {
	y, m, d := now.In(billing.BusinessLocation).Date()
	return time.Date(y, m, d, 0, 0, 0, 0, billing.BusinessLocation)
}
