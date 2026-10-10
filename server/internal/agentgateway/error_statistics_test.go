package agentgateway

import (
	"bytes"
	"context"
	es "controltower/internal/errorstats"
	"encoding/json"
	"net/http/httptest"
	"testing"
	"time"
)

type statisticsSink struct {
	memorySink
	calls int
}

func (s *statisticsSink) SaveErrorStatistics(_ context.Context, b es.Batch) error {
	s.calls++
	return nil
}

type statisticsTokenLookup struct{}

func (statisticsTokenLookup) InstanceIDByTokenHash(string, time.Time) (string, bool, error) {
	return "allowed", true, nil
}
func TestStatisticsGatewayValidation(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Minute)
	for _, tc := range []struct {
		instance, token string
		count           int64
		status          int
	}{{"allowed", "token", 1, 200}, {"other", "token", 1, 403}, {"allowed", "", 1, 401}, {"allowed", "token", -1, 400}} {
		sink := &statisticsSink{}
		h := NewHandlerWithTokens("", sink, statisticsTokenLookup{}, "pepper")
		b := es.Batch{InstanceID: tc.instance, ID: "batch", StartedAt: now, ObservedAt: now, Rows: []es.Row{{Minute: now, Code: "http:429", Count: tc.count}}}
		raw, _ := json.Marshal(b)
		r := httptest.NewRequest("POST", "/api/agent/error-statistics", bytes.NewReader(raw))
		if tc.token != "" {
			r.Header.Set("Authorization", "Bearer "+tc.token)
		}
		w := httptest.NewRecorder()
		h.HandleErrorStatistics(w, r)
		if w.Code != tc.status {
			t.Fatalf("%+v => %d %s", tc, w.Code, w.Body.String())
		}
		if (sink.calls == 1) != (tc.status == 200) {
			t.Fatal("rejected payload reached store")
		}
	}
}
