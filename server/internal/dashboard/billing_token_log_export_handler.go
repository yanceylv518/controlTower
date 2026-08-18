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
type BillingTokenLogExportHandler struct {
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
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	_, _ = w.Write([]byte("\xef\xbb\xbf"))
	cw := csv.NewWriter(w)
	_ = cw.Write([]string{"请求时间", "Request ID", "模型", "普通输入Token", "缓存读取Token", "缓存写入Token", "输出Token", "Quota"})
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
		for _, v := range logs {
			_ = cw.Write([]string{billing.FormatBusinessTime(v.CreatedUnix), v.RequestID, v.ModelName, strconv.FormatInt(v.PromptTokens.Int64, 10), strconv.FormatInt(v.CacheTokens, 10), strconv.FormatInt(v.CacheWriteTokens, 10), strconv.FormatInt(v.CompletionTokens.Int64, 10), strconv.FormatInt(v.Quota, 10)})
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
	cw.Flush()
}
