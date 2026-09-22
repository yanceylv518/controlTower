package archivepipeline

import (
	"encoding/json"
	"errors"
	"testing"
	"time"
)

func fixture(t *testing.T) *State {
	t.Helper()
	s := New(Settings{Migration: true, Organization: true, Verification: true, Collection: true})
	s.SchemaReady = true
	if err := s.BeginRound(time.Date(2026, 9, 22, 12, 0, 0, 0, time.FixedZone("site", 28800)), time.FixedZone("site", 28800)); err != nil {
		t.Fatal(err)
	}
	return &s
}

func reserve(t *testing.T, s *State, task Task) Work {
	t.Helper()
	w, err := s.Reserve(task)
	if err != nil {
		t.Fatalf("reserve %s: %v", task, err)
	}
	return w
}

func finish(t *testing.T, s *State, w Work, code, version string) {
	t.Helper()
	if err := s.Finish(w, code, version); err != nil {
		t.Fatal(err)
	}
}

func observe(t *testing.T, s *State, dates ...string) {
	t.Helper()
	if err := s.ObserveCommittedRows(dates, nil); err != nil {
		t.Fatal(err)
	}
}

func TestMigrationAndCollectionOverlapWithoutStartingOrganization(t *testing.T) {
	s := fixture(t)
	m := reserve(t, s, Migration)
	c := reserve(t, s, Collection)
	observe(t, s, "2026-09-20", "2026-09-21", "2026-09-22")
	if _, err := s.Reserve(Organization); !errors.Is(err, ErrNotReady) {
		t.Fatal(err)
	}
	finish(t, s, m, "", "")
	o := reserve(t, s, Organization)
	if o.Date != "2026-09-20" {
		t.Fatal(o)
	}
	finish(t, s, o, "", "")
	if _, err := s.Reserve(Verification); !errors.Is(err, ErrNotReady) {
		t.Fatal("verification overlapped collection", err)
	}
	finish(t, s, c, "", "")
	v := reserve(t, s, Verification)
	if v.Date != o.Date {
		t.Fatal("organized a later day before verifying earlier day")
	}
	if _, err := s.Reserve(Collection); !errors.Is(err, ErrNotReady) {
		t.Fatal("collection overlapped verification")
	}
	if _, err := s.Reserve(Organization); !errors.Is(err, ErrNotReady) {
		t.Fatal("organization overlapped verification")
	}
	finish(t, s, v, "", "seal-20-v1")
	if w := reserve(t, s, Organization); w.Date != "2026-09-21" {
		t.Fatal(w)
	}
	if _, err := s.Reserve(Migration); !errors.Is(err, ErrNotReady) {
		t.Fatal("migration repeated", err)
	}
}

func TestRoundBoundaryAndCrossMonthObservation(t *testing.T) {
	s := fixture(t)
	s.MigrationDone = true
	observe(t, s, "2026-09-21")
	if s.NextDate() != "" {
		t.Fatal("last observed date is not collection-complete")
	}
	observe(t, s, "2026-09-22")
	if s.NextDate() != "2026-09-21" || s.DayState("2026-09-22") != "collecting" {
		t.Fatal(s)
	}
	// Seeing a future day must not extend the fixed September 21 cutoff.
	observe(t, s, "2026-09-23")
	if s.Cutoff != "2026-09-21" {
		t.Fatal(s.Cutoff)
	}
	s = fixture(t)
	s.MigrationDone = true
	if err := s.BeginRound(time.Date(2026, 10, 1, 0, 1, 0, 0, time.FixedZone("site", 28800)), time.FixedZone("site", 28800)); err != nil {
		t.Fatal(err)
	}
	observe(t, s, "2026-09-30")
	if s.NextDate() != "" {
		t.Fatal("table/date observation alone closed itself")
	}
	observe(t, s, "2026-10-01")
	if s.NextDate() != "2026-09-30" {
		t.Fatal(s.NextDate())
	}
}

func TestLateRowsImmediatelyInvalidateSealedResultButKeepVersion(t *testing.T) {
	s := fixture(t)
	s.MigrationDone = true
	observe(t, s, "2026-09-20", "2026-09-21")
	finish(t, s, reserve(t, s, Organization), "", "")
	finish(t, s, reserve(t, s, Verification), "", "immutable-version-1")
	if s.DayState("2026-09-20") != "sealed" {
		t.Fatal(s)
	}
	if err := s.ObserveCommittedRows([]string{"2026-09-20"}, map[string]bool{"2026-09-20": false}); err != nil {
		t.Fatal(err)
	}
	if s.DayState("2026-09-20") != "sealed" {
		t.Fatal("identical replay invalidated seal")
	}
	if err := s.ObserveCommittedRows([]string{"2026-09-20"}, map[string]bool{"2026-09-20": true}); err != nil {
		t.Fatal(err)
	}
	d := s.Days["2026-09-20"]
	if s.DayState("2026-09-20") != "changed" || d.Result != nil || d.LastSeal.SealVersion != "immutable-version-1" {
		t.Fatal(d)
	}
	finish(t, s, reserve(t, s, Organization), "", "")
	finish(t, s, reserve(t, s, Verification), "", "immutable-version-2")
	if d.Result.SealVersion != "immutable-version-2" {
		t.Fatal(d)
	}
}

