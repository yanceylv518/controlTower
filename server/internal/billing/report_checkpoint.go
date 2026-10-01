package billing

import (
	"context"
	"time"
)

// ReportCheckpoint contains aggregates, never source orders. Exact rational
// strings avoid rounding again when a completed page is replayed after restart.
type ReportAccumulator struct {
	Row             ReportRow `json:"row"`
	Amount          string    `json:"amount"`
	Cost            string    `json:"cost"`
	Raw             string    `json:"raw"`
	UnknownOriginal bool      `json:"unknown_original"`
}
type ReportCheckpoint struct {
	Version        int                          `json:"version"`
	Site           string                       `json:"site"`
	From           time.Time                    `json:"from"`
	To             time.Time                    `json:"to"`
	DataSource     string                       `json:"data_source"`
	ArchiveSource  string                       `json:"archive_source,omitempty"`
	ArchiveVersion string                       `json:"archive_version,omitempty"`
	Money          *MoneySnapshot               `json:"money"`
	Discounts      []StatementDiscount          `json:"discounts"`
	Upstreams      []Upstream                   `json:"upstreams"`
	Cursor         LogCursor                    `json:"cursor"`
	Processed      int64                        `json:"processed"`
	ScanComplete   bool                         `json:"scan_complete"`
	Groups         map[string]ReportAccumulator `json:"groups"`
}
type ReportCheckpointStore interface {
	LoadReportCheckpoint(context.Context, string, string) (*ReportCheckpoint, error)
	SaveReportCheckpoint(context.Context, string, string, *ReportCheckpoint) error
	RetryReportTask(context.Context, string, string) error
}
