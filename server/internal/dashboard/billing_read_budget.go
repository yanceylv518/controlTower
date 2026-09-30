package dashboard

import (
	"context"
	"sync"
	"time"
)

// Billing/report log scans in this server share one slot per site, including
// readers using separate SQL pools. Cancellation while waiting never issues SQL.
type billingReadBudget struct {
	mu    sync.Mutex
	pause time.Duration
	sites map[string]*billingReadSlot
}
type billingReadSlot struct {
	token chan struct{}
	next  time.Time
}

var sourceBillingBudget = billingReadBudget{pause: 500 * time.Millisecond}

func SetBillingSourceReadPause(pause time.Duration) {
	sourceBillingBudget.mu.Lock()
	defer sourceBillingBudget.mu.Unlock()
	sourceBillingBudget.pause = pause
}
func (b *billingReadBudget) acquire(ctx context.Context, site string) (func(), error) {
	b.mu.Lock()
	if b.sites == nil {
		b.sites = map[string]*billingReadSlot{}
	}
	slot := b.sites[site]
	if slot == nil {
		slot = &billingReadSlot{token: make(chan struct{}, 1)}
		b.sites[site] = slot
	}
	pause := b.pause
	b.mu.Unlock()
	select {
	case slot.token <- struct{}{}:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	if delay := time.Until(slot.next); delay > 0 {
		timer := time.NewTimer(delay)
		select {
		case <-timer.C:
		case <-ctx.Done():
			timer.Stop()
			<-slot.token
			return nil, ctx.Err()
		}
	}
	if err := ctx.Err(); err != nil {
		<-slot.token
		return nil, err
	}
	return func() { slot.next = time.Now().Add(pause); <-slot.token }, nil
}
