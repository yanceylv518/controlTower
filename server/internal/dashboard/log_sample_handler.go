package dashboard

import (
	"net/http"
	"time"

	"controltower/server/internal/storage"
)

type LogSampleStore interface {
	QueryLogSamples(query storage.LogSampleQuery) ([]storage.LogSample, error)
}

type LogSampleListResponse struct {
	Items []LogSampleSummary `json:"items"`
}

type LogSampleSummary struct {
	InstanceID        string    `json:"instance_id"`
	SampleKind        string    `json:"sample_kind"`
	SourceLogID       int64     `json:"source_log_id"`
	CreatedAt         time.Time `json:"created_at"`
	LogType           string    `json:"log_type"`
	UserID            int64     `json:"user_id"`
	Username          string    `json:"username"`
	ChannelID         int64     `json:"channel_id"`
	ModelName         string    `json:"model_name"`
	TokenID           int64     `json:"token_id"`
	TokenName         string    `json:"token_name"`
	TotalTokens       int64     `json:"total_tokens"`
	Quota             int64     `json:"quota"`
	UseTime           float64   `json:"use_time"`
	RequestID         string    `json:"request_id"`
	UpstreamRequestID string    `json:"upstream_request_id"`
	ErrorSummary      string    `json:"error_summary"`
}

func (h Handler) WithLogSampleStore(store LogSampleStore) Handler {
	h.logSampleStore = store
	return h
}

func (h Handler) HandleLogSamples(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeDashboardError(w, http.StatusMethodNotAllowed, "method_not_allowed")
		return
	}
	if h.logSampleStore == nil {
		writeDashboardError(w, http.StatusInternalServerError, "log_sample_store_not_configured")
		return
	}
	items, err := h.logSampleStore.QueryLogSamples(parseLogSampleFilter(r))
	if err != nil {
		writeDashboardError(w, http.StatusInternalServerError, "query_failed")
		return
	}
	writeDashboardJSON(w, http.StatusOK, LogSampleListResponse{Items: summarizeLogSamples(items)})
}

func parseLogSampleFilter(r *http.Request) storage.LogSampleQuery {
	query := r.URL.Query()
	return storage.LogSampleQuery{
		InstanceID: query.Get("instance_id"),
		SampleKind: query.Get("sample_kind"),
		UserID:     parseInt64(query.Get("user_id")),
		ChannelID:  parseInt64(query.Get("channel_id")),
		ModelName:  query.Get("model_name"),
		LogType:    query.Get("log_type"),
		RequestID:  query.Get("request_id"),
		StartTime:  parseTime(query.Get("start_time")),
		EndTime:    parseTime(query.Get("end_time")),
		Limit:      parseInt(query.Get("limit")),
		Offset:     parseInt(query.Get("offset")),
	}
}

func summarizeLogSamples(samples []storage.LogSample) []LogSampleSummary {
	summaries := make([]LogSampleSummary, 0, len(samples))
	for _, sample := range samples {
		summaries = append(summaries, LogSampleSummary{InstanceID: sample.InstanceID, SampleKind: sample.SampleKind, SourceLogID: sample.SourceLogID, CreatedAt: sample.CreatedAt, LogType: sample.LogType, UserID: sample.UserID, Username: sample.Username, ChannelID: sample.ChannelID, ModelName: sample.ModelName, TokenID: sample.TokenID, TokenName: sample.TokenName, TotalTokens: sample.TotalTokens, Quota: sample.Quota, UseTime: sample.UseTime, RequestID: sample.RequestID, UpstreamRequestID: sample.UpstreamRequestID, ErrorSummary: sample.ErrorSummary})
	}
	return summaries
}
