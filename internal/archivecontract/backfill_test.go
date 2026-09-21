package archivecontract

import (
	"encoding/json"
	"math"
	"reflect"
	"strings"
	"testing"
	"time"
)

func validBackfillTask() BackfillTask {
	return BackfillTask{Identity: testIdentity(), TaskID: strings.Repeat("c", 32), Date: "2026-09-01", Type: "date_backfill", Attempt: 1, Policy: DefaultCoveragePolicy()}
}

func validBackfillStatus() BackfillStatus {
	from, to, _ := DateBounds("2026-09-01")
	return BackfillStatus{TaskID: strings.Repeat("c", 32), Date: "2026-09-01", Attempt: 1, WriterEpoch: 1, State: "succeeded", AfterCreatedUnix: from, AfterID: 1, ScannedRows: 1, BatchID: strings.Repeat("d", 32), SourceNowUnix: to + 300}
}

func TestBackfillWireRoundTripKeepsExactProgress(t *testing.T) {
	s := validBackfillStatus()
	s.WriterEpoch, s.AfterID, s.ScannedRows = math.MaxUint64, math.MaxInt64, 9007199254740993
	s.ReadBytes, s.WrittenBytes, s.ElapsedMillis, s.CatalogRevision = math.MaxUint64, math.MaxUint64-1, 9007199254740993, math.MaxUint64
	raw, err := json.Marshal(s)
	if err != nil {
		t.Fatal(err)
	}
	var wire map[string]any
	if err = json.Unmarshal(raw, &wire); err != nil {
		t.Fatal(err)
	}
	for field, want := range map[string]string{"writer_epoch": "18446744073709551615", "after_id": "9223372036854775807", "scanned_rows": "9007199254740993", "read_bytes": "18446744073709551615", "written_bytes": "18446744073709551614", "elapsed_millis": "9007199254740993", "catalog_revision": "18446744073709551615"} {
		if wire[field] != want {
			t.Errorf("%s lost exact string representation: %v", field, wire[field])
		}
	}
	var restored BackfillStatus
	if err = json.Unmarshal(raw, &restored); err != nil || !reflect.DeepEqual(s, restored) {
		t.Fatalf("cursor did not survive wire round trip: %+v %v", restored, err)
	}
	if err = restored.Validate(); err != nil {
		t.Fatal(err)
	}
	for _, bad := range []string{`{"after_id":9007199254740993}`, `{"writer_epoch":"18446744073709551616"}`, `{"read_bytes":"-1"}`} {
		if json.Unmarshal([]byte(bad), &restored) == nil {
			t.Errorf("unsafe integer encoding accepted: %s", bad)
		}
	}
}

func TestBackfillDateBoundsPreserveBeijingMidnight(t *testing.T) {
	for _, date := range []string{"2026-09-01", "2026-10-01", "2024-02-29"} {
		from, to, err := DateBounds(date)
		if err != nil || to-from != 86400 || time.Unix(from, 0).UTC().Hour() != 16 {
			t.Fatalf("wrong Beijing full-day boundary for %s: %d %d %v", date, from, to, err)
		}
		if got := time.Unix(from, 0).In(time.FixedZone("Beijing", 28800)).Format("2006-01-02"); got != date {
			t.Fatalf("day shifted across month: %s != %s", got, date)
		}
	}
	for _, date := range []string{"", "2026-9-01", "2026-02-29", "1969-12-31", "2026-09-01T00:00:00+08:00", "2026-09-01 ", "0000-01-01"} {
		if _, _, err := DateBounds(date); err == nil {
			t.Errorf("invalid date accepted: %q", date)
		}
	}
}

