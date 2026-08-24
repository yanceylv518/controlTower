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
	"controltower/server/internal/storage"
)

type BillingTokenStore interface {
	BillingSummaryStore
	QueryBillingTokenRows(context.Context, string, int64, int64, time.Time, time.Time) ([]billing.TokenDailyRow, error)
	BillingTokenAnomalyCountsForJob(context.Context, string, int64, int64, time.Time, time.Time) ([]billing.AnomalyCount, error)
}

type BillingTokenHandler struct{ Store BillingTokenStore }

func (h BillingTokenHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	instanceID := strings.TrimSpace(r.URL.Query().Get("instance_id"))
	userID, userErr := strconv.ParseInt(r.URL.Query().Get("user_id"), 10, 64)
	from, to, _, rangeErr := billingPeriodQuery(r)
	if instanceID == "" || userErr != nil || userID <= 0 || rangeErr != nil {
		writeDashboardError(w, 400, "invalid_query")
		return
	}
	if user, ok := ctauth.CurrentUser(r); ok && !tokenBillingScopeAllowed(user, instanceID, userID) {
		writeDashboardError(w, 403, "forbidden")
		return
	}
	job, err := billingJobForRead(r, h.Store, instanceID, "generate", from, to)
	if err != nil || job.Status != "complete" {
		writeBillingReadConflict(w, err)
		return
	}
	tokenID := int64(-1)
	if strings.HasSuffix(r.URL.Path, "/daily") {
		tokenID, err = strconv.ParseInt(r.URL.Query().Get("token_id"), 10, 64)
		if err != nil || tokenID < 0 {
			writeDashboardError(w, 400, "invalid_query")
			return
		}
	}
	rows, err := h.Store.QueryBillingTokenRows(r.Context(), job.ID, userID, tokenID, from, to)
	if err != nil {
		writeDashboardError(w, 500, "billing_query_failed")
		return
	}
	anomalies, err := h.Store.BillingTokenAnomalyCountsForJob(r.Context(), job.ID, userID, tokenID, from, to)
	if err != nil {
		writeDashboardError(w, 500, "billing_query_failed")
		return
	}
	prices, err := h.Store.ListBillingPrices(r.Context(), instanceID)
	if err != nil {
		writeDashboardError(w, 500, "billing_query_failed")
		return
	}
	ratios, err := h.Store.ListBillingGroupRatios(r.Context(), instanceID)
	if err != nil {
		writeDashboardError(w, 500, "billing_query_failed")
		return
	}
	snapshots, err := h.Store.BillingRatioSnapshots(r.Context(), instanceID, from, to)
	if err != nil {
		writeDashboardError(w, 500, "billing_query_failed")
		return
	}
	if tokenID >= 0 {
		items := billing.BuildDetails(billing.TokenRowsAsAggregates(rows), prices, ratios, snapshots)
		items = applyDetailAnomalyCounts(items, anomalies, userID, 0)
		if r.URL.Query().Get("format") == "csv" {
			writeTokenCSV(w, userID, tokenID, from, to, billing.BuildTokenSummaries(rows, prices, ratios, snapshots), items)
			return
		}
		writeDashboardJSON(w, 200, map[string]any{"items": items, "token_id": tokenID, "job": job})
		return
	}
	missing := false
	if len(rows) == 0 {
		userRows, queryErr := h.Store.QueryBillingAggregatesForJobRange(r.Context(), job.ID, from, to, []int64{userID})
		if queryErr != nil {
			writeDashboardError(w, 500, "billing_query_failed")
			return
		}
		missing = len(userRows) > 0
	}
	items := billing.BuildTokenSummaries(rows, prices, ratios, snapshots)
	items = applyTokenAnomalyCounts(items, anomalies)
	writeDashboardJSON(w, 200, map[string]any{"items": items, "token_data_missing": missing, "job": job})
}

func tokenBillingScopeAllowed(user storage.User, instanceID string, userID int64) bool {
	return user.Role == "admin" || (user.ScopeSite == instanceID && containsBillingUser(user.ScopeUserIDs, userID))
}

func writeTokenCSV(w http.ResponseWriter, userID, tokenID int64, from, to time.Time, summaries []billing.TokenSummary, details []billing.DetailItem) {
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="`+billingDownloadName("billing-token", userID, tokenID, from, to)+`.csv"`)
	_, _ = w.Write([]byte("\xef\xbb\xbf"))
	cw := csv.NewWriter(w)
	_ = cw.Write([]string{"令牌汇总"})
	_ = cw.Write([]string{"令牌ID", "令牌名", "请求数", "异常订单数", "异常金额", "普通输入Token", "缓存读取Token", "缓存写入Token", "输出Token", "CT金额", "Quota"})
	for _, v := range summaries {
		_ = cw.Write([]string{strconv.FormatInt(v.TokenID, 10), v.TokenName, strconv.FormatInt(v.RequestCount, 10), strconv.FormatInt(v.AbnormalRows, 10), v.AbnormalAmount, strconv.FormatInt(v.PromptTokens, 10), strconv.FormatInt(v.CacheTokens, 10), strconv.FormatInt(v.CacheWriteTokens, 10), strconv.FormatInt(v.CompletionTokens, 10), v.CTAmount, strconv.FormatInt(v.Quota, 10)})
	}
	_ = cw.Write([]string{})
	_ = cw.Write([]string{"令牌日账单"})
	_ = cw.Write([]string{"日期", "模型", "分组", "档位", "请求数", "异常订单数", "异常金额", "普通输入Token", "缓存读取Token", "缓存写入Token", "输出Token", "金额"})
	for _, v := range details {
		_ = cw.Write([]string{v.Day, v.ModelName, v.GroupName, strconv.FormatInt(v.TierFrom, 10), strconv.FormatInt(v.RequestCount, 10), strconv.FormatInt(v.AbnormalRows, 10), v.AbnormalAmount, strconv.FormatInt(v.PromptTokens, 10), strconv.FormatInt(v.CacheTokens, 10), strconv.FormatInt(v.CacheWriteTokens, 10), strconv.FormatInt(v.CompletionTokens, 10), v.Amount})
	}
	cw.Flush()
}
