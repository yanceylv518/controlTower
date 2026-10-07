package mysqlstore

import (
	"context"
	"errors"
	"time"

	"github.com/go-sql-driver/mysql"
)

// The callback must own the entire transaction and roll it back before returning.
// Retry only confirmed deadlocks; ambiguous commit/network errors are not safe
// to replay. There are at most three attempts, including the initial attempt.
func retryBillingDeadlock(ctx context.Context, attempt func() error) error {
	for n := 0; ; n++ {
		if err := ctx.Err(); err != nil {
			return err
		}
		err := attempt()
		var deadlock *mysql.MySQLError
		if n >= 2 || !errors.As(err, &deadlock) || deadlock.Number != 1213 {
			return err
		}
		timer := time.NewTimer(time.Duration(n+1) * 50 * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
	}
}
