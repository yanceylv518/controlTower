package dashboard

import (
	"encoding/csv"
	"net/http"
	"strconv"
	"strings"
	"time"

	ctauth "controltower/server/internal/auth"
	"controltower/server/internal/billing"
)

type BillingDetailHandler struct{ Store BillingSummaryStore }

func (h BillingDetailHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	instanceID := strings.TrimSpace(r.URL.Query().Get("instance_id"))
	userID, err := strconv.ParseInt(r.URL.Query().Get("user_id"), 10, 64)
	from, to, period, rangeErr := billingPeriodQuery(r)
	if r.Method != http.MethodGet || instanceID == "" || err != nil || userID <= 0 || rangeErr != nil {
		writeDashboardError(w, http.StatusBadRequest, "invalid_query")
		return
	}
	if user, ok := ctauth.CurrentUser(r); ok && user.Role != "admin" {
		if user.ScopeSite != instanceID || !containsBillingUser(user.ScopeUserIDs, userID) {
			writeDashboardError(w, http.StatusForbidden, "forbidden")
			return
		}
	}
	job, jobErr := h.Store.LatestBillingJob(r.Context(), instanceID, "generate", from, to)
	var rows []billing.AggregateRow
	if jobErr == nil && job.Status == "complete" {
		rows, err = h.Store.QueryBillingAggregatesForJob(r.Context(), job.ID, []int64{userID})
	}
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
	if r.URL.Query().Get("format") == "invoice_csv" {
		invoiceItems, invoiceTotal, invoiceErr := billing.BuildInvoice(rows, prices, ratios, snapshots, "1")
		if invoiceErr != nil {
			writeDashboardError(w, http.StatusUnprocessableEntity, "invoice_failed")
			return
		}
		writeBillingInvoiceCSV(w, userID, period, invoiceItems, invoiceTotal)
		return
	}
	items := billing.BuildDetails(rows, prices, ratios, snapshots)
	if jobErr == nil && job.Status == "complete" {
		counts, countErr := anomalyCounts(h.Store, r.Context(), job.ID)
		if countErr != nil {
			writeDashboardError(w, http.StatusInternalServerError, "billing_query_failed")
			return
		}
		applyDetailAnomalyCounts(items, counts, userID, 0)
	}
	if r.URL.Query().Get("format") == "csv" {
		writeBillingDetailCSV(w, items)
		return
	}
	writeDashboardJSON(w, http.StatusOK, map[string]any{"items": items, "user_id": userID, "period": period, "data_through": to.Add(-time.Nanosecond)})
}

func writeBillingInvoiceCSV(w http.ResponseWriter, userID int64, month string, items []billing.InvoiceItem, total billing.InvoiceTotal) {
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="billing-invoice.csv"`)
	_, _ = w.Write([]byte{0xef, 0xbb, 0xbf})
	writer := csv.NewWriter(w)
	_ = writer.Write([]string{"用户ID", strconv.FormatInt(userID, 10)})
	_ = writer.Write([]string{"账单月份", month})
	_ = writer.Write([]string{})
	_ = writer.Write([]string{"模型", "价格档位Token", "请求数", "普通输入Token", "缓存读取Token", "缓存写入Token", "输出Token", "输入单价/1M", "缓存读取单价/1M", "缓存写入单价/1M", "输出单价/1M", "输入金额", "缓存读取金额", "缓存写入金额", "输出金额", "小计", "未定价"})
	for _, item := range items {
		_ = writer.Write([]string{item.ModelName, strconv.FormatInt(item.TierFrom, 10), strconv.FormatInt(item.RequestCount, 10), strconv.FormatInt(item.PromptTokens, 10), strconv.FormatInt(item.CacheTokens, 10), strconv.FormatInt(item.CacheWriteTokens, 10), strconv.FormatInt(item.CompletionTokens, 10), item.InputPrice, item.CachePrice, item.CacheWritePrice, item.OutputPrice, item.InputAmount, item.CacheAmount, item.CacheWriteAmount, item.OutputAmount, item.Amount, strconv.FormatBool(item.Unpriced)})
	}
	_ = writer.Write([]string{})
	_ = writer.Write([]string{"账单合计", total.Amount})
	writer.Flush()
}

func containsBillingUser(ids []int64, target int64) bool {
	for _, id := range ids {
		if id == target {
			return true
		}
	}
	return false
}

func writeBillingDetailCSV(w http.ResponseWriter, items []billing.DetailItem) {
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="billing-detail.csv"`)
	_, _ = w.Write([]byte{0xef, 0xbb, 0xbf})
	// UTF-8 BOM so Excel opens the file with correct CJK encoding.
	_, _ = w.Write([]byte("\xef\xbb\xbf"))
	writer := csv.NewWriter(w)
	_ = writer.Write([]string{"日期", "模型", "分组", "阶梯起点", "请求数", "异常订单数", "普通输入Token", "输出Token", "缓存读取Token", "缓存写入Token", "输入单价/1M", "缓存读取单价/1M", "缓存写入单价/1M", "输出单价/1M", "金额", "未定价"})
	for _, item := range items {
		_ = writer.Write([]string{item.Day, item.ModelName, item.GroupName, strconv.FormatInt(item.TierFrom, 10), strconv.FormatInt(item.RequestCount, 10), strconv.FormatInt(item.AbnormalRows, 10), strconv.FormatInt(item.PromptTokens, 10), strconv.FormatInt(item.CompletionTokens, 10), strconv.FormatInt(item.CacheTokens, 10), strconv.FormatInt(item.CacheWriteTokens, 10), item.InputPrice, item.CachePrice, item.CacheWritePrice, item.OutputPrice, item.Amount, strconv.FormatBool(item.Unpriced)})
	}
	writer.Flush()
}
