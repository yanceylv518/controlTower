package dashboard

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

var monitorCodeScalar = regexp.MustCompile(`^[A-Za-z0-9_.-]{1,80}$`)
var monitorCodeText = regexp.MustCompile(`(?i)\b(?:status[_ ]code|http[_ ]status(?:[_ ]code)?|error_code|code)\s*[=:：]\s*["']?([A-Za-z0-9_.-]{1,80})\b`)

func monitorErrorCode(other, content string) string {
	var read func(map[string]any, int) string
	read = func(v map[string]any, depth int) string {
		if depth > 8 {
			return ""
		}
		for _, key := range []string{"status_code", "http_status_code", "error_code", "code"} {
			var s string
			switch x := v[key].(type) {
			case string:
				s = strings.TrimSpace(x)
			case json.Number:
				s = x.String()
			}
			if monitorCodeScalar.MatchString(s) {
				return s
			}
		}
		if child, ok := v["error"].(map[string]any); ok {
			return read(child, depth+1)
		}
		return ""
	}
	for _, raw := range []string{other, content} {
		var value map[string]any
		decoder := json.NewDecoder(strings.NewReader(raw))
		decoder.UseNumber()
		if decoder.Decode(&value) == nil {
			if code := read(value, 0); code != "" {
				return code
			}
		}
		if m := monitorCodeText.FindStringSubmatch(raw); len(m) > 1 {
			return m[1]
		}
	}
	return ""
}

type monitorErrorItem struct {
	Code  string `json:"code"`
	Count int64  `json:"count"`
}

func monitorErrorFilter(kind, value string) (string, any, bool) {
	switch kind {
	case "instance_user", "instance_channel":
		id, err := strconv.ParseInt(value, 10, 64)
		if err != nil || id <= 0 {
			return "", nil, false
		}
		if kind == "instance_user" {
			return "user_id", id, true
		}
		return "channel_id", id, true
	case "instance_model":
		if value == "" || len(value) > 256 {
			return "", nil, false
		}
		return "model_name", value, true
	}
	return "", nil, false
}

type monitorErrorBucket struct {
	Time   time.Time        `json:"time"`
	Counts map[string]int64 `json:"counts"`
}

func monitorBucketUnix(timestamp, seconds int64) int64 { return timestamp / seconds * seconds }

// Exact within the requested source-log window; never publish a truncated histogram.
func (h *PassthroughHandler) MonitorErrorCodes(w http.ResponseWriter, r *http.Request) {
	site, _, err := passthroughScope(r)
	if err != nil || readonlyViewer(r) {
		writeDashboardError(w, 400, "invalid_scope")
		return
	}
	column, value, ok := monitorErrorFilter(r.URL.Query().Get("dimension_type"), r.URL.Query().Get("value"))
	if !ok {
		writeDashboardError(w, 400, "invalid_dimension")
		return
	}
	start, end, err := queryWindow(r)
	if err != nil {
		writeDashboardError(w, 400, "invalid_time_range")
		return
	}
	bucketSeconds := int64(60)
	if r.URL.Query().Get("bucket") == "5m" {
		bucketSeconds = 300
	} else if b := r.URL.Query().Get("bucket"); b != "" && b != "1m" {
		writeDashboardError(w, 400, "invalid_bucket")
		return
	}
	if end.Sub(start)/time.Duration(bucketSeconds*int64(time.Second)) > 2000 {
		writeDashboardError(w, 422, "error_statistics_limit")
		return
	}
	db, configured, err := h.database(site)
	if err != nil {
		writeDashboardError(w, 502, "readonly_connection_failed")
		return
	}
	if !configured {
		writeDashboardJSON(w, 200, map[string]any{"configured": false, "items": []monitorErrorItem{}, "total": 0})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	rows, err := db.QueryContext(ctx, "SELECT /*+ MAX_EXECUTION_TIME(4000) */ created_at,LEFT(other,65537),LEFT(content,65537) FROM logs WHERE created_at>=? AND created_at<? AND type=5 AND "+column+"=? LIMIT 20001", start.Unix(), end.Unix(), value)
	if err != nil {
		writeDashboardError(w, 502, "readonly_query_failed")
		return
	}
	defer rows.Close()
	counts := map[string]int64{}
	buckets := map[int64]map[string]int64{}
	var total int64
	bytes := 0
	for rows.Next() {
		var other, content sql.NullString
		var created int64
		if err = rows.Scan(&created, &other, &content); err != nil {
			writeDashboardError(w, 502, "readonly_query_failed")
			return
		}
		total++
		bytes += len(other.String) + len(content.String)
		if total > 20000 || bytes > 32*1024*1024 || len(other.String) > 65536 || len(content.String) > 65536 {
			writeDashboardError(w, 422, "error_statistics_limit")
			return
		}
		code := monitorErrorCode(other.String, content.String)
		counts[code]++
		bucket := monitorBucketUnix(created, bucketSeconds)
		if buckets[bucket] == nil {
			buckets[bucket] = map[string]int64{}
		}
		buckets[bucket][code]++
	}
	if rows.Err() != nil {
		writeDashboardError(w, 502, "readonly_query_failed")
		return
	}
	items := make([]monitorErrorItem, 0, len(counts))
	for code, count := range counts {
		items = append(items, monitorErrorItem{code, count})
	}
	sort.Slice(items, func(i, j int) bool {
		if items[i].Count == items[j].Count {
			return items[i].Code < items[j].Code
		}
		return items[i].Count > items[j].Count
	})
	timeline := make([]monitorErrorBucket, 0, len(buckets))
	for bucket, counts := range buckets {
		timeline = append(timeline, monitorErrorBucket{time.Unix(bucket, 0).UTC(), counts})
	}
	sort.Slice(timeline, func(i, j int) bool { return timeline[i].Time.Before(timeline[j].Time) })
	writeDashboardJSON(w, 200, map[string]any{"buckets": timeline, "bucket_seconds": bucketSeconds, "configured": true, "items": items, "total": total, "start_time": start, "end_time": end})
}
