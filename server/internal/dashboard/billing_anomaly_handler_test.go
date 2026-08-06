package dashboard

import (
	"context"
	"controltower/server/internal/billing"
	"encoding/csv"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

type anomalyExportStore struct {
	items []billing.AnomalyOrder
}

func (s anomalyExportStore) QueryBillingAnomalies(context.Context, string, int64, int64, time.Time, time.Time, time.Time, int64, int) ([]billing.AnomalyOrder, error) {
	return s.items, nil
}

func TestBillingAnomalyCSVUsesBusinessTimezone(t *testing.T) {
	store := anomalyExportStore{items: []billing.AnomalyOrder{{
		ModelName:   "glm-5.2",
		RequestID:   "req-1",
		SourceLogID: 1,
		CreatedAt:   time.Date(2026, 6, 30, 16, 30, 0, 0, time.UTC),
	}}}
	req := httptest.NewRequest("GET", "/?instance_id=demo&from=2026-07-01+00%3A00%3A00&to=2026-08-01+00%3A00%3A00&format=csv", nil)
	rec := httptest.NewRecorder()
	BillingAnomalyHandler{Store: store}.ServeHTTP(rec, req)

	body := strings.TrimPrefix(rec.Body.String(), "\ufeff")
	records, err := csv.NewReader(strings.NewReader(body)).ReadAll()
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 2 {
		t.Fatalf("expected header and one item, got %d rows", len(records))
	}
	if got := records[1][3]; got != "2026/07/01 00:30:00" {
		t.Fatalf("expected business-local request time, got %q", got)
	}
}
