package dashboard

import (
	"context"
	"controltower/server/internal/billing"
	"crypto/sha256"
	"database/sql"
	"errors"
	"fmt"
	"log"
	"strings"
	"sync/atomic"
	"time"
)

type ReportCalculator interface {
	Calculate(context.Context, string, time.Time, time.Time, func(int64) error) (billing.ReportDocument, error)
}

type SettlementReportWorker struct {
	Store             billing.ReportStore
	Calculator        ReportCalculator
	idleTimeout       time.Duration
	heartbeatInterval time.Duration
}

// Schedule only creates tasks; the site coordinator owns all execution.
func (w SettlementReportWorker) Schedule(ctx context.Context) {
	for ctx.Err() == nil {
		w.schedule(ctx)
		reportPause(ctx, time.Minute)
	}
}
func reportPause(ctx context.Context, d time.Duration) {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
	case <-t.C:
	}
}
func (w SettlementReportWorker) runLocked(ctx context.Context) {
	if w.Store.RecoverReportTasks(ctx) != nil {
		return
	}
	nextAuto := time.Time{}
	for ctx.Err() == nil {
		if time.Now().After(nextAuto) {
			w.schedule(ctx)
			nextAuto = time.Now().Add(time.Minute)
		}
		worked, err := w.RunOnce(ctx)
		if err != nil {
			return
		}
		if !worked {
			reportPause(ctx, 2*time.Second)
		}
	}
}

// RunOnce executes one date of the task selected by the site coordinator.
func (w SettlementReportWorker) RunOnce(ctx context.Context) (bool, error) {
	t, d, e := w.Store.NextReportTaskDay(ctx)
	if errors.Is(e, sql.ErrNoRows) {
		return false, nil
	}
	if e != nil {
		return false, e
	}
	if !t.Overwrite {
		exists, e := w.Store.ReportDayExists(ctx, t.Site, d.Day)
		if e != nil {
			return false, e
		}
		if exists {
			if e = w.Store.FinishReportTaskDay(ctx, t, d, nil, "reused"); e != nil {
				return false, e
			}
			return true, nil
		}
	}
	doc, err := w.calculateDay(ctx, t, d)
	if ctx.Err() != nil {
		return false, ctx.Err()
	}
	message := ""
	var result *billing.ReportDocument
	if err == nil {
		result = &doc
	} else {
		message = reportFailureMessage(err)
		if errors.Is(err, context.Canceled) {
			message = "生成已中断，请重试"
		}
		if errors.Is(err, errReportStalled) {
			message = "长时间无处理进展，已暂停，可继续生成"
		}
		if errors.Is(err, context.DeadlineExceeded) {
			message = "读取或保存超时，可继续生成"
		}
	}
	if e = w.Store.FinishReportTaskDay(ctx, t, d, result, message); e != nil && !errors.Is(e, billing.ErrReportCancelled) {
		log.Printf("report save failed: %v", e)
		return false, e
	}
	return true, nil
}

func (w SettlementReportWorker) schedule(ctx context.Context) {
	today := billing.CompleteDayBoundary(time.Now())
	if time.Now().Before(today.Add(5 * time.Minute)) {
		return
	}
	sites, e := w.Store.ListBillingSites(ctx)
	if e != nil {
		return
	}
	day := today.AddDate(0, 0, -1).Format("2006-01-02")
	for _, site := range sites {
		exists, e := w.Store.ReportDayExists(ctx, site, day)
		if e != nil || exists {
			continue
		}
		sum := sha256.Sum256([]byte(site + "|" + day))
		_, _ = w.Store.CreateReportTask(ctx, billing.ReportTask{ID: fmt.Sprintf("auto-%x", sum[:20]), Site: site, From: day, To: today.Format("2006-01-02"), Automatic: true})
	}
}

var errReportStalled = errors.New("report stalled")

// Wrapped database/source errors can exceed the task's VARCHAR(512). Store a
// bounded public reason so recording failure cannot fail and requeue forever.
func reportFailureMessage(err error) string {
	if errors.Is(err, errReportArchiveChanged) {
		return "归档版本已变化，请新建报表任务，不能继续旧进度"
	}
	message := err.Error()
	for _, code := range []string{"report_source_read_failed", "report_source_index_unavailable", "report_checkpoint_save_failed", "report_checkpoint_load_failed", "report_checkpoint_invalid", "report_archive_version_unavailable"} {
		if strings.HasPrefix(message, code) {
			return code
		}
	}
	text := []rune(message)
	if len(text) > 512 {
		message = string(text[:512])
	}
	return message
}

// Only real page progress renews the idle budget. A healthy long-running day
// has no wall-clock limit; heartbeat updates alone cannot keep a stuck job alive.
func (w SettlementReportWorker) calculateDay(ctx context.Context, t billing.ReportTask, d billing.ReportTaskDay) (billing.ReportDocument, error) {
	idle := w.idleTimeout
	if idle <= 0 {
		idle = 15 * time.Minute
	} // Exceeds the existing ten-minute page retry budget.
	interval := w.heartbeatInterval
	if interval <= 0 {
		interval = 2 * time.Second
	}
	run, cancel := context.WithCancelCause(ctx)
	defer cancel(nil)
	var last atomic.Int64
	started := time.Now()
	processed := d.Processed
	done := make(chan struct{})
	go func() {
		defer close(done)
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-run.Done():
				return
			case <-ticker.C:
				if time.Since(started)-time.Duration(last.Load()) >= idle {
					cancel(errReportStalled)
					return
				}
				heartbeat, stop := context.WithTimeout(run, 5*time.Second)
				err := w.Store.ReportTaskProgress(heartbeat, t.ID, d.Day, 0)
				stop()
				if err != nil {
					cancel(err)
					return
				}
			}
		}
	}()
	progress := func(n int64) error {
		if err := w.Store.ReportTaskProgress(run, t.ID, d.Day, n); err != nil {
			return err
		}
		if n > processed {
			processed = n
			last.Store(int64(time.Since(started)))
		}
		return nil
	}
	var doc billing.ReportDocument
	var err error
	if calculator, ok := w.Calculator.(interface {
		CalculateTask(context.Context, billing.ReportTask, billing.ReportTaskDay, func(int64) error) (billing.ReportDocument, error)
	}); ok {
		doc, err = calculator.CalculateTask(run, t, d, progress)
	} else {
		day, _ := time.ParseInLocation("2006-01-02", d.Day, billing.BusinessLocation)
		doc, err = w.Calculator.Calculate(run, t.Site, day, day.AddDate(0, 0, 1), progress)
	}
	// Use a distinct normal-stop cause; a concurrent monitor failure must not
	// be lost between reading Cause and waiting for the heartbeat goroutine.
	finished := errors.New("report calculation finished")
	cancel(finished)
	<-done
	if cause := context.Cause(run); cause != nil && cause != finished {
		return billing.ReportDocument{}, cause
	}
	return doc, err
}
