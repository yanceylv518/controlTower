package logarchive

import (
	"context"
	af "controltower/internal/archivecontract"
	"database/sql"
	"time"
)

// A selected range has its own durable date/scan cursor. It never advances the
// continuous ID stream, so switching back cannot silently skip older logs.
func (w *Worker) pipelineCollection(ctx context.Context, g af.WriterGrant) (BatchResult, error) {
	var task *af.BackfillTask
	var continuous, done bool
	err := w.pipelineTransaction(ctx, g, func(tx *sql.Tx, p *pipelineCheckpoint) error {
		c := p.State.Settings
		continuous = c.CollectionFrom == ""
		done = p.CollectionDone
		if continuous || done {
			return nil
		}
		if p.CollectionDate == "" {
			p.CollectionDate = c.CollectionFrom
			if c.CollectionNewestFirst {
				p.CollectionDate = c.CollectionThrough
			}
		}
		if p.CollectionScan == nil {
			id, e := newArchiveID()
			if e != nil {
				return e
			}
			policy := af.DefaultCoveragePolicy()
			policy.Budget = w.readBudget()
			policy.CoverageFrom = c.CollectionFrom
			policy.Evidence = "selected_raw_collection_not_verification"
			p.CollectionScan = &af.BackfillTask{Identity: g.Identity, TaskID: id, Date: p.CollectionDate, Type: "date_backfill", Attempt: 1, Policy: policy}
		}
		copy := *p.CollectionScan
		task = &copy
		return nil
	})
	if err != nil {
		return BatchResult{}, err
	}
	if continuous {
		return w.passV2Mode(ctx, g, "", true)
	}
	if done {
		return BatchResult{}, nil
	}
	ctx = workflowOperationContext(ctx, "collection", task.Date)
	before, err := w.scanStatus(ctx, g, *task)
	if err != nil && err != sql.ErrNoRows {
		return BatchResult{}, err
	}
	status, err := w.scanDateMode(ctx, g, *task, true, true)
	if err != nil {
		return BatchResult{}, err
	}
	if status.State == "blocked" {
		return BatchResult{}, &scanError{code: status.ErrorCode}
	}
	result := BatchResult{AfterID: status.AfterID}
	if status.ScannedRows >= before.ScannedRows {
		result.Rows = int(status.ScannedRows - before.ScannedRows)
	}
	if status.State != "succeeded" {
		return result, nil
	}
	err = w.pipelineTransaction(ctx, g, func(tx *sql.Tx, p *pipelineCheckpoint) error {
		if p.CollectionScan == nil || p.CollectionScan.TaskID != task.TaskID {
			return ErrWriterCheckpoint
		}
		c := p.State.Settings
		if (!c.CollectionNewestFirst && p.CollectionDate == c.CollectionThrough) || (c.CollectionNewestFirst && p.CollectionDate == c.CollectionFrom) {
			p.CollectionDone = true
			return nil
		}
		day, _ := time.Parse("2006-01-02", p.CollectionDate)
		step := 1
		if c.CollectionNewestFirst {
			step = -1
		}
		p.CollectionDate = day.AddDate(0, 0, step).Format("2006-01-02")
		p.CollectionScan = nil
		return nil
	})
	return result, err
}
