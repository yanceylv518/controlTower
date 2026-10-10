package dashboard

import (
	"context"
	es "controltower/internal/errorstats"
	ctauth "controltower/server/internal/auth"
	"net/http"
	"strconv"
	"strings"
	"time"
)

func (h Handler) HandleErrorStatistics(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeDashboardError(w, 405, "method_not_allowed")
		return
	}
	p := r.URL.Query()
	dimension, key := p.Get("dimension_type"), p.Get("dimension_key")
	marker := map[string]string{"instance_user": ":user:", "instance_channel": ":channel:", "instance_model": ":model:"}[dimension]
	instance, value, ok := strings.Cut(key, marker)
	hours, err := strconv.Atoi(p.Get("hours"))
	if marker == "" || !ok || instance == "" || value == "" || hours < 1 || hours > 24 || err != nil || len(value) > 200 || len(p.Get("code")) > 80 {
		writeDashboardError(w, 400, "invalid_query")
		return
	}
	if p.Get("instance_id") != "" && p.Get("instance_id") != instance {
		writeDashboardError(w, 400, "invalid_query")
		return
	}
	channels := true
	if u, ok := ctauth.CurrentUser(r); ok && u.Role == "viewer" {
		allowed := false
		id, _ := strconv.ParseInt(value, 10, 64)
		for _, candidate := range u.ScopeUserIDs {
			if candidate == id && id > 0 {
				allowed = true
			}
		}
		ids, e := h.instanceIDsForRequest("", u.ScopeSite)
		if e != nil {
			writeDashboardError(w, 500, "query_failed")
			return
		}
		if dimension != "instance_user" || !allowed || !instanceIDSet(ids)[instance] {
			writeDashboardError(w, 403, "forbidden")
			return
		}
		channels = false
	} else if p.Get("site") != "" {
		ids, e := h.instanceIDsForRequest("", p.Get("site"))
		if e != nil {
			writeDashboardError(w, 500, "query_failed")
			return
		}
		if !instanceIDSet(ids)[instance] {
			writeDashboardError(w, 403, "forbidden")
			return
		}
	}
	source, ok := h.metricSource.(es.Source)
	if !ok {
		writeDashboardError(w, 503, "statistics_unavailable")
		return
	}
	until := time.Now().UTC().Truncate(time.Minute)
	if raw := p.Get("until"); raw != "" {
		unix, e := strconv.ParseInt(raw, 10, 64)
		candidate := time.Unix(unix, 0).UTC()
		if e != nil || candidate.After(until) || candidate.Before(until.Add(-7*24*time.Hour)) {
			writeDashboardError(w, 400, "invalid_query")
			return
		}
		until = candidate.Truncate(time.Minute)
	}
	since := until.Add(-time.Duration(hours) * time.Hour)
	timeline := p.Get("timeline") == "true"
	bucketSeconds := int64(60)
	if timeline {
		if p.Get("bucket") == "5m" {
			bucketSeconds = 300
		} else if p.Get("bucket") != "1m" {
			writeDashboardError(w, 400, "invalid_bucket")
			return
		}
		a, e1 := time.Parse(time.RFC3339, p.Get("start_time"))
		b, e2 := time.Parse(time.RFC3339, p.Get("end_time"))
		if e1 != nil || e2 != nil || !a.Before(b) || b.Sub(a) > 24*time.Hour || b.After(time.Now().UTC().Truncate(time.Duration(bucketSeconds)*time.Second).Add(time.Duration(bucketSeconds)*time.Second)) || a.Before(time.Now().UTC().Add(-7*24*time.Hour)) || a.Nanosecond() != 0 || b.Nanosecond() != 0 || a.Unix()%bucketSeconds != 0 || b.Unix()%bucketSeconds != 0 {
			writeDashboardError(w, 400, "invalid_time_range")
			return
		}
		since, until = a.UTC(), b.UTC()
	}
	if _, _, valid := monitorErrorFilter(dimension, value); !valid {
		writeDashboardError(w, 400, "invalid_dimension")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	result, err := source.QueryErrorStatistics(ctx, es.Query{Timeline: timeline, BucketSeconds: bucketSeconds, Details: p.Get("details") == "true", InstanceID: instance, Dimension: dimension, Key: value, Code: p.Get("code"), Since: since, Until: until, Channels: channels})
	if err != nil {
		writeDashboardError(w, 500, "query_failed")
		return
	}
	writeDashboardJSON(w, 200, result)
}
