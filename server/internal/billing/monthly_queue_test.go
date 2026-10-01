package billing

import (
	"context"
	"testing"
)

type queuedMonthStore struct {
	JobStore
	claimed   bool
	completed bool
}

func (s *queuedMonthStore) ClaimBillingPublish(context.Context) (Job, bool, error) {
	if s.claimed {
		return Job{}, false, nil
	}
	s.claimed = true
	return Job{ID: "month", UsageVersion: 3, BillPeriod: "monthly", Status: "publishing"}, true, nil
}
func (s *queuedMonthStore) CompleteBillingMonth(_ context.Context, job Job) error {
	s.completed = job.ID == "month"
	return nil
}
func TestQueuedMonthPublishesWithoutReadingSourceOrWritingDailyFiles(t *testing.T) {
	s := &queuedMonthStore{}
	// No source, spool or file generator: a month only copies frozen CT totals.
	r := JobRunner{Store: s}
	if worked, e := r.RunOnce(context.Background()); e != nil || !worked || !s.completed {
		t.Fatal(worked, e, s.completed)
	}
}
