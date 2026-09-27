package archivereader

import (
	"context"
	"errors"
	"strconv"
	"testing"

	"github.com/go-sql-driver/mysql"
)

func anomalyTestRow(hour int, count string) map[string]any {
	r := map[string]any{"hour": strconv.Itoa(hour)}
	for _, key := range anomalyMetrics {
		r[key] = count
	}
	return r
}

func TestAnomalyPartitionsSplitTimeoutWithoutDuplicates(t *testing.T) {
	var covered int64
	calls := 0
	items, err := aggregateAnomalyDay(context.Background(), 0, func(from, to int64) ([]map[string]any, error) {
		calls++
		if to-from > 900 {
			return nil, &mysql.MySQLError{Number: 3024}
		}
		if from != covered {
			t.Fatalf("gap/overlap %d != %d", from, covered)
		}
		covered = to
		return []map[string]any{anomalyTestRow(int(from/3600), "9007199254740993")}, nil
	})
	if err != nil || covered != 86400 || len(items) != 24 || calls != 24*7 {
		t.Fatalf("%d %d %d %v", covered, len(items), calls, err)
	}
	if items[0]["consumption"] != "36028797018963972" {
		t.Fatal(items[0])
	}
}

func TestAnomalyFailureNeverReturnsPartialTotals(t *testing.T) {
	for _, failure := range []error{&mysql.MySQLError{Number: 1054}, context.DeadlineExceeded} {
		calls := 0
		items, err := aggregateAnomalyDay(context.Background(), 0, func(from, to int64) ([]map[string]any, error) {
			calls++
			if calls > 1 {
				return nil, failure
			}
			return []map[string]any{anomalyTestRow(0, "1")}, nil
		})
		if items != nil || !errors.Is(err, failure) || calls != 2 {
			t.Fatalf("%v %v %d", items, err, calls)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := aggregateAnomalyDay(ctx, 0, func(int64, int64) ([]map[string]any, error) { t.Fatal("queried after cancellation"); return nil, nil }); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}
