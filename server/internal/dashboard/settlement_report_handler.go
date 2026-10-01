package dashboard

import (
	"controltower/server/internal/billing"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"time"
)

type SavedReportHandler struct{ Store billing.ReportStore }

func reportRange(from, to string) (time.Time, time.Time, bool) {
	a, e := time.Parse(time.RFC3339, from)
	if e != nil {
		a, e = time.ParseInLocation("2006-01-02", from, billing.BusinessLocation)
	}
	b, f := time.Parse(time.RFC3339, to)
	if f != nil {
		b, f = time.ParseInLocation("2006-01-02", to, billing.BusinessLocation)
	}
	return a, b, e == nil && f == nil && a.Equal(billing.CompleteDayBoundary(a)) && b.Equal(billing.CompleteDayBoundary(b)) && a.Before(b) && b.Sub(a) <= 32*24*time.Hour
}
func (h SavedReportHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if !billingAdminAllowed(r) {
		writeDashboardError(w, 403, "forbidden")
		return
	}
	site := r.URL.Query().Get("instance_id")
	a, b, ok := reportRange(r.URL.Query().Get("from"), r.URL.Query().Get("to"))
	if !ok || site == "" {
		writeDashboardError(w, 400, "invalid_report_range")
		return
	}
	docs, e := h.Store.ReadReportDays(r.Context(), site, a.In(billing.BusinessLocation).Format("2006-01-02"), b.In(billing.BusinessLocation).Format("2006-01-02"))
	if e != nil {
		writeDashboardError(w, 500, "report_read_failed")
		return
	}
	items := []billing.ReportRow{}
	seen := map[string]bool{}
	var currency *billing.CurrencyDisplay
	var failed int64
	known := len(docs) > 0
	for _, d := range docs {
		if currency != nil && (currency.Type != d.Currency.Type || currency.Symbol != d.Currency.Symbol) {
			writeDashboardError(w, 409, "report_currency_mismatch")
			return
		}
		c := d.Currency
		currency = &c
		items = append(items, d.Items...)
		seen[d.From.In(billing.BusinessLocation).Format("2006-01-02")] = true
		if d.FailedRequests == nil {
			known = false
		} else {
			failed += *d.FailedRequests
		}
	}
	missing := []string{}
	expected := 0
	today := billing.CompleteDayBoundary(time.Now())
	for day := a; day.Before(b) && day.Before(today); day = day.AddDate(0, 0, 1) {
		expected++
		s := day.In(billing.BusinessLocation).Format("2006-01-02")
		if !seen[s] {
			missing = append(missing, s)
		}
	}
	var failures *int64
	if known {
		failures = &failed
	}
	writeDashboardJSON(w, 200, map[string]any{"items": items, "currency": currency, "failed_requests": failures, "expected_days": expected, "generated_days": len(docs), "missing_days": missing, "complete": expected > 0 && len(missing) == 0})
}

type ReportTasksHandler struct{ Store billing.ReportStore }

func (h ReportTasksHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodPost {
		writeDashboardError(w, 405, "method_not_allowed")
		return
	}
	if !billingAdminAllowed(r) {
		writeDashboardError(w, 403, "forbidden")
		return
	}
	if r.Method == "GET" {
		site := r.URL.Query().Get("instance_id")
		if site == "" {
			writeDashboardError(w, 400, "instance_required")
			return
		}
		tasks, e := h.Store.ListReportTasks(r.Context(), site, 100)
		if e != nil {
			writeDashboardError(w, 500, "report_tasks_failed")
			return
		}
		writeDashboardJSON(w, 200, map[string]any{"items": tasks})
		return
	}
	var v struct {
		ID        string `json:"id"`
		Site      string `json:"instance_id"`
		From      string `json:"from"`
		To        string `json:"to"`
		Overwrite bool   `json:"overwrite"`
	}
	if json.NewDecoder(http.MaxBytesReader(w, r.Body, 65536)).Decode(&v) != nil || v.Site == "" {
		writeDashboardError(w, 400, "invalid_request")
		return
	}
	if r.URL.Query().Get("action") == "cancel" {
		if e := h.Store.CancelReportTask(r.Context(), v.Site, v.ID); e != nil {
			writeDashboardError(w, 409, "report_cancel_failed")
			return
		}
		writeDashboardJSON(w, 200, map[string]bool{"ok": true})
		return
	}
	if r.URL.Query().Get("action") == "retry" {
		store, ok := h.Store.(billing.ReportCheckpointStore)
		if !ok || v.ID == "" {
			writeDashboardError(w, 400, "report_retry_unavailable")
			return
		}
		if e := store.RetryReportTask(r.Context(), v.Site, v.ID); e != nil {
			code := "report_retry_failed"
			if errors.Is(e, billing.ErrReportBusy) {
				code = "report_task_busy"
			}
			if e.Error() == "report_retry_obsolete" {
				code = "report_retry_obsolete"
			}
			writeDashboardError(w, 409, code)
			return
		}
		writeDashboardJSON(w, 202, map[string]bool{"ok": true})
		return
	}
	if r.URL.Query().Get("action") != "" {
		writeDashboardError(w, 400, "invalid_request")
		return
	}
	a, b, ok := reportRange(v.From, v.To)
	today := billing.CompleteDayBoundary(time.Now())
	if b.After(today) {
		b = today
	}
	if !ok || !a.Before(b) {
		writeDashboardError(w, 400, "report_no_closed_days")
		return
	}
	id := make([]byte, 16)
	if _, e := rand.Read(id); e != nil {
		writeDashboardError(w, 500, "report_create_failed")
		return
	}
	t, e := h.Store.CreateReportTask(r.Context(), billing.ReportTask{ID: hex.EncodeToString(id), Site: v.Site, From: a.In(billing.BusinessLocation).Format("2006-01-02"), To: b.In(billing.BusinessLocation).Format("2006-01-02"), Overwrite: v.Overwrite})
	if e != nil {
		if errors.Is(e, billing.ErrReportBusy) {
			writeDashboardError(w, 409, "report_task_busy")
		} else {
			writeDashboardError(w, 500, "report_create_failed")
		}
		return
	}
	writeDashboardJSON(w, 202, t)
}
