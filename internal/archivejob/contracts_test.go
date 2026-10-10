package archivejob

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestProtocolRejectsOldStates(t *testing.T) {
	s := Status{Protocol: Protocol, Days: []Day{{Date: "2026-06-15", State: "pending", Rows: ""}}}
	if !s.Valid() {
		t.Fatal("new state rejected")
	}
	for _, state := range []string{"organization", "migration", "verification", "dirty"} {
		s.Days[0].State = state
		if s.Valid() {
			t.Fatal("legacy state accepted", state)
		}
	}
}

func TestCountReportValidation(t *testing.T) {
	s := Status{Protocol: Protocol, CountsDate: "2026-09-17", CountsError: "database_timeout"}
	if !s.Valid() {
		t.Fatal("valid count diagnostic rejected")
	}
	s.CountsDate = "2026-09-99"
	if s.Valid() {
		t.Fatal("invalid count date accepted")
	}
	s.CountsDate, s.CountsError = "", strings.Repeat("x", 257)
	if s.Valid() {
		t.Fatal("oversized count diagnostic accepted")
	}
}
func TestLargeCursorWirePrecision(t *testing.T) {
	s := Status{Protocol: Protocol, Collection: Progress{AfterID: 9007199254740993}}
	raw, err := json.Marshal(s)
	if err != nil {
		t.Fatal(err)
	}
	var decoded map[string]any
	_ = json.Unmarshal(raw, &decoded)
	if decoded["collection"].(map[string]any)["after_id"] != "9007199254740993" {
		t.Fatal("cursor lost precision")
	}
}

func TestScheduleSettingsValidationAndRoundTrip(t *testing.T) {
	for _, s := range []Settings{{}, {CollectionBatches: 1, HistoryBatches: 100}} {
		if !s.Valid() {
			t.Fatal("valid ratio rejected")
		}
		b, _ := json.Marshal(s)
		var restored Settings
		if err := json.Unmarshal(b, &restored); err != nil || restored != s {
			t.Fatal("ratio lost", err)
		}
	}
	for _, s := range []Settings{{CollectionBatches: -1}, {HistoryBatches: 101}} {
		if s.Valid() {
			t.Fatal("invalid ratio accepted")
		}
	}
}

func TestSummaryStartDateSettings(t *testing.T) {
	for _, date := range []string{"", "2026-09-01", "2028-02-29"} {
		s := Settings{SummaryFromDate: date}
		if !s.Valid() {
			t.Fatal("valid date rejected", date)
		}
		raw, _ := json.Marshal(s)
		var decoded Settings
		if json.Unmarshal(raw, &decoded) != nil || decoded != s {
			t.Fatal("date lost in config", date)
		}
	}
	for _, date := range []string{"2026-02-29", "2026-9-01", "2026-09-1", "2026-09-01T00:00:00Z", "0999-01-01", "not-a-date"} {
		if (Settings{SummaryFromDate: date}).Valid() {
			t.Fatal("invalid date accepted", date)
		}
	}
}
