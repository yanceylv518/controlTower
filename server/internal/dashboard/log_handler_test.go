package dashboard

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"controltower/server/internal/storage"
)

type staticLogSource struct {
	events []storage.LogEvent
}

func (s staticLogSource) Logs() ([]storage.LogEvent, error) {
	return s.events, nil
}

func TestHandleLogsParsesFiltersAndReturnsSummaries(t *testing.T) {
	base := time.Date(2026, 7, 2, 14, 0, 0, 0, time.UTC)
	handler := NewHandler(staticOverviewSource{}).WithLogSource(staticLogSource{
		events: []storage.LogEvent{
			{
				InstanceID:  "inst-1",
				SourceLogID: 10,
				CreatedAt:   base,
				LogType:     "consume",
				UserID:      7,
				ChannelID:   18,
				ModelName:   "gpt-4o",
				RequestID:   "req-10",
			},
			{
				InstanceID:  "inst-1",
				SourceLogID: 11,
				CreatedAt:   base.Add(time.Minute),
				LogType:     "error",
				UserID:      7,
				ChannelID:   19,
				ModelName:   "gpt-4o",
				RequestID:   "req-11",
			},
		},
	})

	req := httptest.NewRequest(http.MethodGet, "/api/dashboard/logs?instance_id=inst-1&log_type=error&limit=10", nil)
	rr := httptest.NewRecorder()
	handler.HandleLogs(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", rr.Code, rr.Body.String())
	}

	var response LogListResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(response.Items) != 1 {
		t.Fatalf("expected one log item, got %#v", response.Items)
	}
	if response.Items[0].SourceLogID != 11 || response.Items[0].RequestID != "req-11" {
		t.Fatalf("unexpected item: %#v", response.Items[0])
	}
}

func TestHandleLogsRejectsNonGET(t *testing.T) {
	handler := NewHandler(staticOverviewSource{}).WithLogSource(staticLogSource{})
	req := httptest.NewRequest(http.MethodPost, "/api/dashboard/logs", nil)
	rr := httptest.NewRecorder()
	handler.HandleLogs(rr, req)

	if rr.Code != http.StatusMethodNotAllowed {
		t.Fatalf("expected 405, got %d", rr.Code)
	}
}

func TestHandleLogsRequiresLogSource(t *testing.T) {
	handler := NewHandler(staticOverviewSource{})
	req := httptest.NewRequest(http.MethodGet, "/api/dashboard/logs", nil)
	rr := httptest.NewRecorder()
	handler.HandleLogs(rr, req)

	if rr.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500, got %d", rr.Code)
	}
}

func TestHandleLogsQueriesLogStoreWithParsedFilter(t *testing.T) {
	base := time.Date(2026, 7, 2, 14, 0, 0, 0, time.UTC)
	store := capturingLogStore{
		events: []storage.LogEvent{
			{InstanceID: "inst-1", SourceLogID: 12, CreatedAt: base, LogType: "error", UserID: 7, ChannelID: 18, ModelName: "gpt-4o", RequestID: "req-12"},
		},
	}
	handler := NewHandler(staticOverviewSource{}).WithLogStore(&store)

	req := httptest.NewRequest(http.MethodGet, "/api/dashboard/logs?instance_id=inst-1&user_id=7&channel_id=18&model_name=gpt-4o&log_type=error&request_id=req-12&start_time=2026-07-02T13:00:00Z&end_time=2026-07-02T15:00:00Z&limit=25&offset=5", nil)
	rr := httptest.NewRecorder()
	handler.HandleLogs(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", rr.Code, rr.Body.String())
	}
	if store.query.InstanceID != "inst-1" || store.query.UserID != 7 || store.query.ChannelID != 18 || store.query.ModelName != "gpt-4o" || store.query.LogType != "error" || store.query.RequestID != "req-12" {
		t.Fatalf("query not parsed: %#v", store.query)
	}
	if store.query.Limit != 25 || store.query.Offset != 5 {
		t.Fatalf("pagination not parsed: %#v", store.query)
	}
	if store.query.StartTime.IsZero() || store.query.EndTime.IsZero() {
		t.Fatalf("time range not parsed: %#v", store.query)
	}
}

type capturingLogStore struct {
	query  storage.LogQuery
	events []storage.LogEvent
}

func (s *capturingLogStore) QueryLogEvents(query storage.LogQuery) ([]storage.LogEvent, error) {
	s.query = query
	return s.events, nil
}
