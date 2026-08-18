package dashboard

import (
	"context"
	"testing"
	"time"

	"controltower/server/internal/billing"
	"controltower/server/internal/xlsxwriter"
)

func TestBillingWorkbookUsesBoundedRequestPages(t *testing.T) {
	seenLimit := 0
	err := writeRequestPages(context.Background(), xlsxwriter.New(), "period", time.Time{}, time.Time{}, nil, nil, nil, func(_ billing.LogCursor, limit int) ([]billing.PagedLogRecord, error) {
		seenLimit = limit
		return nil, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if seenLimit != 500 {
		t.Fatalf("workbook request page limit=%d", seenLimit)
	}
}
