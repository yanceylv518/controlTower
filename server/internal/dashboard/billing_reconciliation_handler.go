package dashboard

import (
	"context"
	"encoding/csv"
	"net/http"
	"strconv"
	"strings"
	"time"

	ctauth "controltower/server/internal/auth"
	"controltower/server/internal/billing"
)

type BillingReconciliationHandler struct {
	Store BillingSummaryStore
}

func (h BillingReconciliationHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet || !billingReconciliationAdmin(r) {
		writeDashboardError(w, http.StatusForbidden, "forbidden")
		return
	}
	instanceID := strings.TrimSpace(r.URL.Query().Get("instance_id"))
	from, to, _, err := billingPeriodQuery(r)
	if instanceID == "" || err != nil {
		writeDashboardError(w, http.StatusBadRequest, "invalid_query")
		return
	}
	job, err := billingJobForRead(r, h.Store, instanceID, "generate", from, to)
	if err != nil || job.Status != "complete" {
		writeDashboardError(w, http.StatusConflict, "billing_not_generated")
		return
	}
	var userIDs []int64
	userID, _ := strconv.ParseInt(strings.TrimSpace(r.URL.Query().Get("user_id")), 10, 64)
	if userID > 0 {
		userIDs = []int64{userID}
	}
	rows, err := h.Store.QueryBillingAggregatesForJob(r.Context(), job.ID, userIDs)
	if err != nil {
		writeDashboardError(w, http.StatusInternalServerError, "billing_query_failed")
		return
	}
	prices, err := h.Store.ListBillingPrices(r.Context(), instanceID)
	if err != nil {
		writeDashboardError(w, http.StatusInternalServerError, "billing_query_failed")
		return
	}
	ratios, err := h.Store.ListBillingGroupRatios(r.Context(), instanceID)
	if err != nil {
		writeDashboardError(w, http.StatusInternalServerError, "billing_query_failed")
		return
	}
	snapshots, err := h.Store.BillingRatioSnapshots(r.Context(), instanceID, from, to)
	if err != nil {
		writeDashboardError(w, http.StatusInternalServerError, "billing_query_failed")
		return
	}
	anomalies, err := anomalyCounts(h.Store, r.Context(), job.ID)
	if err != nil {
		writeDashboardError(w, http.StatusInternalServerError, "billing_query_failed")
		return
	}
	if userID > 0 {
		filtered := anomalies[:0]
		for _, item := range anomalies {
			if item.UserID == userID {
				filtered = append(filtered, item)
			}
		}
		anomalies = filtered
	}
	if r.URL.Query().Get("format") == "csv" {
		writeBillingReconciliationCSV(w, billing.BuildReconciliation(rows, prices, ratios, snapshots, anomalies, false), billing.BuildReconciliation(rows, prices, ratios, snapshots, anomalies, true))
		return
	}
	report := billing.BuildReconciliation(rows, prices, ratios, snapshots, anomalies, userID > 0)
	writeDashboardJSON(w, http.StatusOK, map[string]any{"items": report.Rows, "totals": report.Totals, "job": job, "range_from": from, "range_to": to})
}

func billingReconciliationAdmin(r *http.Request) bool {
	user, ok := ctauth.CurrentUser(r)
	return !ok || user.Role == "admin"
}

