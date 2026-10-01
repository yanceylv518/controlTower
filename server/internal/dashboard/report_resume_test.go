package dashboard

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"controltower/server/internal/archivereader"
	"controltower/server/internal/billing"
)

type reportCalculateFunc func(context.Context, func(int64) error) (billing.ReportDocument, error)

func (f reportCalculateFunc) Calculate(ctx context.Context, _ string, _, _ time.Time, p func(int64) error) (billing.ReportDocument, error) {
	return f(ctx, p)
}

type reportVersionPages struct {
	reportResumePages
	version      string
	countVersion string
}

func (s *reportVersionPages) BillingPageChecked(ctx context.Context, site string, from, to time.Time, c billing.LogCursor, n int, check func(string, string) error) ([]archivereader.BillingRawLog, error) {
	if err := check("archive-source", s.version); err != nil {
		return nil, err
	}
	return s.BillingPage(ctx, site, "", from, to, c, 0, nil, n)
}
func (s *reportVersionPages) BillingFailedRequestsChecked(_ context.Context, _ string, _, _ time.Time, check func(string, string) error) (int64, error) {
	version := s.version
	if s.countVersion != "" {
		version = s.countVersion
	}
	return 3, check("archive-source", version)
}

func TestReportArchiveCheckpointPinsVersionBeforeReading(t *testing.T) {
	cp := resumeCheckpoint()
	source := &reportVersionPages{reportResumePages: reportResumePages{total: 1}, version: "v1"}
	h := SettlementReportHandler{Source: BillingSources{Archive: source}}
	var saved *billing.ReportCheckpoint
	_, err := h.calculate(context.Background(), cp.Site, cp.From, cp.To, cp, func(v *billing.ReportCheckpoint) error {
		if v.ArchiveVersion != "" && v.Processed == 0 && len(source.calls) != 0 {
			t.Fatal("identity saved after source page")
		}
		saved = cloneCheckpoint(t, v)
		return nil
	}, func(int64) error { return errors.New("interrupt after first page") })
	if err == nil || saved == nil || saved.ArchiveVersion != "v1" || saved.Processed != 0 {
		t.Fatal(err, saved)
	}
	source.version = "v2"
	source.calls = nil
	_, err = h.calculate(context.Background(), saved.Site, saved.From, saved.To, saved, nil, nil)
	if !errors.Is(err, errReportArchiveChanged) || len(source.calls) != 0 {
		t.Fatal("mixed archive revisions", err)
	}
}

func TestReportArchiveVersionChangeOnEmptyPageAndFailureCount(t *testing.T) {
	for _, complete := range []bool{false, true} {
		cp := resumeCheckpoint()
		cp.ArchiveSource = "archive-source"
		cp.ArchiveVersion = "v1"
		cp.ScanComplete = complete
		source := &reportVersionPages{version: "v2"}
		h := SettlementReportHandler{Source: BillingSources{Archive: source}}
		_, err := h.calculate(context.Background(), cp.Site, cp.From, cp.To, cp, nil, nil)
		if !errors.Is(err, errReportArchiveChanged) {
			t.Fatalf("complete=%v: %v", complete, err)
		}
	}
}

type reportCountCheckPages struct {
	reportVersionPages
	countErr    error
	beforeCheck bool
}

func (s *reportCountCheckPages) BillingFailedRequestsChecked(ctx context.Context, site string, from, to time.Time, check func(string, string) error) (int64, error) {
	if s.beforeCheck {
		return 0, s.countErr
	}
	n, err := s.reportVersionPages.BillingFailedRequestsChecked(ctx, site, from, to, check)
	if err != nil {
		return 0, err
	}
	return n, s.countErr
}

func TestReportArchiveMustBeVerifiedBeforePublication(t *testing.T) {
	for _, complete := range []bool{false, true} {
		for _, test := range []struct {
			name string
			err  error
		}{
			{"unsealed day", archivereader.ErrVersion},
			{"identity mismatch", archivereader.ErrIdentity},
			{"archive unavailable", errors.New("archive unavailable")},
			{"missing verification", nil},
		} {
			t.Run(fmt.Sprintf("complete=%v/%s", complete, test.name), func(t *testing.T) {
				cp := resumeCheckpoint()
				cp.ArchiveSource, cp.ArchiveVersion = "archive-source", "v1"
				cp.ScanComplete = complete
				source := &reportCountCheckPages{reportVersionPages: reportVersionPages{version: "v1"}, countErr: test.err, beforeCheck: true}
				h := SettlementReportHandler{Source: BillingSources{Archive: source}}
				_, err := h.calculate(context.Background(), cp.Site, cp.From, cp.To, cp, nil, nil)
				if err == nil || !strings.HasPrefix(err.Error(), "report_archive_version_unavailable") {
					t.Fatalf("unverified archive published: %v", err)
				}
			})
		}
	}
}

