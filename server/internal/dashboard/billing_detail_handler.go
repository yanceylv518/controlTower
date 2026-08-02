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
	month := strings.TrimSpace(r.URL.Query().Get("month"))
	if month == "" {
		month = time.Now().Format("2006-01")
	}
	from, monthErr := time.ParseInLocation("2006-01", month, time.Local)
	if r.Method != http.MethodGet || instanceID == "" || err != nil || userID <= 0 || monthErr != nil {
		writeDashboardError(w, http.StatusBadRequest, "invalid_query")
		return
	}
	if user, ok := ctauth.CurrentUser(r); ok && user.Role != "admin" {
		if user.ScopeSite != instanceID || !containsBillingUser(user.ScopeUserIDs, userID) {
			writeDashboardError(w, http.StatusForbidden, "forbidden")
			return
		}
	}
	to := from.AddDate(0, 1, 0)
	rows, err := h.Store.QueryBillingAggregates(r.Context(), instanceID, from, to, []int64{userID})
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
	items := billing.BuildDetails(rows, prices, ratios, snapshots)
	if r.URL.Query().Get("format") == "csv" {
		writeBillingDetailCSV(w, items)
		return
	}
	writeDashboardJSON(w, http.StatusOK, map[string]any{"items": items, "user_id": userID, "month": month, "data_through": to.Add(-time.Nanosecond)})
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
	writer := csv.NewWriter(w)
	_ = writer.Write([]string{"日期", "模型", "分组", "阶梯起点", "请求数", "输入Token", "输出Token", "缓存Token", "金额", "价格来源", "未定价"})
	for _, item := range items {
		_ = writer.Write([]string{item.Day, item.ModelName, item.GroupName, strconv.FormatInt(item.TierFrom, 10), strconv.FormatInt(item.RequestCount, 10), strconv.FormatInt(item.PromptTokens, 10), strconv.FormatInt(item.CompletionTokens, 10), strconv.FormatInt(item.CacheTokens, 10), item.Amount, item.PriceSource, strconv.FormatBool(item.Unpriced)})
	}
	writer.Flush()
}
