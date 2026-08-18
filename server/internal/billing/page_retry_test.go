package billing

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestReadPageWithRetryRecoversAfterTwoFailures(t *testing.T) {
	oldD, oldB := BillingPageRetryDelays, BillingPageRetryBudget
	BillingPageRetryDelays = []time.Duration{time.Millisecond}
	BillingPageRetryBudget = time.Second
	defer func() { BillingPageRetryDelays, BillingPageRetryBudget = oldD, oldB }()
	attempts := 0
	rows, err := ReadPageWithRetry(context.Background(), "test", LogCursor{ID: 9}, func() ([]int, error) {
		attempts++
		if attempts < 3 {
			return nil, errors.New("temporary")
		}
		return []int{1}, nil
	})
	if err != nil || len(rows) != 1 || attempts != 3 {
		t.Fatalf("rows=%v attempts=%d err=%v", rows, attempts, err)
	}
}
func TestReadPageWithRetryBudgetIncludesCursor(t *testing.T) {
	oldD, oldB := BillingPageRetryDelays, BillingPageRetryBudget
	BillingPageRetryDelays = []time.Duration{time.Second}
	BillingPageRetryBudget = time.Millisecond
	defer func() { BillingPageRetryDelays, BillingPageRetryBudget = oldD, oldB }()
	_, err := ReadPageWithRetry(context.Background(), "page=4", LogCursor{CreatedUnix: 7, ID: 8}, func() ([]int, error) { return nil, errors.New("down") })
	if err == nil || !strings.Contains(err.Error(), "cursor=7/8") {
		t.Fatalf("err=%v", err)
	}
}
func TestReadPageWithRetryStopsOnCancel(t *testing.T) {
	oldD, oldB := BillingPageRetryDelays, BillingPageRetryBudget
	BillingPageRetryDelays = []time.Duration{time.Hour}
	BillingPageRetryBudget = 2 * time.Hour
	defer func() { BillingPageRetryDelays, BillingPageRetryBudget = oldD, oldB }()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	attempts := 0
	_, err := ReadPageWithRetry(ctx, "cancel", LogCursor{}, func() ([]int, error) { attempts++; return nil, errors.New("down") })
	if !errors.Is(err, context.Canceled) || attempts != 1 {
		t.Fatalf("attempts=%d err=%v", attempts, err)
	}
}
