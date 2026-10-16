package dashboard

import (
	"context"
	"controltower/server/internal/billing"
	"testing"
	"time"
)

type monthlyPrepareStore struct {
	automaticStoreTest
	targets []billing.AutomaticTarget
	checked []string
}

func (s *monthlyPrepareStore) ListBillingAutomaticTargets(context.Context) ([]billing.AutomaticTarget, error) {
	return s.targets, nil
}
func (s *monthlyPrepareStore) MissingBillingDays(context.Context, billing.AutomaticTarget, time.Time) ([]time.Time, error) {
	panic("monthly preparation entered day/source path")
}
func (s *monthlyPrepareStore) MissingBillingMonths(_ context.Context, target billing.AutomaticTarget, _ time.Time) ([]time.Time, error) {
	s.checked = append(s.checked, target.InstanceID)
	return []time.Time{time.Date(2025, 1, 1, 0, 0, 0, 0, billing.BusinessLocation)}, nil
}
func TestMonthlyPreparationIsSiteScopedAndNeedsNoSource(t *testing.T) {
	s := &monthlyPrepareStore{targets: []billing.AutomaticTarget{{InstanceID: "a", Kind: "user_statement", SubjectID: 7}, {InstanceID: "b", Kind: "user_statement", SubjectID: 8}}}
	// Nil Source proves this also avoids role/options checks for automatic targets.
	if err := (BillingAutomation{Store: s}).FillMonthly(context.Background(), "a"); err != nil {
		t.Fatal(err)
	}
	if len(s.jobs) != 1 || s.jobs[0].BillPeriod != "monthly" || s.jobs[0].InstanceID != "a" || s.jobs[0].DataSource != "archive" || len(s.checked) != 1 {
		t.Fatal(s.jobs, s.checked)
	}
}

func TestGenerationPreparesMonthlyBeforeEveryNextTask(t *testing.T) {
	s := &generationQueueFake{jobs: map[string][]billing.QueuedGeneration{"a": {{Kind: "billing", ID: "day"}}}, locked: map[string]bool{}}
	prepares := 0
	w := SiteGenerationWorker{Store: s, Prepare: func(_ context.Context, site string) error {
		if !s.locked[site] {
			t.Fatal("preparation lost site lease")
		}
		prepares++
		if prepares == 2 {
			s.jobs[site] = append([]billing.QueuedGeneration{{Kind: "billing", ID: "month"}}, s.jobs[site]...)
		}
		return nil
	}}
	var ran []string
	w.RunTask = func(_ context.Context, site string, task billing.QueuedGeneration) (bool, error) {
		ran = append(ran, task.ID)
		if len(ran) > 3 {
			t.Fatal("queue never drained")
		}
		if len(ran) > 1 {
			s.jobs[site] = s.jobs[site][1:]
		}
		return true, nil
	}
	w.runSite(context.Background(), "a")
	if len(ran) != 3 || ran[0] != "day" || ran[1] != "month" || ran[2] != "day" || s.locked["a"] {
		t.Fatal(ran, s.locked)
	}
}
