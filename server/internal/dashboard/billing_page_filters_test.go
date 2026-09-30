package dashboard

import (
	"context"
	"controltower/server/internal/billing"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestBillingPageRangeDoesNotLoseSameSecondRows(t *testing.T) {
	start, end := time.Unix(100, 0), time.Unix(200, 0)
	for _, cursor := range []billing.LogCursor{{}, {CreatedUnix: 99, ID: 9}, {CreatedUnix: 100, ID: 9}, {CreatedUnix: 150, ID: 9}, {CreatedUnix: 200, ID: 9}} {
		args := billingPageRangeArgs(start, end, cursor)
		lower := args[0].(int64)
		for at := int64(95); at <= 205; at++ {
			for id := int64(1); id <= 20; id++ {
				after := at > cursor.CreatedUnix || at == cursor.CreatedUnix && id > cursor.ID
				if (at >= 100 && at < 200 && after) != (at >= lower && at < 200 && after) {
					t.Fatalf("changed inclusion at %d/%d cursor=%+v", at, id, cursor)
				}
			}
		}
	}
}

func TestBillingModelFilterFallsBackWithoutWideningChannelScope(t *testing.T) {
	for _, models := range []map[int64][]string{nil, {2: {"outside-binding"}}, {1: make([]string, 1001)}} {
		q, args := billingChannelsModelsPageQuery([]int64{1}, models)
		if q != billingChannelsLogsPageQuery(1) || !reflect.DeepEqual(args, []any{int64(1)}) {
			t.Fatal(q, args)
		}
	}
	q, args := billingChannelsModelsPageQuery([]int64{1}, map[int64][]string{1: {"' OR 1=1 --", "m "}, 2: {"outside"}})
	if strings.Contains(q, "OR 1=1") || strings.Contains(q, "outside") || len(args) != 4 {
		t.Fatal(q, args)
	}
	// A missing binding exits without even opening the source database.
	h := &PassthroughHandler{}
	rows, err := h.DetailedChannelsModelsLogsPageForBilling(context.Background(), "site", []int64{0, -1}, map[int64][]string{2: {"m"}}, time.Now(), time.Now(), billing.LogCursor{}, 3)
	if err != nil || len(rows) != 0 {
		t.Fatal(rows, err)
	}
}
