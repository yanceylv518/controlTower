package archivecontrol

import (
	af "controltower/internal/archivecontract"
	"strings"
	"testing"
)

func validBackfillControlStatus() Status {
	from, to, _ := af.DateBounds("2026-09-01")
	return Status{
		SiteID: "site-a", AgentID: "agent-a", Session: strings.Repeat("c", 32), State: "running", Configured: true,
		Foundation: &af.FoundationStatus{Identity: af.Identity{SiteID: "site-a", DatasetID: strings.Repeat("a", 32), SourceGenerationID: strings.Repeat("b", 32)}, ProtocolVersion: af.ProtocolVersion, FormatVersion: af.FormatVersion, Capabilities: []string{af.CapabilityFoundation, af.CapabilityAtomicWriter, af.CapabilityBackfill}, WriterEpoch: 9007199254740993},
		Backfill:   &af.BackfillStatus{TaskID: strings.Repeat("d", 32), Date: "2026-09-01", Attempt: 1, WriterEpoch: 9007199254740993, State: "running", AfterCreatedUnix: from, AfterID: 1, ScannedRows: 1, SourceNowUnix: to + 300},
		Metrics:    &af.ArchiveMetrics{Stream: "date_backfill", ReadBytes: 1024},
	}
}

func TestBackfillStatusCannotCrossProtocolSiteOrWriterEpoch(t *testing.T) {
	if !validBackfillControlStatus().Validate() {
		t.Fatal("valid versioned backfill progress rejected")
	}
	for _, tc := range []struct {
		name   string
		change func(*Status)
	}{
		{"legacy-agent", func(v *Status) { v.Foundation = nil }},
		{"foundation-only", func(v *Status) { v.Foundation.Capabilities = []string{af.CapabilityFoundation} }},
		{"p2-agent", func(v *Status) {
			v.Foundation.Capabilities = []string{af.CapabilityFoundation, af.CapabilityAtomicWriter}
		}},
		{"backfill-without-atomic-writer", func(v *Status) { v.Foundation.Capabilities = []string{af.CapabilityFoundation, af.CapabilityBackfill} }},
		{"duplicate-capability", func(v *Status) { v.Foundation.Capabilities = append(v.Foundation.Capabilities, af.CapabilityBackfill) }},
		{"other-site", func(v *Status) { v.SiteID = "site-b" }},
		{"missing-generation", func(v *Status) { v.Foundation.SourceGenerationID = "" }},
		{"old-writer", func(v *Status) { v.Backfill.WriterEpoch-- }},
		{"future-writer", func(v *Status) { v.Backfill.WriterEpoch++ }},
		{"legacy-day-projection", func(v *Status) { v.Days = []Day{{Date: "2026-09-01"}} }},
		{"legacy-reconcile-proof", func(v *Status) { v.Reconciliation = &Reconciliation{State: "matched"} }},
		{"p3-claims-sealed", func(v *Status) { v.Backfill.State = "sealed" }},
		{"metric-secret", func(v *Status) { v.Metrics.ErrorCode = "source_query_failed: password=secret" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			v := validBackfillControlStatus()
			tc.change(&v)
			if v.Validate() {
				t.Fatal("invalid backfill control envelope accepted")
			}
		})
	}
}

func TestBackfillMetricsCannotTurnUnavailableIntoZeroLag(t *testing.T) {
	v := validBackfillControlStatus()
	if v.Metrics.LagSeconds != nil || !v.Validate() {
		t.Fatal("unknown lag must remain representable")
	}
	zero := int64(0)
	v.Metrics.LagSeconds = &zero
	if !v.Validate() {
		t.Fatal("observed zero lag rejected")
	}
	negative := int64(-1)
	v.Metrics.LagSeconds = &negative
	if v.Validate() {
		t.Fatal("invalid negative lag accepted")
	}
}
