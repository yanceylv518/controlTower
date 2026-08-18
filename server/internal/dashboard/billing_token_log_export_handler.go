package dashboard

import (
	"context"
	"encoding/csv"
	"fmt"
	"net/http"
	"strconv"
	"time"

	ctauth "controltower/server/internal/auth"
	"controltower/server/internal/billing"
)

type BillingTokenLogSource interface {
	TokenDetailedLogsPage(context.Context, string, int64, int64, time.Time, time.Time, billing.LogCursor, int) ([]billing.PagedLogRecord, error)
}
type BillingTokenLogStore interface {
	ListBillingPrices(context.Context, string) ([]billing.PriceRecord, error)
	ListBillingGroupRatios(context.Context, string) ([]billing.GroupRatio, error)
	ListBillingModelMetadata(context.Context, string) ([]billing.ModelMetadata, error)
}
type BillingTokenLogExportHandler struct {
	Store     BillingTokenLogStore
	Source    BillingTokenLogSource
	PagePause time.Duration
}

func (h BillingTokenLogExportHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	site := q.Get("instance_id")
	uid, e1 := strconv.ParseInt(q.Get("user_id"), 10, 64)
	tokenID, e2 := strconv.ParseInt(q.Get("token_id"), 10, 64)
	from, to, _, e3 := billingPeriodQuery(r)
	if site == "" || e1 != nil || e2 != nil || e3 != nil || uid <= 0 || tokenID < 0 {
		writeDashboardError(w, 400, "invalid_query")
		return
	}
	if user, ok := ctauth.CurrentUser(r); ok && !tokenBillingScopeAllowed(user, site, uid) {
		writeDashboardError(w, http.StatusForbidden, "forbidden")
		return
	}
	prices, err := h.Store.ListBillingPrices(r.Context(), site)
	if err != nil {
		writeDashboardError(w, http.StatusInternalServerError, "billing_query_failed")
		return
	}
	ratios, err := h.Store.ListBillingGroupRatios(r.Context(), site)
	if err != nil {
		writeDashboardError(w, http.StatusInternalServerError, "billing_query_failed")
		return
	}
	metadata, err := h.Store.ListBillingModelMetadata(r.Context(), site)
	if err != nil {
		writeDashboardError(w, http.StatusInternalServerError, "billing_query_failed")
		return
	}
	byModel := map[string][]billing.Price{}
	for _, value := range prices {
		byModel[value.ModelName] = append(byModel[value.ModelName], value.Price)
	}
	byGroup := map[string]string{}
	for _, value := range ratios {
		byGroup[value.GroupName] = value.Ratio
	}
	maxContext := map[string]int64{}
	for _, value := range metadata {
		maxContext[value.ModelName] = value.MaxContextTokens
	}
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	_, _ = w.Write([]byte("\xef\xbb\xbf"))
	cw := csv.NewWriter(w)
	headerWritten := false
	writeHeader := func(username, tokenName string) {
		_ = cw.Write([]string{"用户名", username})
		_ = cw.Write([]string{"用户 ID", strconv.FormatInt(uid, 10)})
		_ = cw.Write([]string{"Token 名称", tokenName})
		_ = cw.Write([]string{"Token ID", strconv.FormatInt(tokenID, 10)})
		_ = cw.Write([]string{"账单区间", billing.FormatBusinessTime(from.Unix()), billing.FormatBusinessTime(to.Unix())})
		_ = cw.Write(nil)
		_ = cw.Write([]string{"请求时间", "Request ID", "模型", "普通输入Token", "缓存读取Token", "缓存写入Token", "输出Token", "CT金额", "Quota"})
		headerWritten = true
	}
	cursor := billing.LogCursor{}
	page := 0
	for {
		page++
		logs, err := billing.ReadPageWithRetry(r.Context(), fmt.Sprintf("site=%s user=%d token=%d page=%d", site, uid, tokenID, page), cursor, func() ([]billing.PagedLogRecord, error) {
			return h.Source.TokenDetailedLogsPage(r.Context(), site, uid, tokenID, from, to, cursor, billingWorkbookPageSize)
		})
		if err != nil {
			writeDashboardError(w, http.StatusInternalServerError, err.Error())
			return
		}
		if len(logs) == 0 {
			break
		}
		if !headerWritten {
			writeHeader(logs[0].Username, logs[0].TokenName)
		}
		for _, v := range logs {
			amount := ""
			if len(billing.AnomalyReasons(v, maxContext[v.ModelName])) == 0 {
				if price, ok := billing.SelectPrice(byModel[v.ModelName], time.Unix(v.CreatedUnix, 0), billing.RequestContextTokens(v)); ok {
					price, groupRatio := billing.RequestPrice(v, price)
					if groupRatio == "" {
						groupRatio = byGroup[v.GroupName]
					}
					amount = billing.PriceRequest(v, price, groupRatio).Total
				}
			}
			_ = cw.Write([]string{billing.FormatBusinessTime(v.CreatedUnix), v.RequestID, v.ModelName, strconv.FormatInt(v.PromptTokens.Int64, 10), strconv.FormatInt(v.CacheTokens, 10), strconv.FormatInt(v.CacheWriteTokens, 10), strconv.FormatInt(v.CompletionTokens.Int64, 10), amount, strconv.FormatInt(v.Quota, 10)})
		}
		last := logs[len(logs)-1]
		cursor = billing.LogCursor{CreatedUnix: last.CreatedUnix, ID: last.ID}
		if len(logs) < billingWorkbookPageSize {
			break
		}
		if h.PagePause > 0 {
			timer := time.NewTimer(h.PagePause)
			select {
			case <-r.Context().Done():
				timer.Stop()
				return
			case <-timer.C:
			}
		}
	}
	if !headerWritten {
		writeHeader("", "")
	}
	cw.Flush()
}
