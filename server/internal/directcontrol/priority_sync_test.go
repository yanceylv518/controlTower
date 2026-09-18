package directcontrol

import (
	"context"
	"controltower/server/internal/tuning"
	"errors"
	"testing"
	"time"
)

type priorityFake struct {
	rows       []tuning.ChannelBaseValue
	mode       string
	pending    bool
	writes     []tuning.Recommendation
	fail       bool
	afterWrite func()
}

func (f *priorityFake) GetPolicy(string) (tuning.PolicyRecord, bool, error) {
	return tuning.PolicyRecord{Policy: tuning.Policy{DispatchModes: map[string]string{"m": f.mode}}}, true, nil
}
func (f *priorityFake) ListChannelBaseValues(string, string) ([]tuning.ChannelBaseValue, error) {
	return append([]tuning.ChannelBaseValue(nil), f.rows...), nil
}
func (f *priorityFake) HasPendingPrioritySync(string, int64) (bool, error) { return f.pending, nil }
func (f *priorityFake) CreateContinuousWeightChange(r tuning.Recommendation, _ string, _ time.Time) (string, error) {
	f.writes = append(f.writes, r)
	if f.afterWrite != nil {
		f.afterWrite()
	}
	if f.fail {
		return "", errors.New("write failed")
	}
	return "command", nil
}
func TestPriorityCorrectionModesAndTargets(t *testing.T) {
	for _, tc := range []struct {
		name, mode      string
		target, current int64
		pending         bool
		want            int
	}{
		{"automatic", "auto", 11, 2, false, 1}, {"zero target", "auto", 0, 11, false, 1}, {"observe", "observe", 11, 2, false, 0}, {"off", "off", 11, 2, false, 0}, {"equal", "auto", 11, 11, false, 0}, {"pending", "auto", 11, 2, true, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := &priorityFake{mode: tc.mode, pending: tc.pending, rows: []tuning.ChannelBaseValue{{ChannelID: 7, ModelName: "m", Models: []string{"m"}, BasePriority: tc.target, CurrentPriority: tc.current, CurrentWeight: 0, BaseWeight: 0}}}
			if err := reconcilePriorities(context.Background(), f, "site"); err != nil {
				t.Fatal(err)
			}
			if len(f.writes) != tc.want {
				t.Fatalf("writes=%v", f.writes)
			}
			if tc.want > 0 {
				r := f.writes[0]
				if *r.ProposedPriority != tc.target || r.Rule != "base_priority_sync" || r.ProposedWeight != 0 {
					t.Fatalf("wrong correction: %#v", r)
				}
			}
		})
	}
}
func TestPriorityCorrectionRechecksModeAndTargetBetweenWrites(t *testing.T) {
	for _, changeMode := range []bool{false, true} {
		f := &priorityFake{mode: "auto", rows: []tuning.ChannelBaseValue{{ChannelID: 1, ModelName: "m", Models: []string{"m"}, BasePriority: 11}, {ChannelID: 2, ModelName: "m", Models: []string{"m"}, BasePriority: 11}}}
		f.afterWrite = func() {
			if changeMode {
				f.mode = "off"
			} else {
				f.rows[1].BasePriority = 14
			}
		}
		if err := reconcilePriorities(context.Background(), f, "site"); err != nil {
			t.Fatal(err)
		}
		if changeMode {
			if len(f.writes) != 1 {
				t.Fatal("mode change was ignored")
			}
		} else if len(f.writes) != 2 || *f.writes[1].ProposedPriority != 14 {
			t.Fatal("old target was reused")
		}
	}
}
func TestPriorityCorrectionRetriesFailuresAndSkipsMultiModel(t *testing.T) {
	f := &priorityFake{mode: "auto", fail: true, rows: []tuning.ChannelBaseValue{{ChannelID: 1, ModelName: "m", Models: []string{"m"}, BasePriority: 11}, {ChannelID: 2, ModelName: "m", Models: []string{"m", "n"}, BasePriority: 11}}}
	if err := reconcilePriorities(context.Background(), f, "site"); err == nil {
		t.Fatal("failure hidden")
	}
	f.fail = false
	if err := reconcilePriorities(context.Background(), f, "site"); err != nil {
		t.Fatal(err)
	}
	if len(f.writes) != 2 || f.writes[0].ChannelID != 1 || f.writes[1].ChannelID != 1 {
		t.Fatalf("wrong retry: %#v", f.writes)
	}
}