func TestMutationDuringWorkCannotPublishOldRevision(t *testing.T) {
	for _, task := range []Task{Organization, Verification} {
		t.Run(string(task), func(t *testing.T) {
			s := fixture(t)
			s.MigrationDone = true
			observe(t, s, "2026-09-20", "2026-09-21")
			if task == Verification {
				finish(t, s, reserve(t, s, Organization), "", "")
			}
			w := reserve(t, s, task)
			if err := s.ObserveCommittedRows(nil, map[string]bool{w.Date: true}); err != nil {
				t.Fatal(err)
			}
			version := ""
			if task == Verification {
				version = "stale-seal"
			}
			if err := s.Finish(w, "", version); !errors.Is(err, ErrStale) {
				t.Fatal(err)
			}
			if s.Days[w.Date].Result != nil {
				t.Fatal("published stale result")
			}
			if err := s.Release(w); err != nil {
				t.Fatal(err)
			}
			if w2 := reserve(t, s, Organization); w2.Revision <= w.Revision {
				t.Fatal(w2)
			}
		})
	}
}

func TestDateFailureAdvancesButInfrastructureFailureDoesNot(t *testing.T) {
	s := fixture(t)
	s.MigrationDone = true
	observe(t, s, "2026-09-19", "2026-09-20", "2026-09-21")
	o := reserve(t, s, Organization)
	if err := s.Release(o); err != nil {
		t.Fatal(err)
	} // database disconnected
	if s.NextDate() != "2026-09-19" {
		t.Fatal("infrastructure error skipped date")
	}
	finish(t, s, reserve(t, s, Organization), "", "")
	finish(t, s, reserve(t, s, Verification), "source_history_unconfirmed", "")
	if s.NextDate() != "2026-09-20" {
		t.Fatal("date-specific failure blocked subsequent dates")
	}
	finish(t, s, reserve(t, s, Organization), "", "")
	finish(t, s, reserve(t, s, Verification), "", "v20")
	if s.NextDate() != "" {
		t.Fatal(s.NextDate())
	}
	if err := s.BeginRound(time.Date(2026, 9, 23, 0, 1, 0, 0, time.UTC), time.UTC); err != nil {
		t.Fatal(err)
	}
	if s.NextDate() != "2026-09-19" {
		t.Fatal("failed date not retryable next round")
	}
}

func TestRestartFencingAndPausePreserveProgress(t *testing.T) {
	s := fixture(t)
	s.MigrationDone = true
	observe(t, s, "2026-09-20", "2026-09-21")
	o := reserve(t, s, Organization)
	raw, err := json.Marshal(s)
	if err != nil {
		t.Fatal(err)
	}
	var restored State
	if err = json.Unmarshal(raw, &restored); err != nil {
		t.Fatal(err)
	}
	if _, err = restored.Reserve(Organization); !errors.Is(err, ErrConflict) {
		t.Fatal("restart ignored active reservation")
	}
	restored.RecoverAfterFence()
	o2 := reserve(t, &restored, Organization)
	if err = restored.Finish(o, "", ""); !errors.Is(err, ErrConflict) {
		t.Fatal("old fenced worker completed new work")
	}
	restored.Settings.Organization = false
	finish(t, &restored, o2, "", "") // in-flight batch may finish after pause request
	restored.Settings.Verification = false
	if _, err = restored.Reserve(Verification); !errors.Is(err, ErrNotReady) {
		t.Fatal(err)
	}
	if restored.DayState(o.Date) != "organized" || !restored.MigrationDone {
		t.Fatal(restored)
	}
	restored.Settings.Verification = true
	finish(t, &restored, reserve(t, &restored, Verification), "", "v1")
}

func TestCrossDayMutationInvalidatesBothDates(t *testing.T) {
	s := fixture(t)
	s.MigrationDone = true
	observe(t, s, "2026-09-19", "2026-09-20", "2026-09-21")
	for i := 0; i < 2; i++ {
		finish(t, s, reserve(t, s, Organization), "", "")
		finish(t, s, reserve(t, s, Verification), "", "v1")
	}
	if err := s.ObserveCommittedRows(nil, map[string]bool{"2026-09-19": true, "2026-09-20": true}); err != nil {
		t.Fatal(err)
	}
	for _, date := range []string{"2026-09-19", "2026-09-20"} {
		if s.DayState(date) != "changed" {
			t.Fatal(date, s.DayState(date))
		}
	}
}

func TestInvalidObservationDoesNotPartiallyAdvanceFrontier(t *testing.T) {
	s := fixture(t)
	if err := s.ObserveCommittedRows([]string{"2026-09-21", "2026-02-30"}, nil); !errors.Is(err, ErrConflict) {
		t.Fatal(err)
	}
	if s.Frontier != "" || len(s.Days) != 0 {
		t.Fatal("partially accepted invalid observation")
	}
}
