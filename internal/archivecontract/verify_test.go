package archivecontract

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestVerificationDoesNotPromoteUnsupportedClaims(t *testing.T) {
	_, end, _ := DateBounds("2026-09-01")
	a := VerificationAssurance{StableBeforeUnix: end, ValidUntilUnix: end + 3600, Evidence: "site operator statement"}
	if !a.Covers("2026-09-01", end) || a.Covers("2026-09-01", end+3600) || a.Covers("2026-09-02", end+3600) {
		t.Fatal("assurance bounds are not enforced")
	}
	s := ReconcileStatus{TaskID: strings.Repeat("a", 32), RunID: strings.Repeat("b", 32), Date: "2026-09-01", Attempt: 1, WriterEpoch: 1, ProgressVersion: 1, State: "matched", Phase: "completed", Method: "stable_window_paged", SourceDigest: strings.Repeat("c", 64), TargetDigest: strings.Repeat("c", 64), SourceRows: 9007199254740993, TargetRows: 9007199254740993}
	if s.Validate() != nil {
		t.Fatal("valid stable-history run rejected")
	}
	for _, mutate := range []func(*ReconcileStatus){func(v *ReconcileStatus) { v.IssueCount = 1 }, func(v *ReconcileStatus) { v.FinalRevision++ }, func(v *ReconcileStatus) { v.TargetRows++ }, func(v *ReconcileStatus) { v.SourceDigest = "" }, func(v *ReconcileStatus) { v.Method = "snapshot" }, func(v *ReconcileStatus) { v.ProgressVersion = 0 }, func(v *ReconcileStatus) { v.State = "running" }} {
		copy := s
		mutate(&copy)
		if copy.Validate() == nil {
			t.Fatal("unsupported matched proof accepted")
		}
	}
	raw, _ := json.Marshal(s)
	var decoded ReconcileStatus
	if json.Unmarshal(raw, &decoded) != nil || decoded.SourceRows != s.SourceRows {
		t.Fatal("large row count lost precision")
	}
}

func TestSealSuccessRequiresCommittedProgress(t *testing.T) {
	s := SealStatus{TaskID: strings.Repeat("a", 32), BuildID: strings.Repeat("b", 32), Attempt: 1, WriterEpoch: 1, State: "succeeded", CatalogRevision: 1, Versions: []SealedDay{{Date: "2026-09-01", VersionID: strings.Repeat("c", 32), ManifestHash: strings.Repeat("d", 64), VersionNo: 1}}}
	if s.Validate() == nil {
		t.Fatal("uninitialized seal claimed success")
	}
	s.ProgressVersion = 1
	if s.Validate() != nil {
		t.Fatal("committed seal rejected")
	}
}
func TestManifestCanonicalHashSurvivesMySQLFormatting(t *testing.T) {
	a := []byte(`{"b":"9007199254740993","a":{"x":1,"q":"0.1234567890123456789"}}`)
	b := []byte(`{ "a": { "q": "0.1234567890123456789", "x": 1 }, "b": "9007199254740993" }`)
	x, e := ManifestHash(a)
	y, e2 := ManifestHash(b)
	if e != nil || e2 != nil || x != y {
		t.Fatal("MySQL JSON formatting changed canonical manifest hash")
	}
	if _, e = ManifestHash(append(a, []byte(` {}`)...)); e == nil {
		t.Fatal("extra manifest JSON accepted")
	}
}

func TestCopiedSourceVerificationContract(t *testing.T) {
	s := ReconcileStatus{TaskID: strings.Repeat("a", 32), RunID: strings.Repeat("b", 32), Date: "2026-09-01", Attempt: 1, WriterEpoch: 1, ProgressVersion: 1, State: "matched", Phase: "completed", Method: "copied_source_paged", SourceDigest: strings.Repeat("c", 64), TargetDigest: strings.Repeat("c", 64), SourceRows: 1, TargetRows: 1}
	if s.Validate() != nil {
		t.Fatal("copied source proof rejected")
	}
	s.TargetRows = 2
	if s.Validate() == nil {
		t.Fatal("unequal rows accepted")
	}
	s.TargetRows = 1
	s.State = "running"
	s.Phase = "source_first"
	if s.Validate() == nil {
		t.Fatal("copied evidence claims source rescan")
	}
	s.Phase = "target_second"
	if s.Validate() != nil {
		t.Fatal("target-only page rejected")
	}
}
