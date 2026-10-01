package dashboard

import (
	"context"
	"controltower/server/internal/archivereader"
	"controltower/server/internal/billing"
	"fmt"
	"time"
)

type BillingArchiveReader interface {
	BillingPage(context.Context, string, string, time.Time, time.Time, billing.LogCursor, int64, []int64, int) ([]archivereader.BillingRawLog, error)
}
type BillingSources struct {
	BillingReadonlySource
	Archive BillingArchiveReader
}

func (s BillingSources) ForBillingJob(job billing.Job) billing.PageSource {
	if job.DataSource == "archive" {
		return archiveBillingSource{reader: s.Archive, jobID: job.ID}
	}
	return s.BillingReadonlySource
}

type archiveBillingSource struct {
	reader       BillingArchiveReader
	jobID        string
	versionCheck func(string, string) error
}

func (s archiveBillingSource) page(ctx context.Context, site string, user int64, channels []int64, from, to time.Time, c billing.LogCursor, n int) ([]billing.PagedLogRecord, error) {
	if s.reader == nil {
		return nil, fmt.Errorf("archive billing reader unavailable")
	}
	var raw []archivereader.BillingRawLog
	var err error
	if s.versionCheck != nil {
		reader, ok := s.reader.(interface {
			BillingPageChecked(context.Context, string, time.Time, time.Time, billing.LogCursor, int, func(string, string) error) ([]archivereader.BillingRawLog, error)
		})
		if !ok {
			return nil, billing.PermanentPageError{Err: fmt.Errorf("report_archive_version_unavailable")}
		}
		raw, err = reader.BillingPageChecked(ctx, site, from, to, c, n, s.versionCheck)
	} else {
		raw, err = s.reader.BillingPage(ctx, site, s.jobID, from, to, c, user, channels, n)
	}
	if err != nil {
		return nil, err
	}
	out := make([]billing.PagedLogRecord, 0, len(raw))
	for _, v := range raw {
		out = append(out, normalizeBillingLog(v.Log, v.Other))
	}
	return out, nil
}
func (s archiveBillingSource) LogsPage(ctx context.Context, site string, from, to time.Time, c billing.LogCursor, n int) ([]billing.PagedLogRecord, error) {
	return s.page(ctx, site, 0, nil, from, to, c, n)
}
func (s archiveBillingSource) DetailedLogsPage(ctx context.Context, site string, user int64, from, to time.Time, c billing.LogCursor, n int) ([]billing.PagedLogRecord, error) {
	return s.page(ctx, site, user, nil, from, to, c, n)
}
func (s archiveBillingSource) DetailedChannelsLogsPage(ctx context.Context, site string, channels []int64, from, to time.Time, c billing.LogCursor, n int) ([]billing.PagedLogRecord, error) {
	if len(channels) == 0 {
		return nil, fmt.Errorf("no upstream channels")
	}
	return s.page(ctx, site, 0, channels, from, to, c, n)
}
