package archivecontract

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func testIdentity() Identity {
	return Identity{SiteID: "site", DatasetID: strings.Repeat("a", 32), SourceGenerationID: strings.Repeat("b", 32)}
}
func TestCatalogWholeRevisionHashAndPrecision(t *testing.T) {
	large := "90071992547409931234567890123456789012"
	s := CatalogSnapshot{Identity: testIdentity(), CatalogRevision: 9007199254740993, Days: []CatalogDay{{Date: "2026-09-02", State: "unknown", AllRows: &large}, {Date: "2026-09-01", State: "unknown"}}}
	h, err := s.Hash()
	if err != nil {
		t.Fatal(err)
	}
	s.Days[0], s.Days[1] = s.Days[1], s.Days[0]
	h2, err := s.Hash()
	if err != nil || h != h2 {
		t.Fatal("whole snapshot hash depends on row order")
	}
	raw, _ := json.Marshal(s)
	if !strings.Contains(string(raw), `"catalog_revision":"9007199254740993"`) || !strings.Contains(string(raw), large) {
		t.Fatal("exact values lost")
	}
	zero := "0"
	s.Days[0].AllRows = &zero
	h3, _ := s.Hash()
	if h3 == h {
		t.Fatal("unknown collapsed into zero")
	}
	s.Days = append(s.Days, s.Days[0])
	if _, err := s.Hash(); err == nil {
		t.Fatal("duplicate date accepted")
	}
}
func TestSealedDayRequiresEvidenceAndExactCounters(t *testing.T) {
	d := CatalogDay{Date: "2026-09-01", State: "sealed"}
	if d.Validate() == nil {
		t.Fatal("sealed without version accepted")
	}
	zero := "0"
	now := time.Now().UTC()
	d.CurrentVersionID = strings.Repeat("a", 32)
	d.ManifestHash = strings.Repeat("b", 64)
	d.AllRows = &zero
	d.ConsumeRows = &zero
	d.ConsumeQuota = &zero
	d.VerifiedAt = &now
	if err := d.Validate(); err != nil {
		t.Fatal(err)
	}
	bad := "1e6"
	d.ConsumeQuota = &bad
	if d.Validate() == nil {
		t.Fatal("nondecimal quota accepted")
	}
	d.ConsumeQuota = &zero
	negative := "-1"
	d.ConsumeQuota = &negative
	if err := d.Validate(); err != nil {
		t.Fatal("signed source quota was discarded", err)
	}
	d.ConsumeQuota = &zero
	d.BlockReason = "unresolved"
	if d.Validate() == nil {
		t.Fatal("blocked sealed accepted")
	}
}
func TestFoundationRejectsUnsupportedWriters(t *testing.T) {
	s := FoundationStatus{Identity: testIdentity(), ProtocolVersion: ProtocolVersion, FormatVersion: FormatVersion, Capabilities: []string{CapabilityFoundation}}
	if err := s.Validate(); err != nil {
		t.Fatal(err)
	}
	s.WriterEpoch = 1
	if s.Validate() == nil {
		t.Fatal("unimplemented writer epoch accepted")
	}
	s.WriterEpoch = 0
	s.ReceiptID = strings.Repeat("a", 32)
	if s.Validate() == nil {
		t.Fatal("unimplemented receipt accepted")
	}
	s.ReceiptID = ""
	s.ProtocolVersion++
	if s.Validate() == nil {
		t.Fatal("future version accepted")
	}
	for _, id := range []string{"", strings.Repeat("0", 32), strings.Repeat("A", 32), "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa"} {
		if _, err := IDBytes(id); err == nil {
			t.Errorf("invalid identity accepted: %q", id)
		}
	}
}
