package alblog

import (
	"bytes"
	"context"
	"controltower/server/internal/secrets"
	"encoding/json"

	"fmt"
	"net/http"
	"time"
)

const monitorRowLimit = 6000

// Range is either a recent window or an explicit, minute-aligned [From, To).
// The zero value preserves the original one-hour query.
type MonitorRange struct {
	Minutes  int
	From, To int64
}

func (r MonitorRange) Valid(now time.Time) bool {
	if r.From == 0 && r.To == 0 {
		return r.Minutes >= 0 && r.Minutes <= 1440
	}
	return r.Minutes == 0 && r.From > 0 && r.To > r.From && r.To-r.From <= 86400 && r.From%60 == 0 && r.To%60 == 0 && r.To <= now.Unix()
}

type Minute struct {
	Time            int64  `json:"time"`
	Host            string `json:"host"`
	Count           int64  `json:"count"`
	Small           int64  `json:"small"`
	Medium          int64  `json:"medium"`
	Large           int64  `json:"large"`
	Huge            int64  `json:"huge"`
	From20To30      int64  `json:"from_20_to_30"`
	From30To40      int64  `json:"from_30_to_40"`
	AtLeast40       int64  `json:"at_least_40"`
	Unknown         int64  `json:"unknown"`
	RequestBytes    int64  `json:"request_bytes"`
	ResponseBytes   int64  `json:"response_bytes"`
	ResponseUnknown int64  `json:"response_unknown"`
	Latest          int64  `json:"latest"`
}
type Monitor struct {
	RangeSupported bool     `json:"range_supported"`
	Status         string   `json:"status"`
	Code           string   `json:"code,omitempty"`
	QueriedAt      int64    `json:"queried_at"`
	From           int64    `json:"from"`
	To             int64    `json:"to"`
	Latest         int64    `json:"latest"`
	SettledBefore  int64    `json:"settled_before"`
	Rows           []Minute `json:"rows"`
}

// Aggregate across hosts in SLS so a 24-hour query has at most 1441 rows.
// Never fetch request bodies, URLs or credentials.
func monitorSQL(id string) string {
	return fmt.Sprintf(`* | SELECT t AS minute, '' AS host, count(*) AS count,
 sum(CASE WHEN n >= 0 AND n < 5242880 THEN 1 ELSE 0 END) AS small,
 sum(CASE WHEN n >= 5242880 AND n < 10485760 THEN 1 ELSE 0 END) AS medium,
 sum(CASE WHEN n >= 10485760 AND n < 20971520 THEN 1 ELSE 0 END) AS large,
 sum(CASE WHEN n >= 20971520 THEN 1 ELSE 0 END) AS huge,
 sum(CASE WHEN n >= 20971520 AND n < 31457280 THEN 1 ELSE 0 END) AS from_20_to_30,
 sum(CASE WHEN n >= 31457280 AND n < 41943040 THEN 1 ELSE 0 END) AS from_30_to_40,
 sum(CASE WHEN n >= 41943040 THEN 1 ELSE 0 END) AS at_least_40,
 sum(CASE WHEN n IS NULL OR n < 0 THEN 1 ELSE 0 END) AS unknown,
 sum(CASE WHEN n >= 0 THEN n ELSE 0 END) AS request_bytes,
 sum(CASE WHEN b >= 0 THEN b ELSE 0 END) AS response_bytes,
 sum(CASE WHEN b IS NULL OR b < 0 THEN 1 ELSE 0 END) AS response_unknown,
 max(ts) AS latest
 FROM (SELECT __time__ - __time__ %% 60 AS t, __time__ AS ts,
 try_cast(request_length AS bigint) AS n,
 try_cast(body_bytes_sent AS bigint) AS b FROM log WHERE app_lb_id = '%s') AS samples
 GROUP BY t ORDER BY t LIMIT %d`, id, monitorRowLimit+1)
}

func (c Client) Monitor(ctx context.Context, cfg Config) Monitor {
	return c.MonitorRange(ctx, cfg, MonitorRange{})
}

