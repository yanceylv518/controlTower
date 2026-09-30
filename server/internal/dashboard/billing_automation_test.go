package dashboard

import (
	"context"
	"controltower/server/internal/billing"
	"errors"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

type automaticStoreTest struct {
	AutomaticBillingStore
	jobs []billing.Job
	full bool
}

func (s *automaticStoreTest) BillingDataSource(context.Context) (string, error) {
	return "archive", nil
}
func (s *automaticStoreTest) MissingBillingDays(context.Context, billing.AutomaticTarget, time.Time) ([]time.Time, error) {
	return []time.Time{time.Date(2025, 9, 28, 0, 0, 0, 0, billing.BusinessLocation)}, nil
}
func (s *automaticStoreTest) MissingBillingMonths(context.Context, billing.AutomaticTarget, time.Time) ([]time.Time, error) {
	return []time.Time{time.Date(2025, 8, 1, 0, 0, 0, 0, billing.BusinessLocation)}, nil
}
func (s *automaticStoreTest) FailedStatementMoneySnapshot(context.Context, string) (*billing.MoneySnapshot, error) {
	return &billing.MoneySnapshot{}, nil
}
func (s *automaticStoreTest) CreateBillingStatementJob(_ context.Context, j billing.Job, _ []billing.JobStep, _ string) error {
	s.jobs = append(s.jobs, j)
	if s.full {
		return billing.ErrStatementQueueFull
	}
	return nil
}
func TestAutomaticDailyAndClosedMonthJobs(t *testing.T) {
	s := &automaticStoreTest{}
	target := billing.AutomaticTarget{InstanceID: "site", Kind: "user_statement", SubjectID: 7}
	a := BillingAutomation{Store: s, Source: roleSourceTest{role: 1}}
	if err := a.Fill(context.Background(), target); err != nil {
		t.Fatal(err)
	}
	if len(s.jobs) != 2 {
		t.Fatal(s.jobs)
	}
	d, m := s.jobs[0], s.jobs[1]
	if d.BillPeriod != "daily" || m.BillPeriod != "monthly" || m.To.In(billing.BusinessLocation).Format("2006-01-02") != "2025-09-01" {
		t.Fatal(d, m)
	}
	for _, j := range s.jobs {
		if j.DataSource != "archive" || j.UsageVersion != 3 || j.UserID != 7 || j.ExcludeZeroOutput {
			t.Fatal(j)
		}
	}
	if err := a.Fill(context.Background(), target); err != nil {
		t.Fatal(err)
	}
	if s.jobs[2].RequestKey != d.RequestKey || s.jobs[3].RequestKey != m.RequestKey || d.RequestKey == m.RequestKey {
		t.Fatal("unstable period identity")
	}
}
func TestAutomaticQueueFullDefersRemainingPeriods(t *testing.T) {
	s := &automaticStoreTest{full: true}
	if err := (BillingAutomation{Store: s, Source: roleSourceTest{role: 1}}).Fill(context.Background(), billing.AutomaticTarget{InstanceID: "site", Kind: "user_statement", SubjectID: 7}); err != nil {
		t.Fatal(err)
	}
	if len(s.jobs) != 1 {
		t.Fatal("monthly queued after full daily queue")
	}
}

type activitySourceTest struct {
	BillingRatioSource
	days []time.Time
}

func (s activitySourceTest) ActiveBillingDays(context.Context, billing.AutomaticTarget, []int64, time.Time) ([]time.Time, error) {
	return s.days, nil
}
func TestAutomaticSkipsDatesWithoutConsumption(t *testing.T) {
	s := &automaticStoreTest{}
	// Source mode is supplied by a wrapper to exercise real activity discovery.
	store := &sourceActivityStore{automaticStoreTest: s}
	a := BillingAutomation{Store: store, Source: activitySourceTest{days: []time.Time{}}}
	if err := a.Fill(context.Background(), billing.AutomaticTarget{InstanceID: "site", Kind: "user_statement", SubjectID: 7}); err != nil {
		t.Fatal(err)
	}
	if len(s.jobs) != 0 {
		t.Fatal("empty dates produced invoices")
	}
	a.Source = activitySourceTest{days: []time.Time{time.Date(2025, 9, 28, 0, 0, 0, 0, billing.BusinessLocation)}}
	if err := a.Fill(context.Background(), billing.AutomaticTarget{InstanceID: "site", Kind: "user_statement", SubjectID: 7}); err != nil {
		t.Fatal(err)
	}
	if len(s.jobs) != 1 || s.jobs[0].BillPeriod != "daily" {
		t.Fatal("inactive month was generated", s.jobs)
	}
}

type sourceActivityStore struct{ *automaticStoreTest }

func (s *sourceActivityStore) CreateBillingStatementJob(ctx context.Context, j billing.Job, steps []billing.JobStep, name string) error {
	if j.BillPeriod == "monthly" {
		return billing.ErrDailyBillsIncomplete
	}
	return s.automaticStoreTest.CreateBillingStatementJob(ctx, j, steps, name)
}

func (*sourceActivityStore) BillingDataSource(context.Context) (string, error) { return "source", nil }

type boundedAutomaticStore struct {
	*automaticStoreTest
	saved billing.AutomaticTarget
	end   time.Time
}

func (s *boundedAutomaticStore) PutBillingAutomaticTarget(_ context.Context, t billing.AutomaticTarget) error {
	s.saved = t
	return nil
}
func (s *boundedAutomaticStore) MissingBillingDays(_ context.Context, _ billing.AutomaticTarget, end time.Time) ([]time.Time, error) {
	s.end = end
	return nil, nil
}
func (s *boundedAutomaticStore) MissingBillingMonths(context.Context, billing.AutomaticTarget, time.Time) ([]time.Time, error) {
	return nil, nil
}
func TestGenerateMissingRespectsSelectedMonth(t *testing.T) {
	for _, current := range []bool{false, true} {
		from := time.Date(2025, 9, 1, 0, 0, 0, 0, billing.BusinessLocation)
		if current {
			now := time.Now().In(billing.BusinessLocation)
			from = time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, billing.BusinessLocation)
			if !from.Before(billing.CompleteDayBoundary(now)) {
				continue
			}
		}
		to := from.AddDate(0, 1, 0)
		s := &boundedAutomaticStore{automaticStoreTest: &automaticStoreTest{}}
		body := `{"instance_id":"site","kind":"user_statement","subject_id":7,"from":"` + from.Format(time.RFC3339) + `","to":"` + to.Format(time.RFC3339) + `"}`
		w := httptest.NewRecorder()
		(BillingAutomation{Store: s, Source: roleSourceTest{role: 1}}).ServeHTTP(w, httptest.NewRequest("POST", "/", strings.NewReader(body)))
		if w.Code != 202 {
			t.Fatal(w.Code, w.Body.String())
		}
		end := to
		if today := billing.CompleteDayBoundary(time.Now()); end.After(today) {
			end = today
		}
		if !s.saved.From.Equal(from) || !s.saved.To.Equal(to) || !s.end.Equal(end) {
			t.Fatal("selected month escaped", s.saved, s.end)
		}
	}
}
func TestGenerateMissingRejectsPartialMonthBoundary(t *testing.T) {
	s := &boundedAutomaticStore{automaticStoreTest: &automaticStoreTest{}}
	w := httptest.NewRecorder()
	(BillingAutomation{Store: s, Source: roleSourceTest{role: 1}}).ServeHTTP(w, httptest.NewRequest("POST", "/", strings.NewReader(`{"instance_id":"site","kind":"user_statement","subject_id":7,"from":"2025-09-02T00:00:00+08:00","to":"2025-10-01T00:00:00+08:00"}`)))
	if w.Code != 400 || !s.saved.From.IsZero() {
		t.Fatal(w.Code, s.saved)
	}
}

