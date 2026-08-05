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

type BillingSummaryStore interface {
	QueryBillingAggregates(context.Context, string, time.Time, time.Time, []int64) ([]billing.AggregateRow, error)
	QueryBillingAggregatesForJob(context.Context, string, []int64) ([]billing.AggregateRow, error)
	LatestBillingJob(context.Context, string, string, time.Time, time.Time) (billing.Job, error)
	ListBillingPrices(context.Context, string) ([]billing.PriceRecord, error)
	ListBillingGroupRatios(context.Context, string) ([]billing.GroupRatio, error)
	BillingRatioSnapshots(context.Context, string, time.Time, time.Time) (map[string]string, error)
	LatestBillingBalances(context.Context, string, time.Time, []int64) (map[int64]int64, error)
}

type BillingSummaryHandler struct{ Store BillingSummaryStore }

func (h BillingSummaryHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeDashboardError(w, http.StatusMethodNotAllowed, "method_not_allowed")
		return
	}
	instanceID := strings.TrimSpace(r.URL.Query().Get("instance_id"))
	if instanceID == "" {
		instanceID = strings.TrimSpace(r.URL.Query().Get("site"))
	}
	from, to, period, err := billingPeriodQuery(r)
	if err != nil || instanceID == "" {
		writeDashboardError(w, http.StatusBadRequest, "invalid_query")
		return
	}
	var userIDs []int64
	if user, ok := ctauth.CurrentUser(r); ok && user.Role != "admin" {
		if user.ScopeSite != instanceID {
			writeDashboardError(w, http.StatusForbidden, "forbidden")
			return
		}
		userIDs = user.ScopeUserIDs
		if len(userIDs) == 0 {
			h.writeResponse(w, r, nil, billing.SummaryTotal{}, to.Add(-time.Nanosecond))
			return
		}
	}
	job, jobErr := h.Store.LatestBillingJob(r.Context(), instanceID, "generate", from, to)
	jobID := ""
	if jobErr == nil && job.Status == "complete" {
		jobID = job.ID
	}
	cacheKey := billing.SummaryCacheKey(instanceID, period+":"+jobID, userIDs)
	if items, total, ok := billing.MonthlySummaryCache.Get(cacheKey); ok {
		h.respondWithSearch(w, r, items, total, to)
		return
	}
	var rows []billing.AggregateRow
	if jobID != "" {
		rows, err = h.Store.QueryBillingAggregatesForJob(r.Context(), jobID, userIDs)
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
	balances, err := h.Store.LatestBillingBalances(r.Context(), instanceID, to, userIDs)
	if err != nil {
		writeDashboardError(w, http.StatusInternalServerError, "billing_query_failed")
		return
	}
	items, total := billing.BuildSummary(rows, prices, ratios, snapshots, balances)
	billing.MonthlySummaryCache.Put(cacheKey, items, total)
	h.respondWithSearch(w, r, items, total, to)
}

func (h BillingSummaryHandler) respondWithSearch(w http.ResponseWriter, r *http.Request, items []billing.UserSummary, total billing.SummaryTotal, to time.Time) {
	search := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("search")))
	if search != "" {
		filtered := items[:0]
		for _, item := range items {
			if strings.Contains(strings.ToLower(item.Username), search) || strings.Contains(strconv.FormatInt(item.UserID, 10), search) {
				filtered = append(filtered, item)
			}
		}
		items = filtered
		total = billing.SummarizeUsers(items)
	}
	h.writeResponse(w, r, items, total, to.Add(-time.Nanosecond))
}

func (h BillingSummaryHandler) writeResponse(w http.ResponseWriter, r *http.Request, items []billing.UserSummary, total billing.SummaryTotal, through time.Time) {
	if r.URL.Query().Get("format") == "csv" {
		w.Header().Set("Content-Type", "text/csv; charset=utf-8")
		w.Header().Set("Content-Disposition", `attachment; filename="billing-summary.csv"`)
		_, _ = w.Write([]byte{0xef, 0xbb, 0xbf})
		// UTF-8 BOM so Excel opens the file with correct CJK encoding.
		_, _ = w.Write([]byte("\xef\xbb\xbf"))
		writer := csv.NewWriter(w)
		_ = writer.Write([]string{"用户ID", "用户名", "请求数", "输入Token", "输出Token", "缓存Token", "金额", "余额", "未定价模型", "价格来源"})
		for _, item := range items {
			_ = writer.Write([]string{strconv.FormatInt(item.UserID, 10), item.Username, strconv.FormatInt(item.RequestCount, 10), strconv.FormatInt(item.PromptTokens, 10), strconv.FormatInt(item.CompletionTokens, 10), strconv.FormatInt(item.CacheTokens, 10), item.Amount, strconv.FormatInt(item.Balance, 10), strings.Join(item.UnpricedModels, ","), strings.Join(item.PriceSources, ",")})
		}
		writer.Flush()
		return
	}
	page := positiveInt(r.URL.Query().Get("page"), 1)
	pageSize := positiveInt(r.URL.Query().Get("page_size"), 50)
	if pageSize > 200 {
		pageSize = 200
	}
	count := len(items)
	start := (page - 1) * pageSize
	if start > count {
		start = count
	}
	end := start + pageSize
	if end > count {
		end = count
	}
	writeDashboardJSON(w, http.StatusOK, map[string]any{"items": items[start:end], "total": count, "page": page, "page_size": pageSize, "summary": total, "data_through": through})
}

func positiveInt(raw string, fallback int) int {
	v, err := strconv.Atoi(raw)
	if err != nil || v < 1 {
		return fallback
	}
	return v
}
