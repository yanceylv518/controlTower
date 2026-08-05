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

type BillingDetailedLogSource interface {
	DetailedLogs(context.Context, string, int64, time.Time, time.Time) ([]billing.DetailedLogRecord, error)
}
type BillingLogExportHandler struct {
	Store  BillingSummaryStore
	Source BillingDetailedLogSource
}

func (h BillingLogExportHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	instanceID := strings.TrimSpace(r.URL.Query().Get("instance_id"))
	userID, err := strconv.ParseInt(r.URL.Query().Get("user_id"), 10, 64)
	month := strings.TrimSpace(r.URL.Query().Get("month"))
	from, monthErr := time.ParseInLocation("2006-01", month, time.Local)
	if r.Method != http.MethodGet || instanceID == "" || err != nil || userID <= 0 || monthErr != nil {
		writeDashboardError(w, 400, "invalid_query")
		return
	}
	if user, ok := ctauth.CurrentUser(r); ok && user.Role != "admin" {
		if user.ScopeSite != instanceID || !containsBillingUser(user.ScopeUserIDs, userID) {
			writeDashboardError(w, 403, "forbidden")
			return
		}
	}
	to := from.AddDate(0, 1, 0)
	logs, err := h.Source.DetailedLogs(r.Context(), instanceID, userID, from, to)
	if err != nil {
		writeDashboardError(w, 502, "newapi_logs_query_failed")
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
	byModel := map[string][]billing.Price{}
	for _, p := range prices {
		byModel[p.ModelName] = append(byModel[p.ModelName], p.Price)
	}
	byGroup := map[string]string{}
	for _, v := range ratios {
		byGroup[v.GroupName] = v.Ratio
	}
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="billing-log-details.csv"`)
	_, _ = w.Write([]byte{0xef, 0xbb, 0xbf})
	writer := csv.NewWriter(w)
	_ = writer.Write([]string{"日志ID", "时间", "请求ID", "用户名", "模型", "普通输入Token", "缓存读取Token", "缓存写入Token", "5m缓存写入Token", "1h缓存写入Token", "输出Token", "输入单价/1M", "缓存读取单价/1M", "缓存写入单价/1M", "输出单价/1M", "分组倍率", "金额"})
	for _, log := range logs {
		price, ok := billing.SelectPrice(byModel[log.ModelName], log.CreatedAt, log.PromptTokens)
		ratio := byGroup[log.GroupName]
		if ratio == "" {
			ratio = "1"
		}
		amount := ""
		if !ok {
			if snapshot, e := billing.ParseRatioSnapshot(snapshots[log.CreatedAt.Format("2006-01-02")]); e == nil {
				price, ratio, e = billing.FallbackPrice(snapshot, log.ModelName, log.GroupName)
				ok = e == nil
			}
		}
		if ok {
			if value, e := billing.Amount(billing.Usage{PromptTokens: log.PromptTokens, CompletionTokens: log.CompletionTokens, CacheTokens: log.CacheTokens, CacheWriteTokens: log.CacheWriteTokens, CacheWrite5mTokens: log.CacheWrite5mTokens, CacheWrite1hTokens: log.CacheWrite1hTokens}, price, ratio); e == nil {
				amount = billing.FormatAmount(value, 6)
			}
		}
		_ = writer.Write([]string{
			strconv.FormatInt(log.ID, 10), log.CreatedAt.Format(time.RFC3339), log.RequestID,
			log.Username, log.ModelName,
			strconv.FormatInt(log.PromptTokens, 10), strconv.FormatInt(log.CacheTokens, 10),
			strconv.FormatInt(log.CacheWriteTokens, 10), strconv.FormatInt(log.CacheWrite5mTokens, 10),
			strconv.FormatInt(log.CacheWrite1hTokens, 10), strconv.FormatInt(log.CompletionTokens, 10),
			price.Input, price.Cache, price.CacheWrite, price.Output, ratio, amount,
		})
	}
	writer.Flush()
}