func writeBillingReconciliationCSV(w http.ResponseWriter, l1, l2 billing.ReconciliationReport) {
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="billing-reconciliation.csv"`)
	_, _ = w.Write([]byte{0xef, 0xbb, 0xbf})
	writer := csv.NewWriter(w)
	_ = writer.Write([]string{"层级", "用户ID", "用户", "日期", "模型", "分组", "CT金额", "new-api实扣金额", "差额", "差额率", "异常订单差额", "缓存写策略差额", "剩余差额", "分类", "回退计价"})
	write := func(level string, item billing.ReconciliationRow) {
		_ = writer.Write([]string{level, strconv.FormatInt(item.UserID, 10), item.Username, item.Day, item.ModelName, item.GroupName, item.CTAmount, item.ActualAmount, item.DiffAmount, item.DiffRate, item.Breakdown.Anomaly, item.Breakdown.CacheWritePolicy, item.Breakdown.Residual, item.Classification, strconv.FormatBool(item.FallbackPriced)})
	}
	for _, item := range l1.Rows {
		write("L1", item)
	}
	for _, item := range l2.Rows {
		write("L2", item)
	}
	_ = writer.Write([]string{"分类小计", "", "", "", "", "", l1.Totals.CTAmount, l1.Totals.ActualAmount, l1.Totals.DiffAmount, "", l1.Totals.Breakdown.Anomaly, l1.Totals.Breakdown.CacheWritePolicy, l1.Totals.Breakdown.Residual})
	writer.Flush()
}

type billingReconciliationSource interface {
	DetailedLogsPage(context.Context, string, int64, time.Time, time.Time, billing.LogCursor, int) ([]billing.PagedLogRecord, error)
}

type BillingReconciliationRequestsHandler struct {
	Store     BillingSummaryStore
	Source    billingReconciliationSource
	PagePause time.Duration
}

func (h BillingReconciliationRequestsHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet || !billingReconciliationAdmin(r) {
		writeDashboardError(w, http.StatusForbidden, "forbidden")
		return
	}
	q := r.URL.Query()
	instanceID, model := strings.TrimSpace(q.Get("instance_id")), strings.TrimSpace(q.Get("model_name"))
	jobID := strings.TrimSpace(q.Get("job_id"))
	userID, userErr := strconv.ParseInt(q.Get("user_id"), 10, 64)
	from, to, _, rangeErr := billingPeriodQuery(r)
	day, dayErr := time.ParseInLocation("2006-01-02", q.Get("day"), billing.BusinessLocation)
	if instanceID == "" || jobID == "" || model == "" || userErr != nil || userID <= 0 || rangeErr != nil || dayErr != nil || day.Before(from) || !day.Before(to) {
		writeDashboardError(w, http.StatusBadRequest, "invalid_query")
		return
	}
	job, err := billingJobForRead(r, h.Store, instanceID, "generate", from, to)
	if err != nil || job.Status != "complete" {
		writeDashboardError(w, http.StatusConflict, "billing_not_generated")
		return
	}
	dayEnd := day.Add(24 * time.Hour)
	if dayEnd.After(to) {
		dayEnd = to
	}
	all := make([]billing.PagedLogRecord, 0, 20_000)
	cursor := billing.LogCursor{}
	truncated, scanned := false, 0
	for page := 0; page < 10; page++ {
		logs, pageErr := h.Source.DetailedLogsPage(r.Context(), instanceID, userID, day, dayEnd, cursor, billing.BillingPageSize)
		if pageErr != nil {
			writeDashboardError(w, http.StatusInternalServerError, "billing_query_failed")
			return
		}
		scanned += len(logs)
		all = append(all, logs...)
		if len(logs) < billing.BillingPageSize {
			break
		}
		last := logs[len(logs)-1]
		cursor = billing.LogCursor{CreatedUnix: last.CreatedUnix, ID: last.ID}
		if page == 9 {
			truncated = true
			break
		}
		if h.PagePause > 0 {
			select {
			case <-r.Context().Done():
				return
			case <-time.After(h.PagePause):
			}
		}
	}
	prices, err := h.Store.ListBillingPrices(r.Context(), instanceID)
	if err != nil {
		writeDashboardError(w, http.StatusInternalServerError, "billing_query_failed")
		return
	}
	ratios, err := h.Store.ListBillingGroupRatios(r.Context(), instanceID)
	if err != nil {
		writeDashboardError(w, http.StatusInternalServerError, "billing_query_failed")
		return
	}
	snapshots, err := h.Store.BillingRatioSnapshots(r.Context(), instanceID, day, dayEnd)
	if err != nil {
		writeDashboardError(w, http.StatusInternalServerError, "billing_query_failed")
		return
	}
	result := billing.ReconcileRequests(all, model, day, prices, ratios, snapshots[day.Format("2006-01-02")])
	result.Scanned, result.Truncated = scanned, truncated
	writeDashboardJSON(w, http.StatusOK, result)
}