func (c Client) MonitorRange(ctx context.Context, cfg Config, window MonitorRange) Monitor {
	now := time.Now().UTC()
	if c.Now != nil {
		now = c.Now().UTC()
	}
	minutes := window.Minutes
	if minutes == 0 {
		minutes = 60
	}
	out := Monitor{RangeSupported: true, Status: "failed", QueriedAt: now.Unix(), From: now.Truncate(time.Minute).Add(-time.Duration(minutes) * time.Minute).Unix(), To: now.Unix(), Rows: []Minute{}}
	history := window.From != 0 || window.To != 0
	if history {
		out.From, out.To = window.From, window.To
	}
	fail := func(code string) Monitor {
		out.Code = code
		out.Rows = []Minute{}
		out.Latest = 0
		out.SettledBefore = 0
		return out
	}
	if !window.Valid(now) {
		return fail("alb_invalid_range")
	}
	if cfg.Normalize() != nil {
		return fail("alb_invalid_config")
	}
	rows, code := c.queryMonitor(ctx, cfg, out.From, out.To, monitorSQL(cfg.ALBID), now)
	if code != "" {
		return fail(code)
	}
	if len(rows) > monitorRowLimit {
		return fail("alb_too_many_hosts")
	}
	seen := map[string]bool{}
	for _, row := range rows {
		var m Minute
		if json.Unmarshal(row["host"], &m.Host) != nil || len(m.Host) > 1024 {
			return fail("alb_invalid_response")
		}
		values := map[string]*int64{"minute": &m.Time, "count": &m.Count, "small": &m.Small, "medium": &m.Medium, "large": &m.Large, "huge": &m.Huge, "unknown": &m.Unknown, "request_bytes": &m.RequestBytes, "response_bytes": &m.ResponseBytes, "response_unknown": &m.ResponseUnknown, "latest": &m.Latest}
		values["from_20_to_30"], values["from_30_to_40"], values["at_least_40"] = &m.From20To30, &m.From30To40, &m.AtLeast40
		for key, dest := range values {
			n, ok := number(row[key])
			if !ok || n < 0 || n > 9007199254740991 {
				return fail("alb_invalid_response")
			}
			*dest = n
		}
		// Keep the original aggregates for existing clients and verify that the
		// detailed, disjoint buckets account for exactly the same requests.
		if m.Huge != m.From20To30+m.From30To40+m.AtLeast40 {
			return fail("alb_invalid_response")
		}
		key := fmt.Sprintf("%d:%s", m.Time, m.Host)
		if seen[key] || m.Time%60 != 0 || m.Time < out.From || m.Time >= out.To || m.Latest < m.Time || m.Latest >= m.Time+60 || m.Latest >= out.To || m.Count == 0 || m.Count != m.Small+m.Medium+m.Large+m.Huge+m.Unknown || m.ResponseUnknown > m.Count {
			return fail("alb_invalid_response")
		}
		seen[key] = true
		if m.Latest > out.Latest {
			out.Latest = m.Latest
		}
		out.Rows = append(out.Rows, m)
	}
	out.Status = "success"
	if len(out.Rows) == 0 {
		out.Status = "no_data"
		return out
	}
	// This is a conservative display holdback, NOT an SLS delivery watermark.
	// Missing minutes remain unknown; late logs are re-read every refresh.
	out.SettledBefore = now.Truncate(time.Minute).Add(-2 * time.Minute).Unix()
	if history {
		// Old ranges do not use the latest log as a delivery watermark: sparse
		// historical data must include its final populated minute.
		out.SettledBefore = min(out.SettledBefore, out.To)
		return out
	}
	if lastMinute := out.Latest - out.Latest%60; lastMinute < out.SettledBefore {
		out.SettledBefore = lastMinute
	}
	if now.Unix()-out.Latest > 180 {
		out.Status = "delayed"
	}
	return out
}

func (c Client) queryMonitor(ctx context.Context, cfg Config, from, to int64, query string, now time.Time) ([]map[string]json.RawMessage, string) {
	secret, err := secrets.Decrypt(c.SecretKey, cfg.SecretCipher)
	if err != nil || secret == "" {
		return nil, "alb_secret_unavailable"
	}
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	body, _ := json.Marshal(map[string]any{"from": from, "to": to, "query": query, "line": 0, "offset": 0})
	path := "/logstores/" + cfg.Logstore + "/logs"
	hc := http.Client{Timeout: 15 * time.Second}
	if c.HTTP != nil {
		hc = *c.HTTP
	}
	hc.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	for attempt := 0; attempt < 3; attempt++ {
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://"+cfg.Project+"."+cfg.Endpoint+path, bytes.NewReader(body))
		if err != nil {
			return nil, "alb_invalid_config"
		}
		req.Header.Set("Accept-Encoding", "gzip")
		sign(req, body, cfg.AccessKeyID, secret, now)
		resp, err := hc.Do(req)
		if err != nil {
			if ctx.Err() != nil {
				return nil, "alb_timeout"
			}
			return nil, "alb_network_failed"
		}
		data, readErr := readResponseLimit(resp, 4*1024*1024)
		resp.Body.Close()
		if resp.StatusCode != 200 {
			codes := map[int]string{401: "alb_auth_failed", 403: "alb_auth_failed", 404: "alb_source_not_found", 400: "alb_query_failed", 429: "alb_rate_limited"}
			if code := codes[resp.StatusCode]; code != "" {
				return nil, code
			}
			return nil, "alb_service_failed"
		}
		if readErr != nil {
			return nil, "alb_invalid_response"
		}
		var reply struct {
			Meta struct {
				Progress string `json:"progress"`
			} `json:"meta"`
			Data []map[string]json.RawMessage `json:"data"`
		}
		if json.Unmarshal(data, &reply) != nil {
			return nil, "alb_invalid_response"
		}
		if reply.Meta.Progress == "Complete" {
			if reply.Data == nil {
				return nil, "alb_invalid_response"
			}
			return reply.Data, ""
		}
		if reply.Meta.Progress != "Incomplete" {
			return nil, "alb_invalid_response"
		}
		if attempt < 2 {
			timer := time.NewTimer(200 * time.Millisecond)
			select {
			case <-ctx.Done():
				timer.Stop()
				return nil, "alb_timeout"
			case <-timer.C:
			}
		}
	}
	return nil, "alb_query_incomplete"
}
