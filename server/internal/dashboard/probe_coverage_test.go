package dashboard

import (
	"context"
	"encoding/json"
	"errors"
	"net/http/httptest"
	"testing"
	"time"

	"controltower/server/internal/tuning"
)

type coverageReadStub struct {
	*tuningStub
	from, to time.Time
	site     string
	err      error
	calls    int
}

func (s *coverageReadStub) QueryRoutineProbeCoverage(_ context.Context, site string, from, to time.Time) (map[int64]tuning.RoutineProbeCoverage, time.Time, error) {
	s.calls++
	s.site, s.from, s.to = site, from, to
	return map[int64]tuning.RoutineProbeCoverage{7: {Scheduled: tuning.ProbeDispatchCounts{Due: 6, Sent: 5, GlobalLimited: 1}}}, from.Add(time.Second), s.err
}

func TestProbeCoverageUsesPolicyWindowAndKeepsStateOnQueryFailure(t *testing.T) {
	s := &coverageReadStub{tuningStub: &tuningStub{policyExists: true, policy: tuning.PolicyRecord{Policy: tuning.DefaultPolicy()}}}
	s.policy.Policy.Continuous.WindowMinutes = 7
	h := NewHandler(nil).WithTuningStore(s)
	read := func() tuning.ProbeCoverageWindow {
		t.Helper()
		w := httptest.NewRecorder()
		h.HandleTuningContinuousStates(w, httptest.NewRequest("GET", "/api/dashboard/tuning/continuous-states?site_id=site-a", nil))
		if w.Code != 200 {
			t.Fatalf("status %d: %s", w.Code, w.Body.String())
		}
		var body struct {
			Coverage tuning.ProbeCoverageWindow `json:"probe_coverage"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
			t.Fatal(err)
		}
		return body.Coverage
	}
	coverage := read()
	if s.site != "site-a" || s.to.Sub(s.from) != 7*time.Minute || s.from.Second() != 0 || !s.to.After(time.Now().UTC()) || coverage.Items[7].Scheduled.GlobalLimited != 1 {
		t.Fatalf("invalid window/coverage: %+v", coverage)
	}
	s.err = errors.New("private database details")
	coverage = read()
	if coverage.Error != "probe_coverage_unavailable" || len(coverage.Items) != 0 {
		t.Fatalf("error appeared healthy: %+v", coverage)
	}
	// The hot RPM/TPM path must not add a coverage query.
	w := httptest.NewRecorder()
	h.HandleTuningContinuousStates(w, httptest.NewRequest("GET", "/api/dashboard/tuning/continuous-states?site_id=site-a&rates_only=1", nil))
	if s.calls != 2 {
		t.Fatal("rates-only requested coverage")
	}
}
