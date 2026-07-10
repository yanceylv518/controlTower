package dashboard

import (
	"net/http"
	"strconv"
	"time"

	"controltower/server/internal/storage"
)

type LogStore interface {
	QueryLogEvents(query storage.LogQuery) ([]storage.LogEvent, error)
}

type LogSource interface {
	Logs() ([]storage.LogEvent, error)
}

type logSourceAdapter struct {
	source LogSource
}

func (a logSourceAdapter) QueryLogEvents(query storage.LogQuery) ([]storage.LogEvent, error) {
	events, err := a.source.Logs()
	if err != nil {
		return nil, err
	}
	return storage.FilterLogEvents(events, query), nil
}

type LogListResponse struct {
	Items []LogSummary `json:"items"`
}

type LogSummary struct {
	InstanceID        string    `json:"instance_id"`
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

func (h Handler) WithLogSource(source LogSource) Handler {
	h.logStore = logSourceAdapter{source: source}
	return h
}

func (h Handler) WithLogStore(store LogStore) Handler {
	h.logStore = store
	return h
}

func (h Handler) HandleLogs(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeDashboardError(w, http.StatusMethodNotAllowed, "method_not_allowed")
		return
	}
	if h.logStore == nil {
		writeDashboardError(w, http.StatusInternalServerError, "log_source_not_configured")
		return
	}
	query := parseLogFilter(r)
	events, err := h.logStore.QueryLogEvents(query)
	if err != nil {
		writeDashboardError(w, http.StatusInternalServerError, "query_failed")
		return
	}
	writeDashboardJSON(w, http.StatusOK, LogListResponse{Items: summarizeLogs(events)})
}

func parseLogFilter(r *http.Request) LogFilter {
	query := r.URL.Query()
	return LogFilter{
		InstanceID: query.Get("instance_id"),
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

func summarizeLogs(events []storage.LogEvent) []LogSummary {
	summaries := make([]LogSummary, 0, len(events))
	for _, event := range events {
		summaries = append(summaries, LogSummary{
			InstanceID:        event.InstanceID,
			SourceLogID:       event.SourceLogID,
			CreatedAt:         event.CreatedAt,
			LogType:           event.LogType,
			UserID:            event.UserID,
			Username:          event.Username,
			ChannelID:         event.ChannelID,
			ModelName:         event.ModelName,
			TokenID:           event.TokenID,
			TokenName:         event.TokenName,
			TotalTokens:       event.TotalTokens,
			Quota:             event.Quota,
			UseTime:           event.UseTime,
			RequestID:         event.RequestID,
			UpstreamRequestID: event.UpstreamRequestID,
			ErrorSummary:      event.ErrorSummary,
		})
	}
	return summaries
}

func parseInt(value string) int {
	parsed, err := strconv.Atoi(value)
	if err != nil {
		return 0
	}
	return parsed
}

func parseInt64(value string) int64 {
	parsed, err := strconv.ParseInt(value, 10, 64)
	if err != nil {
		return 0
	}
	return parsed
}

func parseTime(value string) time.Time {
	if value == "" {
		return time.Time{}
	}
	parsed, err := time.Parse(time.RFC3339, value)
	if err != nil {
		return time.Time{}
	}
	return parsed
}
func parseBool(value string) (bool, bool) {
	parsed, err := strconv.ParseBool(value)
	if err != nil {
		return false, false
	}
	return parsed, true
}
