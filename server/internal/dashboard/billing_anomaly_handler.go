package dashboard

import (
	"context"
	"controltower/server/internal/billing"
	"database/sql"
	"encoding/csv"
	"net/http"
	"strconv"
	"strings"
	"time"
)

type BillingAnomalyStore interface {
	QueryBillingAnomalies(context.Context, string, string, int64, int64, time.Time, time.Time, time.Time, int64, int) ([]billing.AnomalyOrder, error)
	LatestBillingJob(context.Context, string, string, time.Time, time.Time) (billing.Job, error)
}
type BillingAnomalyHandler struct{ Store BillingAnomalyStore }

func (h BillingAnomalyHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	site := q.Get("instance_id")
	uid, _ := strconv.ParseInt(q.Get("user_id"), 10, 64)
	channelID, _ := strconv.ParseInt(q.Get("channel_id"), 10, 64)
	if !billingSiteAllowed(r, site, uid) {
		writeDashboardError(w, 403, "forbidden")
		return
	}
	from, to, rangeErr := parseBillingInputRange(q.Get("from"), q.Get("to"))
	if rangeErr != nil {
		writeDashboardError(w, 400, "invalid_query")
		return
	}
	jobType := "generate"
	if channelID > 0 {
		jobType = "channel_generate"
	}
	job, jobErr := h.Store.LatestBillingJob(r.Context(), site, jobType, from, to)
	jobID := ""
	if jobErr == nil && job.Status == "complete" {
		jobID = job.ID
	}
	cursorTime := time.Unix(0, 0)
	if raw := q.Get("cursor_time"); raw != "" {
		cursorTime, _ = time.Parse(time.RFC3339, raw)
	}
	cursorID, _ := strconv.ParseInt(q.Get("cursor_id"), 10, 64)
	limit, _ := strconv.Atoi(q.Get("limit"))
	if limit <= 0 || limit > 5000 {
		limit = 500
	}
	if q.Get("format") == "csv" {
		w.Header().Set("Content-Type", "text/csv; charset=utf-8")
		w.Header().Set("Content-Disposition", `attachment; filename="`+billingDownloadName("billing-anomalies", uid, channelID, from, to)+`.csv"`)
		_, _ = w.Write([]byte{0xef, 0xbb, 0xbf})
		cw := csv.NewWriter(w)
		_ = cw.Write([]string{"模型名称", "Request ID", "上游 Request ID", "请求时间", "普通输入", "缓存读取 Token", "缓存写入 Token", "输出 Token", "模型上下文", "输入 Token 单价", "输出 Token 单价", "缓存读取单价", "缓存写入单价", "输入 Token 费用", "输出 Token 费用", "缓存读取费用", "缓存写入费用", "异常记录参考金额", "实际扣除 Quota", "异常原因"})
		for {
			items, e := h.Store.QueryBillingAnomalies(r.Context(), site, jobID, uid, channelID, from, to, cursorTime, cursorID, 5000)
			if e != nil {
				return
			}
			for _, v := range items {
				_ = cw.Write([]string{v.ModelName, v.RequestID, v.UpstreamRequestID, v.CreatedAt.In(billing.BusinessLocation).Format("2006/01/02 15:04:05"), nullInt(v.PromptTokens), strconv.FormatInt(v.CacheTokens, 10), strconv.FormatInt(v.CacheWriteTokens, 10), nullInt(v.CompletionTokens), strconv.FormatInt(v.MaxContextTokens, 10), v.InputPrice, v.OutputPrice, v.CachePrice, v.CacheWritePrice, v.InputAmount, v.OutputAmount, v.CacheAmount, v.CacheWriteAmount, v.ReferenceAmount, strconv.FormatInt(v.Quota, 10), localizedReasons(v.Reasons)})
			}
			if len(items) < 5000 {
				break
			}
			last := items[len(items)-1]
			cursorTime, cursorID = last.CreatedAt, last.SourceLogID
		}
		cw.Flush()
		return
	}
	items, e := h.Store.QueryBillingAnomalies(r.Context(), site, jobID, uid, channelID, from, to, cursorTime, cursorID, limit)
	if e != nil {
		writeDashboardError(w, 500, "billing_anomaly_query_failed")
		return
	}
	writeDashboardJSON(w, 200, map[string]any{"items": items})
}

func billingDownloadName(prefix string, userID, channelID int64, from, to time.Time) string {
	identity := "all"
	if channelID > 0 {
		identity = "channel-" + strconv.FormatInt(channelID, 10)
	} else if userID > 0 {
		identity = "user-" + strconv.FormatInt(userID, 10)
	}
	return prefix + "-" + identity + "-" + from.Format("20060102-150405") + "_" + to.Add(-time.Second).Format("20060102-150405")
}
func localizedReasons(raw string) string {
	parts := strings.Split(raw, ",")
	for i, v := range parts {
		switch v {
		case "input_token_missing":
			parts[i] = "输入 Token 为空"
		case "input_token_zero":
			parts[i] = "输入 Token 为 0"
		case "output_token_missing":
			parts[i] = "输出 Token 为空"
		case "output_token_zero":
			parts[i] = "输出 Token 为 0"
		case "context_limit_exceeded":
			parts[i] = "输入 Token 超过模型上下文上限"
		}
	}
	return strings.Join(parts, "；")
}
func nullInt(v sql.NullInt64) string {
	if !v.Valid {
		return ""
	}
	return strconv.FormatInt(v.Int64, 10)
}
