package dashboard

import (
	"context"
	"controltower/server/internal/billing"
	"fmt"
	"testing"
	"time"
)

type reportWorkerFake struct {
	billing.ReportStore
	next, finished int
	cancel         context.CancelFunc
	failed         bool
}

func (s *reportWorkerFake) RecoverReportTasks(context.Context) error           { return nil }
func (s *reportWorkerFake) ListBillingSites(context.Context) ([]string, error) { return nil, nil }
func (s *reportWorkerFake) NextReportTaskDay(context.Context) (billing.ReportTask, billing.ReportTaskDay, error) {
	s.next++
	return billing.ReportTask{ID: "task", Site: "site", Overwrite: true}, billing.ReportTaskDay{Day: fmt.Sprintf("2025-01-%02d", s.next)}, nil
}
func (s *reportWorkerFake) ReportTaskProgress(context.Context, string, string, int64) error {
	return nil
}
func (s *reportWorkerFake) FinishReportTaskDay(_ context.Context, _ billing.ReportTask, _ billing.ReportTaskDay, doc *billing.ReportDocument, reason string) error {
	s.finished++
	if s.finished == 1 {
		s.failed = doc == nil && reason != ""
	}
	if s.finished == 2 {
		s.cancel()
	}
	return nil
}

type reportCalculatorFake struct{ n int }

func (c *reportCalculatorFake) Calculate(ctx context.Context, site string, from, to time.Time, progress func(int64) error) (billing.ReportDocument, error) {
	c.n++
	if c.n == 1 {
		return billing.ReportDocument{}, fmt.Errorf("report_source_read_failed")
	}
	return billing.ReportDocument{From: from, To: to}, nil
}
func TestReportWorkerContinuesAfterFailedDay(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	store := &reportWorkerFake{cancel: cancel}
	worker := SettlementReportWorker{Store: store, Calculator: &reportCalculatorFake{}}
	worker.runLocked(ctx)
	if store.finished != 2 || !store.failed {
		t.Fatalf("finished=%d failed=%v", store.finished, store.failed)
	}
}