func TestReportArchiveVerifiedCountFailureRemainsOptional(t *testing.T) {
	cp := resumeCheckpoint()
	cp.ArchiveSource, cp.ArchiveVersion = "archive-source", "v1"
	cp.ScanComplete = true
	source := &reportCountCheckPages{reportVersionPages: reportVersionPages{version: "v1"}, countErr: errors.New("COUNT timed out")}
	h := SettlementReportHandler{Source: BillingSources{Archive: source}}
	doc, err := h.calculate(context.Background(), cp.Site, cp.From, cp.To, cp, nil, nil)
	if err != nil || doc.FailedRequests != nil {
		t.Fatalf("verified snapshot blocked by optional count: %+v, %v", doc, err)
	}
}

func TestReportFailureMessageBoundedAndSafe(t *testing.T) {
	wrapped := fmt.Errorf("report_source_read_failed: %w", errors.New(strings.Repeat("private database detail", 100)))
	if reportFailureMessage(wrapped) != "report_source_read_failed" {
		t.Fatal("raw wrapped source error exposed")
	}
	if len([]rune(reportFailureMessage(errors.New(strings.Repeat("错", 600))))) > 512 {
		t.Fatal("failure cannot fit in storage")
	}
}

func TestReportIdleBudgetRenewsOnlyOnProgress(t *testing.T) {
	task := billing.ReportTask{ID: "task", Site: "site"}
	day := billing.ReportTaskDay{Day: "2025-01-01"}
	worker := SettlementReportWorker{Store: &reportWorkerFake{}, idleTimeout: 150 * time.Millisecond, heartbeatInterval: 10 * time.Millisecond}
	worker.Calculator = reportCalculateFunc(func(ctx context.Context, p func(int64) error) (billing.ReportDocument, error) {
		if _, ok := ctx.Deadline(); ok {
			t.Error("unexpected total runtime deadline")
		}
		for n := int64(1); n <= 10; n++ {
			select {
			case <-ctx.Done():
				return billing.ReportDocument{}, ctx.Err()
			case <-time.After(35 * time.Millisecond):
			}
			if err := p(n); err != nil {
				return billing.ReportDocument{}, err
			}
		}
		return billing.ReportDocument{}, nil
	})
	if _, err := worker.calculateDay(context.Background(), task, day); err != nil {
		t.Fatal("progressing day exceeded idle budget", err)
	}
	worker.Calculator = reportCalculateFunc(func(ctx context.Context, p func(int64) error) (billing.ReportDocument, error) {
		for {
			select {
			case <-ctx.Done():
				return billing.ReportDocument{}, ctx.Err()
			case <-time.After(20 * time.Millisecond):
				_ = p(0)
			}
		}
	})
	if _, err := worker.calculateDay(context.Background(), task, day); !errors.Is(err, errReportStalled) {
		t.Fatal("heartbeat/unchanged count renewed budget", err)
	}
}

type reportResumePages struct {
	calls  []billing.LogCursor
	total  int64
	failAt int64
}

func (s *reportResumePages) BillingPageChecked(ctx context.Context, site string, from, to time.Time, c billing.LogCursor, n int, check func(string, string) error) ([]archivereader.BillingRawLog, error) {
	if err := check("archive-source", "v1"); err != nil {
		return nil, err
	}
	return s.BillingPage(ctx, site, "", from, to, c, 0, nil, n)
}

func (s *reportResumePages) BillingFailedRequestsChecked(_ context.Context, _ string, _, _ time.Time, check func(string, string) error) (int64, error) {
	return 0, check("archive-source", "v1")
}

