package dashboard

import (
	"context"
	"controltower/server/internal/billing"
	"time"
)

func captureBillingMoney(ctx context.Context, source BillingRatioSource, site string) (*billing.MoneySnapshot, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	var raw string
	var err error
	if narrow, ok := source.(interface {
		MoneyOptionsSnapshot(context.Context, string) (string, error)
	}); ok {
		raw, err = narrow.MoneyOptionsSnapshot(ctx, site)
	} else {
		raw, err = source.RatioSnapshot(ctx, site)
	}
	if err != nil {
		return nil, err
	}
	return billing.NewMoneySnapshot(site, raw, time.Now())
}
