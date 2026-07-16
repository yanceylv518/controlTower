package dashboard

import (
	"net/http"
	"strconv"
	"strings"
	"time"

	"controltower/server/internal/storage"
)

type NginxTimingStore interface {
	QueryNginxTiming(storage.NginxTimingQuery) ([]storage.NginxTimingBucket, error)
	QueryNginxSlowSamples(storage.NginxSlowSampleQuery) ([]storage.NginxSlowSample, error)
	QueryRequestDimensions(string, []string) ([]storage.RequestDimension, error)
}

func (h Handler) WithNginxTimingStore(s NginxTimingStore) Handler { h.nginxTimingStore = s; return h }

type NginxTimingBucketSummary struct {
	BucketAt          time.Time `json:"bucket_at"`
	RequestCount      int64     `json:"request_count"`
	UpstreamCount     int64     `json:"upstream_count"`
	Status4xx         int64     `json:"status_4xx"`
	Status5xx         int64     `json:"status_5xx"`
	Status504         int64     `json:"status_504"`
	RTP50             float64   `json:"rt_p50"`
	RTP95             float64   `json:"rt_p95"`
	RTMax             float64   `json:"rt_max"`
	UHTP50            float64   `json:"uht_p50"`
	UHTP95            float64   `json:"uht_p95"`
	UHTMax            float64   `json:"uht_max"`
	TransferP50       float64   `json:"transfer_p50"`
	TransferP95       float64   `json:"transfer_p95"`
	TransferMax       float64   `json:"transfer_max"`
	BytesTotal        int64     `json:"bytes_total"`
	SlowCount         int64     `json:"slow_count"`
	SlowTTFTCount     int64     `json:"slow_ttft_count"`
	SlowTransferCount int64     `json:"slow_transfer_count"`
}
type NginxTimingTotals struct {
	TotalRequests       int64   `json:"total_requests"`
	Status5xx           int64   `json:"status_5xx"`
	Status504           int64   `json:"status_504"`
	SlowCount           int64   `json:"slow_count"`
	SlowTTFTCount       int64   `json:"slow_ttft_count"`
	SlowTransferCount   int64   `json:"slow_transfer_count"`
	SlowTTFTPercent     float64 `json:"slow_ttft_percent"`
	SlowTransferPercent float64 `json:"slow_transfer_percent"`
}
type NginxTimingResponse struct {
	Items   []NginxTimingBucketSummary `json:"items"`
	Summary NginxTimingTotals          `json:"summary"`
}
type NginxSlowSampleSummary struct {
	ID          int64     `json:"id"`
	OccurredAt  time.Time `json:"occurred_at"`
	Path        string    `json:"path"`
	Status      int       `json:"status"`
	RT          float64   `json:"rt"`
	UHT         float64   `json:"uht"`
	URT         float64   `json:"urt"`
	Bytes       int64     `json:"bytes"`
	RequestID   string    `json:"request_id"`
	MatchStatus string    `json:"match_status"`
	MatchCount  int       `json:"match_count"`
	UserID      int64     `json:"user_id"`
	UserName    string    `json:"user_name"`
	ChannelID   int64     `json:"channel_id"`
	ChannelName string    `json:"channel_name"`
	ModelName   string    `json:"model_name"`
	TokenName   string    `json:"token_name"`
}

