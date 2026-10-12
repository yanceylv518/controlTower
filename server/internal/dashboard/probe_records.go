package dashboard

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"controltower/server/internal/tuning"
)

func probeQuery(r *http.Request) (tuning.ProbeRecordQuery, error) {
	now := time.Now().UTC()
	q := tuning.ProbeRecordQuery{Site: tuningSiteID(r), Now: now, From: now.Add(-time.Hour), To: now, Limit: 50}
	values := r.URL.Query()
	q.Model = strings.TrimSpace(values.Get("model"))
	q.Kind = values.Get("kind")
	q.Search = strings.TrimSpace(values.Get("q"))
	q.Outcome = values.Get("outcome")
	q.Cursor = values.Get("cursor")
	invalid := fmt.Errorf("invalid probe query")
	if q.Site == "" || len(q.Site) > 64 || len(q.Model) > 255 || len(q.Search) > 100 {
		return q, invalid
	}
	var err error
	if raw := values.Get("channel_id"); raw != "" {
		q.ChannelID, err = strconv.ParseInt(raw, 10, 64)
		if err != nil || q.ChannelID <= 0 {
			return q, invalid
		}
	}
	if raw := values.Get("from"); raw != "" {
		q.From, err = time.Parse(time.RFC3339Nano, raw)
		if err != nil {
			return q, invalid
		}
	}
	if raw := values.Get("to"); raw != "" {
		q.To, err = time.Parse(time.RFC3339Nano, raw)
		if err != nil {
			return q, invalid
		}
	}
	if q.To.Before(q.From) || q.To.Sub(q.From) > 366*24*time.Hour || q.To.After(now.Add(time.Minute)) {
		return q, invalid
	}
	if raw := values.Get("limit"); raw != "" {
		q.Limit, err = strconv.Atoi(raw)
		if err != nil || q.Limit < 1 || q.Limit > 200 {
			return q, invalid
		}
	}
	if q.Kind != "" && q.Kind != "scheduled" && q.Kind != "accelerated" && q.Kind != "recovery" {
		return q, invalid
	}
	switch q.Outcome {
	case "", "success", "slow", "error", "unknown", "canceled", "pending", "unconfirmed", "skipped":
	default:
		return q, invalid
	}
	if q.Cursor != "" {
		if len(q.Cursor) > 512 {
			return q, invalid
		}
		var c struct {
			At time.Time `json:"at"`
			ID string    `json:"id"`
		}
		raw, e := base64.RawURLEncoding.DecodeString(q.Cursor)
		if e != nil || json.Unmarshal(raw, &c) != nil || c.At.IsZero() || c.ID == "" || len(c.ID) > 128 {
			return q, invalid
		}
	}
	return q, nil
}
func (h Handler) HandleProbeOverview(w http.ResponseWriter, r *http.Request) {
	q, err := probeQuery(r)
	if err != nil {
		writeDashboardError(w, 400, "invalid_probe_query")
		return
	}
	s, ok := h.tuningStore.(tuning.ProbeRecordsReader)
	if !ok {
		writeDashboardError(w, 501, "probe_records_unavailable")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	v, err := s.QueryProbeOverview(ctx, q)
	if err != nil {
		writeDashboardError(w, 503, "probe_records_unavailable")
		return
	}
	if directory, ok := h.tuningStore.(TuningChannelDirectory); ok {
		channels, e := directory.LatestChannels(q.Site)
		if e != nil {
			writeDashboardError(w, 503, "probe_channels_unavailable")
			return
		}
		seen := map[string]bool{}
		for _, c := range v.Channels {
			seen[fmt.Sprintf("%d/%s", c.ChannelID, c.ModelName)] = true
		}
		for _, c := range channels {
			if q.ChannelID > 0 && q.ChannelID != c.ID {
				continue
			}
			if q.Search != "" && !strings.Contains(strings.ToLower(c.Name), strings.ToLower(q.Search)) && fmt.Sprint(c.ID) != q.Search {
				continue
			}
			for _, model := range c.Models {
				if q.Model != "" && q.Model != model {
					continue
				}
				if !seen[fmt.Sprintf("%d/%s", c.ID, model)] {
					v.Channels = append(v.Channels, tuning.ProbeChannelStats{ChannelID: c.ID, ChannelName: c.Name, ModelName: model})
				}
			}
			for i := range v.Channels {
				if v.Channels[i].ChannelID == c.ID {
					v.Channels[i].ChannelName = c.Name
				}
			}
		}
	}
	writeDashboardJSON(w, 200, v)
}
func (h Handler) HandleProbeRecords(w http.ResponseWriter, r *http.Request) {
	q, err := probeQuery(r)
	if err != nil {
		writeDashboardError(w, 400, "invalid_probe_query")
		return
	}
	s, ok := h.tuningStore.(tuning.ProbeRecordsReader)
	if !ok {
		writeDashboardError(w, 501, "probe_records_unavailable")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	v, err := s.QueryProbeRecords(ctx, q)
	if err != nil {
		writeDashboardError(w, 503, "probe_records_unavailable")
		return
	}
	writeDashboardJSON(w, 200, v)
}
func (h Handler) HandleChannelLifeline(w http.ResponseWriter, r *http.Request) {
	q, err := probeQuery(r)
	source := r.URL.Query().Get("source")
	if source == "" {
		source = "important"
	}
	if err != nil || q.ChannelID <= 0 || (source != "all" && source != "important" && source != "operations" && source != "decisions" && source != "probes") {
		writeDashboardError(w, 400, "invalid_probe_query")
		return
	}
	s, ok := h.tuningStore.(tuning.ChannelLifelineReader)
	if !ok {
		writeDashboardError(w, 501, "channel_lifeline_unavailable")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	v, err := s.QueryChannelLifeline(ctx, q, source)
	if err != nil {
		writeDashboardError(w, 503, "channel_lifeline_unavailable")
		return
	}
	writeDashboardJSON(w, 200, v)
}
