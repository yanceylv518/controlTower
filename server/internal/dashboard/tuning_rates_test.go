package dashboard

import (
	"controltower/server/internal/tuning"
	"encoding/json"
	"errors"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

type liveRatesStub struct{ tuningStub }

func (*liveRatesStub) QueryCurrentChannelRateSnapshot(_ string, now time.Time) ([]tuning.ChannelMetric, time.Time, error) {
	return []tuning.ChannelMetric{{ChannelID: 7, RequestCount: 12, TPM: 3456}}, now.Add(-20 * time.Second), nil
}
func TestCurrentRatesIndependentOfEvaluation(t *testing.T) {
	h := Handler{}.WithTuningStore(&liveRatesStub{})
	rr := httptest.NewRecorder()
	h.HandleTuningContinuousStates(rr, httptest.NewRequest("GET", "/api/dashboard/tuning/continuous-states?site_id=s&rates_only=1", nil))
	if rr.Code != 200 || !strings.Contains(rr.Body.String(), `"tpm":3456`) || !strings.Contains(rr.Body.String(), `"window_seconds":60`) {
		t.Fatal(rr.Code, rr.Body.String())
	}
	var payload struct {
		AsOf  time.Time `json:"as_of"`
		Start time.Time `json:"window_start"`
		Delay int64     `json:"delay_seconds"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if payload.Delay != 20 || payload.AsOf.Sub(payload.Start) != time.Minute {
		t.Fatalf("incorrect window metadata: %+v", payload)
	}
}

type staleRatesStub struct{ tuningStub }

func (*staleRatesStub) QueryCurrentChannelRateSnapshot(string, time.Time) ([]tuning.ChannelMetric, time.Time, error) {
	return nil, time.Time{}, errors.New("coverage expired")
}
func TestCurrentRatesExpiredCoverageReturnsUnavailable(t *testing.T) {
	h := Handler{}.WithTuningStore(&staleRatesStub{})
	rr := httptest.NewRecorder()
	h.HandleTuningContinuousStates(rr, httptest.NewRequest("GET", "/api/dashboard/tuning/continuous-states?site_id=s&rates_only=1", nil))
	if rr.Code != 503 {
		t.Fatal(rr.Code, rr.Body.String())
	}
}
func TestCurrentRatesDoesNotPretendOldAgentIsZero(t *testing.T) {
	h := Handler{}.WithTuningStore(&tuningStub{})
	rr := httptest.NewRecorder()
	h.HandleTuningContinuousStates(rr, httptest.NewRequest("GET", "/api/dashboard/tuning/continuous-states?site_id=s&rates_only=1", nil))
	if rr.Code != 503 {
		t.Fatal(rr.Code, rr.Body.String())
	}
}
