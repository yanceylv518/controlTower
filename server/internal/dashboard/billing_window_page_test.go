package dashboard

import (
	"context"
	"controltower/server/internal/billing"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/go-sql-driver/mysql"
)

func TestBillingWindowPagesPreserveSparseAndSameSecondRows(t *testing.T) {
	input := []billing.PagedLogRecord{{ID: 1, CreatedUnix: 100}, {ID: 2, CreatedUnix: 100}, {ID: 3, CreatedUnix: 999}, {ID: 4, CreatedUnix: 1000}, {ID: 5, CreatedUnix: 4000}, {ID: 6, CreatedUnix: 4000}, {ID: 7, CreatedUnix: 9999}}
	for _, timeout := range []bool{false, true} {
		var cursor billing.LogCursor
		var got []int64
		calls := 0
		for {
			page, err := billingWindowPage(context.Background(), time.Unix(100, 0), time.Unix(5000, 0), cursor, 2, func(from, to time.Time, c billing.LogCursor, n int) ([]billing.PagedLogRecord, error) {
				calls++
				if calls > 300 {
					t.Fatal("pagination did not terminate")
				}
				if to.Sub(from) > 15*time.Minute {
					t.Fatal("unbounded scan")
				}
				if timeout && to.Sub(from) > 200*time.Second {
					return nil, &mysql.MySQLError{Number: 3024}
				}
				var out []billing.PagedLogRecord
				for _, v := range input {
					if v.CreatedUnix >= from.Unix() && v.CreatedUnix < to.Unix() && (v.CreatedUnix > c.CreatedUnix || v.CreatedUnix == c.CreatedUnix && v.ID > c.ID) {
						out = append(out, v)
						if len(out) == n {
							break
						}
					}
				}
				return out, nil
			})
			if err != nil {
				t.Fatal(err)
			}
			for _, v := range page {
				got = append(got, v.ID)
				cursor = billing.LogCursor{CreatedUnix: v.CreatedUnix, ID: v.ID}
			}
			if len(page) < 2 {
				break
			}
		}
		if !reflect.DeepEqual(got, []int64{1, 2, 3, 4, 5, 6}) {
			t.Fatal(timeout, got)
		}
	}
}

func TestBillingWindowTimeoutStopsAtMinimumWithoutSkipping(t *testing.T) {
	calls := 0
	_, err := billingWindowPage(context.Background(), time.Unix(100, 0), time.Unix(5000, 0), billing.LogCursor{}, 2, func(from, to time.Time, _ billing.LogCursor, _ int) ([]billing.PagedLogRecord, error) {
		calls++
		if from.Unix() != 100 {
			t.Fatal("advanced on timeout")
		}
		return nil, &mysql.MySQLError{Number: 3024}
	})
	var permanent billing.PermanentPageError
	if !errors.As(err, &permanent) || calls > 11 {
		t.Fatal(calls, err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = billingWindowPage(ctx, time.Unix(100, 0), time.Unix(5000, 0), billing.LogCursor{}, 2, func(time.Time, time.Time, billing.LogCursor, int) ([]billing.PagedLogRecord, error) {
		t.Fatal("read after cancel")
		return nil, nil
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}

func TestBillingWindowRecoversAfterSlowSegment(t *testing.T) {
	calls := 0
	_, err := billingWindowPage(context.Background(), time.Unix(100, 0), time.Unix(86500, 0), billing.LogCursor{}, 2, func(from, to time.Time, _ billing.LogCursor, _ int) ([]billing.PagedLogRecord, error) {
		calls++
		if calls > 150 {
			t.Fatal("tiny window persisted across empty day")
		}
		if from.Unix() == 100 && to.Unix()-from.Unix() > 1 {
			return nil, &mysql.MySQLError{Number: 3024}
		}
		return nil, nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
