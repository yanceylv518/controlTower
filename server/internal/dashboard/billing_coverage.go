package dashboard

import (
	"context"
	"time"
)

type billingDayStatusReader interface {
	ListCompleteBillingDays(context.Context, string, time.Time, time.Time) ([]time.Time, error)
}

func billingCoverage(ctx context.Context, store any, site string, from, to time.Time) map[string]any {
	expected := int(to.Sub(from).Hours() / 24)
	result := map[string]any{"expected_days": expected, "available_days": 0, "missing_days": []string{}, "status": "missing"}
	reader, ok := store.(billingDayStatusReader)
	if !ok {
		return result
	}
	days, err := reader.ListCompleteBillingDays(ctx, site, from, to)
	if err != nil {
		result["status"] = "unknown"
		return result
	}
	available := map[string]bool{}
	for _, day := range days {
		available[day.Format("2006-01-02")] = true
	}
	missing := []string{}
	for day := from; day.Before(to); day = day.AddDate(0, 0, 1) {
		if !available[day.Format("2006-01-02")] {
			missing = append(missing, day.Format("2006-01-02"))
		}
	}
	result["available_days"], result["missing_days"] = len(available), missing
	if len(missing) == 0 {
		result["status"] = "complete"
	} else if len(available) > 0 {
		result["status"] = "partial"
	}
	return result
}
