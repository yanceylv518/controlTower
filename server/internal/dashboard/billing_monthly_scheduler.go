package dashboard

import (
	"context"
	"controltower/server/internal/billing"
	"errors"
	"fmt"
	"log"
	"time"
)

// FillMonthly runs under the worker's site lease, so enqueue cannot starve
// behind the worker itself. It deliberately avoids Fill's source metadata,
// day queue and in-process producer gate. Daily snapshots are its only input.
func (a BillingAutomation) FillMonthly(ctx context.Context, site string) error {
	store, ok := a.Store.(interface {
		MissingBillingMonths(context.Context, billing.AutomaticTarget, time.Time) ([]time.Time, error)
	})
	if !ok {
		return nil
	}
	targets, err := a.Store.ListBillingAutomaticTargets(ctx)
	if err != nil {
		return err
	}
	for _, target := range targets {
		if target.InstanceID != site {
			continue
		}
		end := billing.CompleteDayBoundary(time.Now())
		if !target.To.IsZero() && target.To.Before(end) {
			end = target.To
		}
		if !target.ProgressUntil.IsZero() && target.ProgressUntil.Before(end) {
			end = target.ProgressUntil
		}
		months, err := store.MissingBillingMonths(ctx, target, end)
		if err != nil {
			return err
		}
		if len(months) == 0 {
			continue
		}
		source, e := a.Store.BillingDataSource(ctx)
		if e != nil {
			return e
		}
		for _, month := range months {
			err = a.enqueue(ctx, target, month, month.AddDate(0, 1, 0), "monthly", source, func() (*billing.MoneySnapshot, error) {
				return nil, fmt.Errorf("monthly scheduler must not read source options")
			})
			if errors.Is(err, billing.ErrDailyBillsIncomplete) || errors.Is(err, billing.ErrStatementNoData) {
				continue
			}
			if err != nil {
				// A broken subject must not block independent monthly summaries.
				log.Printf("billing monthly enqueue site=%s subject=%d: %v", site, target.SubjectID, err)
				if recorder, ok := a.Store.(interface {
					RecordBillingGenerationAttempt(context.Context, billing.AutomaticTarget, error) error
				}); ok {
					_ = recorder.RecordBillingGenerationAttempt(ctx, target, err)
				}
			}
		}
	}
	return ctx.Err()
}