type cachedActivityStore struct {
	*sourceActivityStore
	checks map[int64]bool
}

func (s *cachedActivityStore) BillingDayActivity(_ context.Context, t billing.AutomaticTarget) (bool, bool, error) {
	v, ok := s.checks[t.From.Unix()]
	return v, ok, nil
}
func (s *cachedActivityStore) RecordBillingDayActivity(_ context.Context, t billing.AutomaticTarget, v bool) error {
	s.checks[t.From.Unix()] = v
	return nil
}

type countedActivitySource struct {
	BillingRatioSource
	calls  int
	active bool
}

func (s *countedActivitySource) ActiveBillingDays(_ context.Context, t billing.AutomaticTarget, _ []int64, end time.Time) ([]time.Time, error) {
	s.calls++
	if !end.Equal(t.From.AddDate(0, 0, 1)) {
		panic("activity scan exceeded one day")
	}
	if s.active {
		return []time.Time{t.From}, nil
	}
	return nil, nil
}
func TestAutomaticCachesEmptyAndActiveDays(t *testing.T) {
	for _, active := range []bool{false, true} {
		base := &automaticStoreTest{}
		store := &cachedActivityStore{sourceActivityStore: &sourceActivityStore{base}, checks: map[int64]bool{}}
		source := &countedActivitySource{active: active}
		a := BillingAutomation{Store: store, Source: source}
		target := billing.AutomaticTarget{InstanceID: "site", Kind: "user_statement", SubjectID: 7}
		for i := 0; i < 3; i++ {
			if err := a.Fill(context.Background(), target); err != nil {
				t.Fatal(err)
			}
		}
		if source.calls != 1 {
			t.Fatalf("active=%v: repeated source checks: %d", active, source.calls)
		}
		if !active && len(base.jobs) != 0 {
			t.Fatal("empty day generated invoice")
		}
	}
}
func TestAutomaticCompletedRangeNeverReadsSource(t *testing.T) {
	store := &boundedAutomaticStore{automaticStoreTest: &automaticStoreTest{}}
	source := &countedActivitySource{active: true}
	if err := (BillingAutomation{Store: store, Source: source}).Fill(context.Background(), billing.AutomaticTarget{InstanceID: "site", Kind: "user_statement", SubjectID: 7}); err != nil {
		t.Fatal(err)
	}
	if source.calls != 0 || len(store.jobs) != 0 {
		t.Fatal("completed range was scanned again")
	}
}

