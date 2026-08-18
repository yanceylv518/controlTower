package dashboard

import (
	"context"
	"net/http"
	"strconv"
	"strings"
	"time"

	ctauth "controltower/server/internal/auth"
	"controltower/server/internal/billing"
)

type BillingRequestsSource interface {
	DetailedLogsPage(context.Context, string, int64, time.Time, time.Time, billing.LogCursor, int) ([]billing.PagedLogRecord, error)
}

type BillingRequestsHandler struct {
	Store  BillingSummaryStore
	Source BillingRequestsSource
}

type billingRequestItem struct {
	LogID      int64  `json:"log_id"`
	CreatedAt  string `json:"created_at"`
	RequestID  string `json:"request_id"`
	ModelName  string `json:"model_name"`
	Prompt     int64  `json:"prompt_tokens"`
	Cache      int64  `json:"cache_tokens"`
	CacheWrite int64  `json:"cache_write_tokens"`
	Completion int64  `json:"completion_tokens"`
	Quota      int64  `json:"quota"`
}

func (h BillingRequestsHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	instanceID := strings.TrimSpace(q.Get("instance_id"))
	userID, userErr := strconv.ParseInt(q.Get("user_id"), 10, 64)
	from, to, _, rangeErr := billingPeriodQuery(r)
	pageSize := 100
	if raw := strings.TrimSpace(q.Get("page_size")); raw != "" {
		value, err := strconv.Atoi(raw)
		if err != nil || value < 1 || value > 200 {
			writeDashboardError(w, http.StatusBadRequest, "invalid_page_size")
			return
		}
		pageSize = value
	}
	afterID := int64(0)
	afterCreated := int64(0)
	if raw := strings.TrimSpace(q.Get("after_id")); raw != "" {
		value, err := strconv.ParseInt(raw, 10, 64)
		if err != nil || value < 0 {
			writeDashboardError(w, http.StatusBadRequest, "invalid_after_id")
			return
		}
		afterID = value
	}
	if raw := strings.TrimSpace(q.Get("after_created")); raw != "" {
		value, err := strconv.ParseInt(raw, 10, 64)
		if err != nil || value < 0 {
			writeDashboardError(w, http.StatusBadRequest, "invalid_after_created")
			return
		}
		afterCreated = value
	}
	if instanceID == "" || userErr != nil || userID <= 0 || rangeErr != nil || h.Source == nil {
		writeDashboardError(w, http.StatusBadRequest, "invalid_query")
		return
	}
	if user, ok := ctauth.CurrentUser(r); ok && user.Role != "admin" && (user.ScopeSite != instanceID || !containsBillingUser(user.ScopeUserIDs, userID)) {
		writeDashboardError(w, http.StatusForbidden, "forbidden")
		return
	}
	job, jobErr := billingJobForRead(r, h.Store, instanceID, "generate", from, to)
	if jobErr != nil || job.Status != "complete" {
		writeBillingReadConflict(w, jobErr)
		return
	}
	logs, err := h.Source.DetailedLogsPage(r.Context(), instanceID, userID, from, to, billing.LogCursor{CreatedUnix: afterCreated, ID: afterID}, pageSize+1)
	if err != nil {
		writeDashboardError(w, http.StatusBadGateway, "newapi_logs_query_failed")
		return
	}
	hasMore := len(logs) > pageSize
	if hasMore {
		logs = logs[:pageSize]
	}
	items := make([]billingRequestItem, 0, len(logs))
	for _, log := range logs {
		items = append(items, billingRequestItem{
			LogID: log.ID, CreatedAt: billing.FormatBusinessTime(log.CreatedUnix), RequestID: log.RequestID,
			ModelName: log.ModelName, Prompt: log.PromptTokens.Int64, Cache: log.CacheTokens,
			CacheWrite: log.CacheWriteTokens, Completion: log.CompletionTokens.Int64, Quota: log.Quota,
		})
	}
	nextAfterID := afterID
	nextAfterCreated := afterCreated
	if len(logs) > 0 {
		nextAfterID = logs[len(logs)-1].ID
		nextAfterCreated = logs[len(logs)-1].CreatedUnix
	}
	writeDashboardJSON(w, http.StatusOK, map[string]any{"items": items, "has_more": hasMore, "next_after_id": nextAfterID, "next_after_created": nextAfterCreated})
}
