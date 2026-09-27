package archivejob

import (
	aj "controltower/internal/archivejob"
	"encoding/json"
	"testing"
)

func TestScheduleRatioAndRestart(t *testing.T) {
	for _, tc := range []struct {
		c, h int
		want string
	}{{0, 0, "CCCCHCCCCH"}, {2, 3, "CCHHHCCHHH"}, {1, 1, "CHCHCHCHCH"}} {
		s := state{}
		got := ""
		cfg := aj.Settings{Collection: true, History: true, CollectionBatches: tc.c, HistoryBatches: tc.h}
		for i := 0; i < 10; i++ {
			if s.nextHistoryTurn(cfg) {
				got += "H"
			} else {
				got += "C"
			}
			raw, _ := json.Marshal(s)
			s = state{}
			if err := json.Unmarshal(raw, &s); err != nil {
				t.Fatal(err)
			}
		}
		if got != tc.want {
			t.Fatalf("%+v: %s", tc, got)
		}
	}
}
func TestScheduleConfigChangesAndSingleTask(t *testing.T) {
	s := state{Turns: 4}
	both := aj.Settings{Collection: true, History: true}
	if !s.nextHistoryTurn(both) {
		t.Fatal("legacy checkpoint not resumed")
	}
	both.CollectionBatches, both.HistoryBatches = 2, 3
	if s.nextHistoryTurn(both) {
		t.Fatal("ratio change did not start collection")
	}
	for i := 0; i < 5; i++ {
		if !s.nextHistoryTurn(aj.Settings{History: true}) {
			t.Fatal("history gated")
		}
		if s.nextHistoryTurn(aj.Settings{Collection: true}) {
			t.Fatal("collection gated")
		}
	}
	if s.nextHistoryTurn(both) {
		t.Fatal("reenable not fresh")
	}
}
