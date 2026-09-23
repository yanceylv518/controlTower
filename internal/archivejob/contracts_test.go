package archivejob

import (
	"encoding/json"
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
