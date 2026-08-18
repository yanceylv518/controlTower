package billing

import (
	"context"
	"fmt"
	"log"
	"time"
)

var BillingPageRetryDelays = []time.Duration{2 * time.Second, 10 * time.Second, 30 * time.Second, 60 * time.Second, 120 * time.Second}
var BillingPageRetryBudget = 10 * time.Minute

func ReadPageWithRetry[T any](ctx context.Context, label string, cursor LogCursor, read func() ([]T, error)) ([]T, error) {
	started := time.Now()
	attempt := 0
	for {
		pageStarted := time.Now()
		rows, err := read()
		elapsed := time.Since(pageStarted)
		if elapsed > 10*time.Second {
			log.Printf("slow billing page %s cursor=%d/%d elapsed=%s", label, cursor.CreatedUnix, cursor.ID, elapsed.Round(time.Millisecond))
		}
		if err == nil {
			return rows, nil
		}
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		delay := BillingPageRetryDelays[len(BillingPageRetryDelays)-1]
		if attempt < len(BillingPageRetryDelays) {
			delay = BillingPageRetryDelays[attempt]
		}
		if time.Since(started)+delay > BillingPageRetryBudget {
			return nil, fmt.Errorf("%s cursor=%d/%d retry budget exhausted: %w", label, cursor.CreatedUnix, cursor.ID, err)
		}
		attempt++
		log.Printf("billing page retry %s cursor=%d/%d attempt=%d delay=%s: %v", label, cursor.CreatedUnix, cursor.ID, attempt, delay, err)
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil, ctx.Err()
		case <-timer.C:
		}
	}
}
