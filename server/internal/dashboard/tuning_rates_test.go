package dashboard

import (
	"controltower/server/internal/tuning"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

type liveRatesStub struct{ tuningStub }

func (*liveRatesStub) QueryCurrentChannelRates(string, time.Time) ([]tuning.ChannelMetric, error) {
	return []tuning.ChannelMetric{{ChannelID: 7, RequestCount: 12, TPM: 3456}}, nil
}
func TestCurrentRatesIndependentOfEvaluation(t *testing.T) {
	h := Handler{}.WithTuningStore(&liveRatesStub{})
	rr := httptest.NewRecorder()
	h.HandleTuningContinuousStates(rr, httptest.NewRequest("GET", "/api/dashboard/tuning/continuous-states?site_id=s&rates_only=1", nil))
	if rr.Code != 200 || !strings.Contains(rr.Body.String(), `"tpm":3456`) || !strings.Contains(rr.Body.String(), `"window_seconds":60`) {
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