func (h Handler) HandleNginxTiming(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeDashboardError(w, http.StatusMethodNotAllowed, "method_not_allowed")
		return
	}
	instanceID := r.URL.Query().Get("instance_id")
	hours, ok := boundedInt(r.URL.Query().Get("hours"), 24, 1, 168)
	if !ok || instanceID == "" {
		writeDashboardError(w, http.StatusBadRequest, "invalid_query")
		return
	}
	items, err := h.nginxTimingStore.QueryNginxTiming(storage.NginxTimingQuery{InstanceID: instanceID, Since: time.Now().UTC().Add(-time.Duration(hours) * time.Hour)})
	if err != nil {
		writeDashboardError(w, 500, "query_failed")
		return
	}
	resp := NginxTimingResponse{Items: make([]NginxTimingBucketSummary, 0, len(items))}
	for _, v := range items {
		s := NginxTimingBucketSummary{BucketAt: v.BucketAt, RequestCount: v.RequestCount, UpstreamCount: v.UpstreamCount, Status4xx: v.Status4xx, Status5xx: v.Status5xx, Status504: v.Status504, RTP50: v.RTP50, RTP95: v.RTP95, RTMax: v.RTMax, UHTP50: v.UHTP50, UHTP95: v.UHTP95, UHTMax: v.UHTMax, TransferP50: v.TransferP50, TransferP95: v.TransferP95, TransferMax: v.TransferMax, BytesTotal: v.BytesTotal, SlowCount: v.SlowCount, SlowTTFTCount: v.SlowTTFTCount, SlowTransferCount: v.SlowTransferCount}
		resp.Items = append(resp.Items, s)
		resp.Summary.TotalRequests += v.RequestCount
		resp.Summary.Status5xx += v.Status5xx
		resp.Summary.Status504 += v.Status504
		resp.Summary.SlowCount += v.SlowCount
		resp.Summary.SlowTTFTCount += v.SlowTTFTCount
		resp.Summary.SlowTransferCount += v.SlowTransferCount
	}
	if resp.Summary.SlowCount > 0 {
		resp.Summary.SlowTTFTPercent = float64(resp.Summary.SlowTTFTCount) * 100 / float64(resp.Summary.SlowCount)
		resp.Summary.SlowTransferPercent = float64(resp.Summary.SlowTransferCount) * 100 / float64(resp.Summary.SlowCount)
	}
	writeDashboardJSON(w, 200, resp)
}
func (h Handler) HandleNginxSlowSamples(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeDashboardError(w, 405, "method_not_allowed")
		return
	}
	instanceID := r.URL.Query().Get("instance_id")
	hours, ok := boundedInt(r.URL.Query().Get("hours"), 24, 1, 168)
	if !ok || instanceID == "" {
		writeDashboardError(w, 400, "invalid_query")
		return
	}
	limit, ok := boundedInt(r.URL.Query().Get("limit"), 50, 1, 200)
	if !ok {
		writeDashboardError(w, 400, "invalid_query")
		return
	}
	offset, ok := boundedInt(r.URL.Query().Get("offset"), 0, 0, 1000000)
	if !ok {
		writeDashboardError(w, 400, "invalid_query")
		return
	}
	// Correlation filters are applied after loading request dimensions, so page
	// after enrichment instead of skipping raw rows before the filters run.
	items, err := h.nginxTimingStore.QueryNginxSlowSamples(storage.NginxSlowSampleQuery{InstanceID: instanceID, Since: time.Now().UTC().Add(-time.Duration(hours) * time.Hour), Limit: 200})
	if err != nil {
		writeDashboardError(w, 500, "query_failed")
		return
	}
	userID, ok := optionalPositiveInt64(r.URL.Query().Get("user_id"))
	if !ok {
		writeDashboardError(w, 400, "invalid_query")
		return
	}
	channelID, ok := optionalPositiveInt64(r.URL.Query().Get("channel_id"))
	if !ok {
		writeDashboardError(w, 400, "invalid_query")
		return
	}
	modelName := strings.TrimSpace(r.URL.Query().Get("model_name"))
	matchFilter := strings.TrimSpace(r.URL.Query().Get("match_status"))
	if matchFilter != "" && matchFilter != "matched" && matchFilter != "unmatched" && matchFilter != "multiple" {
		writeDashboardError(w, 400, "invalid_query")
		return
	}
	requestIDs := make([]string, 0, len(items))
	seenIDs := map[string]struct{}{}
	for _, item := range items {
		if item.RequestID != "" {
			if _, seen := seenIDs[item.RequestID]; !seen {
				requestIDs = append(requestIDs, item.RequestID)
				seenIDs[item.RequestID] = struct{}{}
			}
		}
	}
	dimensions, err := h.nginxTimingStore.QueryRequestDimensions(instanceID, requestIDs)
	if err != nil {
		writeDashboardError(w, 500, "query_failed")
		return
	}
	byRequest := make(map[string][]storage.RequestDimension)
	seenMatches := make(map[string]struct{})
	for _, dimension := range dimensions {
		key := dimension.RequestID + ":" + strconv.FormatInt(dimension.SourceLogID, 10)
		if _, seen := seenMatches[key]; seen {
			continue
		}
		seenMatches[key] = struct{}{}
		byRequest[dimension.RequestID] = append(byRequest[dimension.RequestID], dimension)
	}
	out := make([]NginxSlowSampleSummary, 0, len(items))
	for _, v := range items {
		summary := NginxSlowSampleSummary{ID: v.ID, OccurredAt: v.OccurredAt, Path: v.Path, Status: v.Status, RT: v.RT, UHT: v.UHT, URT: v.URT, Bytes: v.Bytes, RequestID: v.RequestID, MatchStatus: "unmatched"}
		matches := byRequest[v.RequestID]
		summary.MatchCount = len(matches)
		if len(matches) == 1 {
			d := matches[0]
			summary.MatchStatus = "matched"
			summary.UserID = d.UserID
			summary.UserName = d.Username
			summary.ChannelID = d.ChannelID
			summary.ModelName = d.ModelName
			summary.TokenName = d.TokenName
			if summary.UserName == "" && h.names != nil {
				summary.UserName = h.names.UserName(instanceID, d.UserID)
			}
			if h.names != nil {
				summary.ChannelName = h.names.ChannelName(instanceID, d.ChannelID)
			}
		} else if len(matches) > 1 {
			summary.MatchStatus = "multiple"
		}
		if userID > 0 && summary.UserID != userID || channelID > 0 && summary.ChannelID != channelID || modelName != "" && summary.ModelName != modelName || matchFilter != "" && summary.MatchStatus != matchFilter {
			continue
		}
		out = append(out, summary)
	}
	if offset >= len(out) {
		out = []NginxSlowSampleSummary{}
	} else {
		end := offset + limit
		if end > len(out) {
			end = len(out)
		}
		out = out[offset:end]
	}
	writeDashboardJSON(w, 200, map[string]any{"items": out})
}

func optionalPositiveInt64(raw string) (int64, bool) {
	if raw == "" {
		return 0, true
	}
	v, err := strconv.ParseInt(raw, 10, 64)
	return v, err == nil && v > 0
}
func boundedInt(raw string, fallback, min, max int) (int, bool) {
	if raw == "" {
		return fallback, true
	}
	v, e := strconv.Atoi(raw)
	return v, e == nil && v >= min && v <= max
}