func TestBackfillTaskRejectsUnboundedOrAmbiguousRequests(t *testing.T) {
	for _, tc := range []struct {
		name   string
		change func(*BackfillTask)
	}{
		{"zero-task", func(v *BackfillTask) { v.TaskID = strings.Repeat("0", 32) }},
		{"missing-generation", func(v *BackfillTask) { v.SourceGenerationID = "" }},
		{"seal-request", func(v *BackfillTask) { v.Type = "seal" }},
		{"invalid-day", func(v *BackfillTask) { v.Date = "2026-09-31" }},
		{"unversioned-attempt", func(v *BackfillTask) { v.Attempt = 0 }},
		{"unbounded-rows", func(v *BackfillTask) { v.Policy.Budget.MaxRows = 5001 }},
		{"unbounded-bytes", func(v *BackfillTask) { v.Policy.Budget.MaxBytes = math.MaxUint64 }},
		{"row-larger-than-batch", func(v *BackfillTask) { v.Policy.Budget.MaxRowBytes = v.Policy.Budget.MaxBytes + 1 }},
		{"unbounded-duration", func(v *BackfillTask) { v.Policy.Budget.MaxDurationMillis = 30001 }},
		{"zero-budget", func(v *BackfillTask) { v.Policy.Budget = ScanBudget{} }},
		{"unbounded-recent-days", func(v *BackfillTask) { v.Policy.RecentDays = 32 }},
		{"coverage-without-evidence", func(v *BackfillTask) { v.Policy.CoverageFrom = "2026-09-01" }},
		{"retention-without-evidence", func(v *BackfillTask) { v.Policy.SourceRetainedFrom = "2026-09-01"; v.Policy.Evidence = " \n\t" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			v := validBackfillTask()
			tc.change(&v)
			if v.Validate() == nil {
				t.Fatal("invalid backfill request accepted")
			}
		})
	}
	unknown := validBackfillTask()
	if err := unknown.Validate(); err != nil {
		t.Fatal("unknown history should remain representable:", err)
	}
	declared := validBackfillTask()
	declared.Policy.CoverageFrom, declared.Policy.SourceRetainedFrom, declared.Policy.Evidence = "2026-01-01", "2026-09-01", "operator confirmed retention boundary"
	if err := declared.Validate(); err != nil {
		t.Fatal("coverage lost to retention must remain representable as unknown:", err)
	}
}

func TestBackfillCompletionRequiresReceiptWithoutClaimingSeal(t *testing.T) {
	zero := validBackfillStatus()
	zero.AfterCreatedUnix, zero.AfterID, zero.ScannedRows = 0, 0, 0
	zero.EmptyCandidate = true
	if err := zero.Validate(); err != nil {
		t.Fatal("successful zero-row scan candidate rejected:", err)
	}
	for _, tc := range []struct {
		name   string
		change func(*BackfillStatus)
	}{
		{"no-commit-receipt", func(v *BackfillStatus) { v.BatchID = "" }},
		{"invalid-receipt", func(v *BackfillStatus) { v.BatchID = strings.Repeat("0", 32) }},
		{"unfenced-result", func(v *BackfillStatus) { v.WriterEpoch = 0 }},
		{"failed-query-is-not-empty", func(v *BackfillStatus) { v.State = "blocked"; v.ErrorCode = "source_query_failed" }},
		{"nonempty-is-not-empty", func(v *BackfillStatus) { v.ScannedRows = 1 }},
		{"completed-with-error", func(v *BackfillStatus) { v.ErrorCode = "source_cleared" }},
		{"completed-without-source-clock", func(v *BackfillStatus) { v.SourceNowUnix = 0 }},
		{"completed-before-day-end", func(v *BackfillStatus) { _, to, _ := DateBounds(v.Date); v.SourceNowUnix = to - 1 }},
		{"cannot-claim-sealed", func(v *BackfillStatus) { v.State = "sealed" }},
		{"cannot-claim-verified", func(v *BackfillStatus) { v.State = "verified" }},
		{"id-without-time", func(v *BackfillStatus) { v.EmptyCandidate = false; v.AfterID = 1 }},
		{"empty-with-row-cursor", func(v *BackfillStatus) { v.AfterCreatedUnix, _, _ = DateBounds(v.Date); v.AfterID = 1 }},
		{"nonempty-without-cursor", func(v *BackfillStatus) { v.EmptyCandidate = false; v.ScannedRows = 1 }},
		{"cross-day-cursor", func(v *BackfillStatus) { v.EmptyCandidate = false; _, v.AfterCreatedUnix, _ = DateBounds(v.Date) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			v := zero
			tc.change(&v)
			if v.Validate() == nil {
				t.Fatal("unproven or contradictory completed result accepted")
			}
		})
	}
	zero.AfterCreatedUnix, _, _ = DateBounds(zero.Date)
	if err := zero.Validate(); err != nil {
		t.Fatal("empty task initialized at the start of its day rejected:", err)
	}
}

func TestBackfillControlErrorsCannotCarrySQLOrSourceContent(t *testing.T) {
	for _, code := range []string{"source_query_failed: user=secret", "Error 1146: Table private.logs not found", "mysql://user:password@host", "source_query_failed\nsecret", strings.Repeat("x", 2048)} {
		s := validBackfillStatus()
		s.State, s.ErrorCode = "blocked", code
		if s.Validate() == nil || (ArchiveMetrics{Stream: "date_backfill", ErrorCode: code}).Validate() == nil {
			t.Errorf("source/SQL details accepted as a public error code: %q", code)
		}
	}
	for _, code := range []string{"source_index_missing", "source_cleared", "source_history_unknown", "row_too_large", "writer_lease", "date_frozen"} {
		s := validBackfillStatus()
		s.State, s.ErrorCode = "blocked", code
		if err := s.Validate(); err != nil {
			t.Errorf("actionable fixed reason %s rejected: %v", code, err)
		}
	}
}
