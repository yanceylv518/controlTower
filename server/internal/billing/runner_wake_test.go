package billing

import (
	"context"
	"errors"
	"testing"
	"time"
)

type idleWakeStore struct {
	JobStore
	claims int
	cancel context.CancelFunc
}

func (s *idleWakeStore) ClaimBillingStep(context.Context) (Job, JobStep, bool, error) {
	s.claims++
	if s.claims == 1 {
		return Job{JobType: "verify"}, JobStep{}, true, nil
	}
	if s.claims == 3 {
		s.cancel()
	}
	return Job{}, JobStep{}, false, nil
}
func (s *idleWakeStore) ClaimBillingPublish(context.Context) (Job, bool, error) {
	return Job{}, false, nil
}
func (s *idleWakeStore) FailBillingStep(context.Context, Job, JobStep, error) error { return nil }
func TestRunnerWakesProducerOnceWhenBatchDrains(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	store := &idleWakeStore{cancel: cancel}
	calls := 0
	err := (JobRunner{Store: store, Poll: time.Millisecond, OnIdle: func() { calls++ }}).Run(ctx)
	if !errors.Is(err, context.Canceled) || calls != 1 || store.claims != 3 {
		t.Fatal(err, calls, store.claims)
	}
}

type completedDayStore struct {
	JobStore
	claimed bool
}

func (s *completedDayStore) ClaimBillingPublish(context.Context) (Job, bool, error) {
	return Job{Status: "no_data"}, true, nil
}
func (s *completedDayStore) ClaimBillingStep(context.Context) (Job, JobStep, bool, error) {
	s.claimed = true
	return Job{}, JobStep{}, false, nil
}
func TestRunnerFinishesEmptyDayBeforeReadingNextDay(t *testing.T) {
	store := &completedDayStore{}
	wakes := 0
	worked, err := (JobRunner{Store: store, OnIdle: func() { wakes++ }}).RunOnce(context.Background())
	if err != nil || !worked || store.claimed || wakes != 1 {
		t.Fatal(worked, err, store.claimed, wakes)
	}
}