func (s *reportResumePages) BillingPage(ctx context.Context, _, _ string, from, _ time.Time, c billing.LogCursor, _ int64, _ []int64, n int) ([]archivereader.BillingRawLog, error) {
	s.calls = append(s.calls, c)
	out := []archivereader.BillingRawLog{}
	for id := c.ID + 1; id <= s.total && len(out) < n; id++ {
		quota := int64(13)
		if id == s.failAt {
			quota = -1
		}
		out = append(out, archivereader.BillingRawLog{Log: billing.PagedLogRecord{ID: id, CreatedUnix: from.Unix() + 1, UserID: 7, Username: "frozen user", ChannelID: 3, ChannelName: "channel", ModelName: "model", Quota: quota, PromptTokens: sql.NullInt64{Int64: 10, Valid: true}, CompletionTokens: sql.NullInt64{Int64: 2, Valid: true}}, Other: `{}`})
	}
	return out, nil
}
func resumeCheckpoint() *billing.ReportCheckpoint {
	from := time.Date(2025, 1, 1, 0, 0, 0, 0, billing.BusinessLocation)
	return &billing.ReportCheckpoint{Version: 1, Site: "site", From: from, To: from.AddDate(0, 0, 1), DataSource: "archive", Money: &billing.MoneySnapshot{QuotaPerUnit: "500000", Display: billing.CurrencyDisplay{Type: "CNY", Symbol: "¥", ExchangeRate: "7.3"}}, Discounts: []billing.StatementDiscount{{DiscountType: billing.DiscountUpstreamChannel, SubjectID: 9, ChannelID: 3, Discount: "0.375", EffectiveFrom: from.Add(-time.Hour)}}, Upstreams: []billing.Upstream{{ID: 9, Name: "frozen upstream", Channels: []billing.UpstreamChannel{{ChannelID: 3}}}}, Groups: map[string]billing.ReportAccumulator{}}
}
func cloneCheckpoint(t *testing.T, cp *billing.ReportCheckpoint) *billing.ReportCheckpoint {
	t.Helper()
	b, e := json.Marshal(cp)
	if e != nil {
		t.Fatal(e)
	}
	var out billing.ReportCheckpoint
	if e = json.Unmarshal(b, &out); e != nil {
		t.Fatal(e)
	}
	return &out
}
func TestReportCheckpointResumeExactTotalsAndFrozenSettings(t *testing.T) {
	source := &reportResumePages{total: 20103}
	// Store and live money source are deliberately nil: resumed calculations
	// must exclusively use their original snapshots.
	h := SettlementReportHandler{Source: BillingSources{Archive: source}}
	cp := resumeCheckpoint()
	var saved *billing.ReportCheckpoint
	save := func(v *billing.ReportCheckpoint) error { saved = cloneCheckpoint(t, v); return nil }
	interrupted := errors.New("injected interruption after unsaved page")
	_, err := h.calculate(context.Background(), cp.Site, cp.From, cp.To, cp, save, func(n int64) error {
		if n > 20000 {
			return interrupted
		}
		return nil
	})
	if !errors.Is(err, interrupted) || saved == nil || saved.Processed != 20000 || saved.ScanComplete {
		t.Fatal(err, saved)
	}
	source.calls = nil
	restored := cloneCheckpoint(t, saved)
	doc, err := h.calculate(context.Background(), restored.Site, restored.From, restored.To, restored, save, nil)
	if err != nil {
		t.Fatal(err)
	}
	if source.calls[0].ID != 20000 || !saved.ScanComplete || saved.Processed != source.total {
		t.Fatal(source.calls, saved.Processed)
	}
	baseline := resumeCheckpoint()
	want, err := h.calculate(context.Background(), baseline.Site, baseline.From, baseline.To, baseline, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(doc.Items, want.Items) || !reflect.DeepEqual(doc.MoneySnapshot, want.MoneySnapshot) || len(doc.Items) != 1 || doc.Items[0].Requests != 20103 || doc.Items[0].Upstream != "frozen upstream" {
		t.Fatalf("resume mismatch: %+v / %+v", doc, want)
	}
	source.calls = nil
	ready := cloneCheckpoint(t, saved)
	again, err := h.calculate(context.Background(), ready.Site, ready.From, ready.To, ready, nil, nil)
	if err != nil || len(source.calls) != 0 || !reflect.DeepEqual(doc.Items, again.Items) {
		t.Fatal("complete checkpoint rescanned source", err)
	}
}

func TestReportCheckpointDoesNotPersistPartialPage(t *testing.T) {
	source := &reportResumePages{total: 20103, failAt: 20050}
	h := SettlementReportHandler{Source: BillingSources{Archive: source}}
	cp := resumeCheckpoint()
	var saved *billing.ReportCheckpoint
	_, err := h.calculate(context.Background(), cp.Site, cp.From, cp.To, cp, func(v *billing.ReportCheckpoint) error { saved = cloneCheckpoint(t, v); return nil }, nil)
	if err == nil || saved == nil || saved.Processed != 20000 {
		t.Fatal("saved partial page", err)
	}
	var requests int64
	for _, v := range saved.Groups {
		requests += v.Row.Requests
	}
	if requests != 20000 {
		t.Fatalf("partial page contaminated durable totals: %d", requests)
	}
}
