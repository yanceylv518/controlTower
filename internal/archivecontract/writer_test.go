package archivecontract

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestAtomicWriterCapabilityRequiresFoundationAndValidReceipt(t *testing.T) {
	s := FoundationStatus{Identity: testIdentity(), ProtocolVersion: 2, FormatVersion: 2, Capabilities: []string{CapabilityFoundation, CapabilityAtomicWriter}, WriterEpoch: 9007199254740993, ReceiptID: strings.Repeat("c", 32)}
	if err := s.Validate(); err != nil {
		t.Fatal(err)
	}
	b, _ := json.Marshal(s)
	if !strings.Contains(string(b), `"writer_epoch":"9007199254740993"`) {
		t.Fatal("epoch precision lost")
	}
	s.WriterEpoch = 0
	if s.Validate() == nil {
		t.Fatal("receipt without writer epoch accepted")
	}
	s.WriterEpoch = 1
	s.Capabilities = []string{CapabilityAtomicWriter}
	if s.Validate() == nil {
		t.Fatal("writer without foundation accepted")
	}
	s.Capabilities = []string{CapabilityFoundation, CapabilityAtomicWriter, CapabilityAtomicWriter}
	if s.Validate() == nil {
		t.Fatal("duplicate capability accepted")
	}
}
