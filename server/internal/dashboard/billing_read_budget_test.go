package dashboard

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestBillingReadBudgetSerializesSameSiteAndCancelsWait(t *testing.T) {
	b := billingReadBudget{}
	release, e := b.acquire(context.Background(), "a")
	if e != nil {
		t.Fatal(e)
	}
	other, e := b.acquire(context.Background(), "b")
	if e != nil {
		t.Fatal(e)
	}
	other()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if _, e = b.acquire(ctx, "a"); !errors.Is(e, context.DeadlineExceeded) {
		t.Fatal(e)
	}
	release()
	next, e := b.acquire(context.Background(), "a")
	if e != nil {
		t.Fatal(e)
	}
	next()
}
func TestBillingReadBudgetCooldownCanBeCancelled(t *testing.T) {
	b := billingReadBudget{pause: time.Hour}
	release, e := b.acquire(context.Background(), "a")
	if e != nil {
		t.Fatal(e)
	}
	release()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if _, e = b.acquire(ctx, "a"); !errors.Is(e, context.DeadlineExceeded) {
		t.Fatal(e)
	}
	b.mu.Lock()
	b.sites["a"].next = time.Time{}
	b.mu.Unlock()
	next, e := b.acquire(context.Background(), "a")
	if e != nil {
		t.Fatal(e)
	}
	next()
}