type roleSourceTest struct {
	BillingRatioSource
	role    int
	failure error
}

func (s roleSourceTest) BillingUserRole(context.Context, string, int64) (int, error) {
	return s.role, s.failure
}
func (s activitySourceTest) BillingUserRole(context.Context, string, int64) (int, error) {
	return 1, nil
}
func (s *countedActivitySource) BillingUserRole(context.Context, string, int64) (int, error) {
	return 1, nil
}
func TestAdministratorOnlyExcludedFromAutomaticBilling(t *testing.T) {
	for _, role := range []int{1, 10, 100} {
		for _, manual := range []bool{false, true} {
			store := &automaticStoreTest{}
			target := billing.AutomaticTarget{InstanceID: "site", Kind: "user_statement", SubjectID: 7}
			if manual {
				target.From = time.Date(2025, 9, 1, 0, 0, 0, 0, billing.BusinessLocation)
				target.To = target.From.AddDate(0, 1, 0)
			}
			a := BillingAutomation{Store: store, Source: roleSourceTest{role: role}}
			if err := a.Fill(context.Background(), target); err != nil {
				t.Fatal(err)
			}
			if (len(store.jobs) > 0) != (manual || role < 10) {
				t.Fatal(role, manual, store.jobs)
			}
		}
	}
	store := &automaticStoreTest{}
	if err := (BillingAutomation{Store: store}).Fill(context.Background(), billing.AutomaticTarget{Kind: "user_statement"}); err == nil || len(store.jobs) != 0 {
		t.Fatal("unknown role allowed auto billing")
	}
}

func TestAutomaticRoleLookupFailureDoesNotGenerate(t *testing.T) {
	store := &automaticStoreTest{}
	a := BillingAutomation{Store: store, Source: roleSourceTest{failure: errors.New("source unavailable")}}
	if err := a.Fill(context.Background(), billing.AutomaticTarget{Kind: "user_statement"}); err == nil || len(store.jobs) != 0 {
		t.Fatal("lookup failure allowed automatic billing")
	}
}
func TestSubmittedManualTaskDoesNotExpandPastWorkUntil(t *testing.T) {
	store := &automaticStoreTest{}
	from := time.Date(2025, 9, 1, 0, 0, 0, 0, billing.BusinessLocation)
	target := billing.AutomaticTarget{Kind: "user_statement", From: from, To: from.AddDate(0, 1, 0), ProgressUntil: from}
	if err := (BillingAutomation{Store: store}).Fill(context.Background(), target); err != nil || len(store.jobs) != 0 {
		t.Fatal("manual task expanded", err)
	}
}

type wakingAutomationStore struct {
	AutomaticBillingStore
	called chan struct{}
}

func (s wakingAutomationStore) ListBillingAutomaticTargets(context.Context) ([]billing.AutomaticTarget, error) {
	s.called <- struct{}{}
	return nil, nil
}
func TestAutomationWakeDoesNotWaitForMinuteTick(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	wake := make(chan struct{}, 1)
	calls := make(chan struct{}, 2)
	done := make(chan struct{})
	go func() {
		defer close(done)
		(BillingAutomation{Store: wakingAutomationStore{called: calls}, Wake: wake}).Run(ctx)
	}()
	select {
	case <-calls:
	case <-ctx.Done():
		t.Fatal("initial run missing")
	}
	wake <- struct{}{}
	select {
	case <-calls:
	case <-ctx.Done():
		t.Fatal("wake waited for minute tick")
	}
	cancel()
	<-done
}
