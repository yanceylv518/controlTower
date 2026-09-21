package logarchive

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestReconcileV4DigestRestartAndExactTotals(t *testing.T) {
	columns := []string{"id", "created_at", "type", "quota", "prompt_tokens", "completion_tokens", "other"}
	state := newReconcileScans()
	row := []any{"9007199254740993", "1788278401", "2", "9223372036854775807", "1", nil, string([]byte{0xff, 0x00})}
	if _, err := state.Scans[0].add(columns, row); err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(state)
	if err != nil {
		t.Fatal(err)
	}
	var restored reconcileScans
	if json.Unmarshal(encoded, &restored) != nil || !restored.valid() {
		t.Fatal("persistent digest could not resume")
	}
	row[0] = "9007199254740994"
	row[6] = nil
	if _, err := restored.Scans[0].add(columns, row); err != nil {
		t.Fatal(err)
	}
	if _, err := state.Scans[0].add(columns, row); err != nil {
		t.Fatal(err)
	}
	if !sameReconcileSummary(restored.Scans[0].Summary, state.Scans[0].Summary) {
		t.Fatal("restart changes digest")
	}
	s := restored.Scans[0].Summary
	if s.Rows != 2 || s.Quota != "18446744073709551614" || s.ConsumeQuota != s.Quota || s.Nulls["completion_tokens"] != 2 || s.Nulls["other"] != 1 || s.Types["2"] != 2 {
		t.Fatalf("precision or NULL semantics lost: %+v", s)
	}
	restored.Scans[0].Summary.Digest = strings.Repeat("0", 64)
	if restored.valid() {
		t.Fatal("corrupt digest checkpoint accepted")
	}
}

func TestReconcileV4HistogramJSONPrecision(t *testing.T) {
	const large uint64 = 9007199254740993
	summary := newReconcileScans().Scans[0].Summary
	summary.Types["2"] = large
	summary.Nulls["other"] = ^uint64(0)
	raw, err := json.Marshal(summary)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), `"types":{"2":"9007199254740993"}`) || !strings.Contains(string(raw), `"nulls":{"other":"18446744073709551615"}`) {
		t.Fatalf("histogram is not encoded as decimal strings: %s", raw)
	}
	for _, encoded := range [][]byte{raw, []byte(strings.ReplaceAll(strings.ReplaceAll(string(raw), `"9007199254740993"`, `9007199254740993`), `"18446744073709551615"`, `18446744073709551615`))} {
		var restored ReconcileSummary
		if json.Unmarshal(encoded, &restored) != nil || restored.Types["2"] != large || restored.Nulls["other"] != ^uint64(0) {
			t.Fatalf("histogram precision lost: %+v", restored)
		}
	}
	for _, invalid := range []string{`-1`, `1.5`, `1e3`, `18446744073709551616`, `null`, `"-1"`, `"18446744073709551616"`} {
		var counts reconcileCounts
		if json.Unmarshal([]byte(`{"2":`+invalid+`}`), &counts) == nil {
			t.Fatalf("invalid count accepted: %s", invalid)
		}
	}
}
