package channelcontrol

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

type sequenceProber struct {
	results  []ProbeResult
	calls    int
	cancel   context.CancelFunc
	cancelOn int
}

func (p *sequenceProber) Probe(context.Context, int64, string) (ProbeResult, error) {
	r := p.results[p.calls]
	p.calls++
	if p.cancel != nil && (p.cancelOn == 0 || p.calls == p.cancelOn) {
		p.cancel()
	}
	return r, nil
}

func TestProbeRoundSlowRule(t *testing.T) {
	for _, tc := range []struct {
		name                        string
		results                     []ProbeResult
		attempts, successes, streak int
	}{
		{"two missing responses", []ProbeResult{{Slow: true}, {Slow: true}, {Success: true}}, 2, 0, 2},
		{"successful but slow", []ProbeResult{{Success: true, Duration: 31}, {Success: true, Duration: 32}, {Success: true}}, 2, 0, 2},
		{"earlier success cannot hide slow streak", []ProbeResult{{Success: true, Duration: 1}, {Slow: true}, {Slow: true}, {Success: true}}, 3, 1, 2},
		{"fast success resets streak", []ProbeResult{{Slow: true}, {Success: true, Duration: 1}, {Slow: true}}, 3, 1, 1},
		{"fast failure resets streak", []ProbeResult{{Slow: true}, {Success: false}, {Slow: true}}, 3, 0, 1},
		{"30 seconds returned is accepted", []ProbeResult{{Success: true, Duration: 30}, {Success: true, Duration: 30}}, 2, 2, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := &sequenceProber{results: tc.results}
			r := RunProbeRound(context.Background(), p, 1, "m", len(tc.results), 0, func(context.Context, time.Duration) {})
			if r.Attempts != tc.attempts || r.Successes != tc.successes || r.SlowStreak != tc.streak {
				t.Fatalf("unexpected round: %+v", r)
			}
		})
	}
}

func TestProbeRoundParentCancellationDoesNotDisable(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	p := &sequenceProber{results: []ProbeResult{{Slow: true}}, cancel: cancel}
	r := RunProbeRound(ctx, p, 1, "m", 10, 0, func(context.Context, time.Duration) {})
	if r.SlowStreak != 0 || r.Attempts != 0 || r.Successes != 0 {
		t.Fatalf("parent cancellation became slow evidence: %+v", r)
	}
}

func TestProbeRoundCanceledLastAttemptIsNotACompleteFailure(t *testing.T) {
	for _, count := range []int{1, 10} {
		ctx, cancel := context.WithCancel(context.Background())
		p := &sequenceProber{results: make([]ProbeResult, count), cancel: cancel, cancelOn: count}
		r := RunProbeRound(ctx, p, 1, "m", count, 0, nil)
		cancel()
		if r.Attempts != count-1 || r.Successes != 0 || r.SlowStreak != 0 || r.Error != context.Canceled.Error() {
			t.Fatalf("count=%d: canceled request became complete-failure evidence: %+v", count, r)
		}
	}
}

func TestProbeDeadlineReturnsBeforeUpstreamResponse(t *testing.T) {
	started := make(chan struct{})
	release := make(chan struct{})
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(started)
		<-release // Intentionally withhold even after client cancellation.
		_, _ = w.Write([]byte(`{"success":true,"time":1}`))
	}))
	defer api.Close()
	defer close(release)
	c := New(api.URL, "token", 1, nil)
	done := make(chan ProbeResult, 1)
	go func() { r, _ := c.probe(context.Background(), 1, "m", 50*time.Millisecond); done <- r }()
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("request did not start")
	}
	select {
	case r := <-done:
		if !r.Slow || r.Success {
			t.Fatalf("timeout did not immediately produce slow evidence: %+v", r)
		}
	case <-time.After(time.Second):
		t.Fatal("probe waited for response past its deadline")
	}
}

func TestProbeParentDeadlineAndAuthenticationErrorAreNotSlow(t *testing.T) {
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { <-r.Context().Done() }))
	defer api.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	r, err := New(api.URL, "token", 1, nil).probe(ctx, 1, "m", time.Second)
	if !errors.Is(err, context.DeadlineExceeded) || r.Slow {
		t.Fatalf("parent timeout counted as channel timeout: %+v %v", r, err)
	}
	r, err = New(api.URL, "", 1, nil).probe(context.Background(), 1, "m", time.Second)
	if err == nil || r.Slow {
		t.Fatalf("credential error counted as channel timeout: %+v %v", r, err)
	}
}
