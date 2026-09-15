package voicealert

import (
	"context"
	"errors"
	"testing"
	"time"
)

type fakeRepository struct {
	config      Config
	coverageErr error
	claimErr    error
	claimed     bool
	finishes    int
	result      Result
}

func (s *fakeRepository) Config(context.Context) (Config, error)    { return s.config, nil }
func (s *fakeRepository) Targets(context.Context) ([]Target, error) { return s.config.Targets, nil }
func (s *fakeRepository) Snapshot(context.Context, Target, time.Time) ([]int64, time.Time, error) {
	v := make([]int64, 11)
	v[10] = 20000000
	return v, time.Now(), s.coverageErr
}
func (s *fakeRepository) Claim(context.Context, Target, time.Time, int64, int64, string, time.Time) (string, error) {
	if s.claimErr != nil {
		return "", s.claimErr
	}
	if s.claimed {
		return "", nil
	}
	s.claimed = true
	return "call", nil
}
func (s *fakeRepository) Finish(_ context.Context, _ string, r Result, _ time.Time) error {
	s.finishes++
	s.result = r
	return nil
}

type fakeCaller struct {
	ready  bool
	calls  int
	phones []string
}

func (c *fakeCaller) Ready() bool { return c.ready }
func (c *fakeCaller) Call(_ context.Context, _ Config, target Target, _, _ string) Result {
	c.calls++
	c.phones = append(c.phones, target.Phone)
	return Result{Status: "unknown", Code: "timeout"}
}
func TestRunnerDurableCooldownAndFailClosed(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Enabled = true
	cfg.Targets = []Target{{Site: "a", UserID: 7}}
	cfg.Recipients = []Recipient{{Phone: "13800000000"}}
	blockedStore := &fakeRepository{config: cfg}
	blockedCaller := &fakeCaller{ready: true}
	blockedRunner := NewRunner(blockedStore, blockedCaller)
	blockedRunner.NotificationsAllowed = func() (bool, error) { return false, nil }
	if err := blockedRunner.Once(context.Background()); err != nil || blockedCaller.calls != 0 || blockedRunner.Status()[0].State != "notifications_disabled" {
		t.Fatal("global notification switch ignored", err)
	}
	s := &fakeRepository{config: cfg}
	c := &fakeCaller{ready: true}
	for i := 0; i < 2; i++ {
		r := NewRunner(s, c)
		if err := r.Once(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	if c.calls != 1 || s.finishes != 1 || s.result.Status != "unknown" {
		t.Fatal("restart retried unknown call")
	}
	for _, tc := range []struct {
		ready           bool
		coverage, claim error
	}{{false, nil, nil}, {true, ErrCoverage, nil}, {true, errors.New("database"), nil}, {true, nil, errors.New("claim failed")}} {
		s := &fakeRepository{config: cfg, coverageErr: tc.coverage, claimErr: tc.claim}
		c := &fakeCaller{ready: tc.ready}
		r := NewRunner(s, c)
		_ = r.Once(context.Background())
		if c.calls != 0 {
			t.Fatal("called without credentials/coverage/durable reservation")
		}
	}
}

func TestRunnerCallsAllMatchingRecipients(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Enabled = true
	cfg.Targets = []Target{{Site: "site-a", UserID: 7, Label: "alice"}}
	cfg.Recipients = []Recipient{
		{Phone: "13800000000"},
		{Phone: "13900000000", Targets: []string{"site-a/7"}},
		{Phone: "13700000000", Targets: []string{"site-a/8"}},
	}
	store := &multiClaimRepository{fakeRepository: fakeRepository{config: cfg}}
	caller := &fakeCaller{ready: true}
	runner := NewRunner(store, caller)
	if err := runner.Once(context.Background()); err != nil {
		t.Fatal(err)
	}
	if caller.calls != 2 || len(caller.phones) != 2 || caller.phones[0] != "13800000000" || caller.phones[1] != "13900000000" {
		t.Fatalf("calls=%d phones=%v", caller.calls, caller.phones)
	}
}

type multiClaimRepository struct{ fakeRepository }

func (s *multiClaimRepository) Claim(_ context.Context, _ Target, _ time.Time, _, _ int64, _ string, _ time.Time) (string, error) {
	s.claimed = false
	return "call", nil
}
