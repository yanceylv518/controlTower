package dashboard

import (
	"context"
	"controltower/server/internal/billing"
	"crypto/sha256"
	"database/sql"
	"errors"
	"fmt"
	"log"
	"time"
)

type ReportCalculator interface {
	Calculate(context.Context, string, time.Time, time.Time, func(int64) error) (billing.ReportDocument, error)
}

type SettlementReportWorker struct {
	Store      billing.ReportStore
	Calculator ReportCalculator
}

func (w SettlementReportWorker) Run(ctx context.Context) {
	for ctx.Err() == nil {
		lease, release, e := w.Store.LockReportWorker(ctx)
		if e != nil {
			reportPause(ctx, 5*time.Second)
			continue
		}
		w.runLocked(lease)
		release()
		reportPause(ctx, 2*time.Second)
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
		t, d, e := w.Store.NextReportTaskDay(ctx)
		if errors.Is(e, sql.ErrNoRows) {
			reportPause(ctx, 2*time.Second)
			continue
		}
		if e != nil {
			return
		}
		if !t.Overwrite {
			exists, e := w.Store.ReportDayExists(ctx, t.Site, d.Day)
			if e != nil {
				return
			}
			if exists {
				if w.Store.FinishReportTaskDay(ctx, t, d, nil, "reused") != nil {
					return
				}
				continue
			}
		}
		day, _ := time.ParseInLocation("2006-01-02", d.Day, billing.BusinessLocation)
		run, cancel := context.WithTimeout(ctx, 30*time.Minute)
		done := make(chan struct{})
		go func() {
			defer close(done)
			ticker := time.NewTicker(2 * time.Second)
			defer ticker.Stop()
			for {
				select {
				case <-run.Done():
					return
				case <-ticker.C:
					if w.Store.ReportTaskProgress(run, t.ID, d.Day, 0) != nil {
						cancel()
						return
					}
				}
			}
		}()
		doc, err := w.Calculator.Calculate(run, t.Site, day, day.AddDate(0, 0, 1), func(n int64) error { return w.Store.ReportTaskProgress(run, t.ID, d.Day, n) })
		cancel()
		<-done
		if ctx.Err() != nil {
			return
		}
		message := ""
		var result *billing.ReportDocument
		if err == nil {
			result = &doc
		} else {
			message = err.Error()
			if errors.Is(err, context.Canceled) {
				message = "生成已中断，请重试"
			}
			if errors.Is(err, context.DeadlineExceeded) {
				message = "生成超时，请重试"
			}
		}
		if e = w.Store.FinishReportTaskDay(ctx, t, d, result, message); e != nil && !errors.Is(e, billing.ErrReportCancelled) {
			log.Printf("report save failed: %v", e)
			return
		}
	}
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
