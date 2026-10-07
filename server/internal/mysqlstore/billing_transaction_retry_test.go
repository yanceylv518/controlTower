package mysqlstore

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/go-sql-driver/mysql"
)

func TestBillingDeadlockRetry(t *testing.T) {
	deadlock := fmt.Errorf("publication: %w", &mysql.MySQLError{Number: 1213})
	for _, tc := range []struct {
		name     string
		failures int
		cause    error
		want     int
	}{
		{"recovers", 2, deadlock, 3},
		{"bounded", 10, deadlock, 3},
		{"lock timeout is not deadlock", 10, &mysql.MySQLError{Number: 1205}, 1},
		{"ambiguous error is not replayed", 10, errors.New("connection lost"), 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			err := retryBillingDeadlock(context.Background(), func() error {
				calls++
				if calls <= tc.failures {
					return tc.cause
				}
				return nil
			})
			if calls != tc.want || (err == nil) != (calls > tc.failures) {
				t.Fatalf("calls=%d error=%v", calls, err)
			}
		})
	}
	ctx, cancel := context.WithCancel(context.Background())
	calls := 0
	err := retryBillingDeadlock(ctx, func() error { calls++; cancel(); return deadlock })
	if !errors.Is(err, context.Canceled) || calls != 1 {
		t.Fatalf("cancellation: calls=%d error=%v", calls, err)
	}
}
