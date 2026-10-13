package alblog

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestMonitorAggregateValidation(t *testing.T) {
	now := time.Unix(1791504030, 0)
	minute := now.Truncate(time.Minute).Unix() - 180
	valid := map[string]any{"minute": minute, "host": "example.test", "count": 5, "small": 1, "medium": 1, "large": 1, "huge": 1, "unknown": 1, "request_bytes": 44564480, "response_bytes": 512, "response_unknown": 1, "latest": minute + 50}
	valid["from_20_to_30"], valid["from_30_to_40"], valid["at_least_40"] = 1, 0, 0
	cases := []struct {
		name, progress, code, status string
		change                       func(map[string]any)
		duplicate                    bool
	}{
		{"success", "Complete", "", "success", nil, false},
		{"huge-breakdown-mismatch", "Complete", "alb_invalid_response", "failed", func(m map[string]any) { m["at_least_40"] = 1 }, false},
		{"missing-breakdown", "Complete", "alb_invalid_response", "failed", func(m map[string]any) { delete(m, "from_30_to_40") }, false},
		{"negative-breakdown", "Complete", "alb_invalid_response", "failed", func(m map[string]any) { m["from_20_to_30"] = -1 }, false},
		{"fraction-breakdown", "Complete", "alb_invalid_response", "failed", func(m map[string]any) { m["at_least_40"] = 0.5 }, false},
		{"bad-total", "Complete", "alb_invalid_response", "failed", func(m map[string]any) { m["count"] = 9 }, false},
		{"negative", "Complete", "alb_invalid_response", "failed", func(m map[string]any) { m["request_bytes"] = -1 }, false},
		{"fraction", "Complete", "alb_invalid_response", "failed", func(m map[string]any) { m["large"] = 1.5 }, false},
		{"future", "Complete", "alb_invalid_response", "failed", func(m map[string]any) { m["latest"] = now.Unix() + 60 }, false},
		{"missing", "Complete", "alb_invalid_response", "failed", func(m map[string]any) { delete(m, "unknown") }, false},
		{"duplicate", "Complete", "alb_invalid_response", "failed", nil, true},
		{"incomplete", "Incomplete", "alb_query_incomplete", "failed", nil, false},
		{"delayed", "Complete", "", "delayed", func(m map[string]any) { m["minute"] = minute - 600; m["latest"] = minute - 550 }, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			row := map[string]any{}
			for k, v := range valid {
				row[k] = v
			}
			if tc.change != nil {
				tc.change(row)
			}
			rows := []any{row}
			if tc.duplicate {
				rows = append(rows, row)
			}
			calls := 0
			c := Client{SecretKey: "key", Now: func() time.Time { return now }, HTTP: &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
				calls++
				var input map[string]any
				json.NewDecoder(r.Body).Decode(&input)
				if input["from"] != float64(now.Truncate(time.Minute).Add(-time.Hour).Unix()) || input["to"] != float64(now.Unix()) {
					t.Fatal("unbounded window")
				}
				q := input["query"].(string)
				for _, fragment := range []string{"n < 5242880", "n >= 5242880 AND n < 10485760", "n >= 10485760 AND n < 20971520", "n >= 20971520", "n IS NULL OR n < 0", "b >= 0 THEN b", "GROUP BY t ORDER BY t", "LIMIT 6001", "app_lb_id = 'alb-test123'"} {
					if !strings.Contains(q, fragment) {
						t.Fatal("missing query condition", fragment)
					}
				}
				if strings.Contains(q, "host =") || strings.Contains(q, "SELECT *") {
					t.Fatal("Host or raw-log query")
				}
				data, _ := json.Marshal(map[string]any{"meta": map[string]string{"progress": tc.progress}, "data": rows})
				return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(string(data))), Header: http.Header{}}, nil
			})}}
			result := c.Monitor(context.Background(), fixture())
			if result.Code != tc.code || result.Status != tc.status {
				t.Fatalf("%+v", result)
			}
			if tc.status == "failed" && (len(result.Rows) != 0 || result.SettledBefore != 0) {
				t.Fatal("partial data escaped")
			}
			if calls > 3 {
				t.Fatal("unbounded retries")
			}
			if tc.name == "success" && (result.Rows[0].RequestBytes != 44564480 || result.SettledBefore != minute || len(result.Rows) != 1) {
				t.Fatal(result)
			}
			if tc.name == "success" && (result.Rows[0].From20To30 != 1 || result.Rows[0].AtLeast40 != 0) {
				t.Fatal("detailed buckets lost", result.Rows)
			}
		})
	}
}

func TestMonitorHistoricalAndLongRanges(t *testing.T) {
	now := time.Unix(1791504030, 0)
	end := now.Truncate(time.Minute).Add(-48 * time.Hour).Unix()
	for _, window := range []MonitorRange{{Minutes: 1440}, {From: end - 86400, To: end}} {
		from, to := window.From, window.To
		if window.Minutes != 0 {
			from, to = now.Truncate(time.Minute).Unix()-86400, now.Unix()
		}
		last := to - to%60 - 60
		c := Client{SecretKey: "key", Now: func() time.Time { return now }, HTTP: &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
			var input map[string]any
			json.NewDecoder(r.Body).Decode(&input)
			if input["from"] != float64(from) || input["to"] != float64(to) {
				t.Fatalf("wrong SLS range: %v", input)
			}
			if strings.Contains(input["query"].(string), "GROUP BY t, h") {
				t.Fatal("unbounded per-host grouping")
			}
			row := map[string]any{"minute": last, "host": "", "count": 1, "small": 1, "medium": 0, "large": 0, "huge": 0, "unknown": 0, "from_20_to_30": 0, "from_30_to_40": 0, "at_least_40": 0, "request_bytes": 100, "response_bytes": 200, "response_unknown": 0, "latest": last + 50}
			body, _ := json.Marshal(map[string]any{"meta": map[string]string{"progress": "Complete"}, "data": []any{row}})
			return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(string(body))), Header: http.Header{}}, nil
		})}}
		got := c.MonitorRange(context.Background(), fixture(), window)
		if got.Status != "success" || !got.RangeSupported || got.From != from || got.To != to {
			t.Fatalf("%+v", got)
		}
		if window.From > 0 && got.SettledBefore != to {
			t.Fatalf("final historical minute excluded: %+v", got)
		}
	}
	c := Client{Now: func() time.Time { return now }}
	for _, window := range []MonitorRange{{Minutes: 1441}, {Minutes: -1}, {From: end, To: end}, {From: end + 1, To: end + 60}, {From: end - 86460, To: end}, {From: end, To: now.Unix() + 60}, {Minutes: 30, From: end - 60, To: end}} {
		if got := c.MonitorRange(context.Background(), fixture(), window); got.Code != "alb_invalid_range" {
			t.Fatalf("accepted invalid range %+v: %+v", window, got)
		}
	}
}

func TestMonitorSizeSQLBoundaries(t *testing.T) {
	// Read the ranges from the SQL actually sent to SLS, and check both sides
	// of each MiB boundary, including invalid and zero-length requests.
	ranges := regexp.MustCompile(`sum\(CASE WHEN n >= (\d+)(?: AND n < (\d+))? THEN 1 ELSE 0 END\) AS (\w+)`).FindAllStringSubmatch(monitorSQL("alb-test123"), -1)
	detailed := map[string]bool{"small": true, "medium": true, "large": true, "from_20_to_30": true, "from_30_to_40": true, "at_least_40": true}
	cases := []struct {
		size int64
		want string
	}{
		{-1, ""}, {0, "small"}, {5242879, "small"}, {5242880, "medium"},
		{10485759, "medium"}, {10485760, "large"}, {20971519, "large"}, {20971520, "from_20_to_30"},
		{31457279, "from_20_to_30"}, {31457280, "from_30_to_40"}, {41943039, "from_30_to_40"},
		{41943040, "at_least_40"}, {1073741824, "at_least_40"},
	}
	for _, tc := range cases {
		var matched []string
		for _, r := range ranges {
			if !detailed[r[3]] {
				continue
			}
			min, _ := strconv.ParseInt(r[1], 10, 64)
			max, _ := strconv.ParseInt(r[2], 10, 64)
			if tc.size >= min && (r[2] == "" || tc.size < max) {
				matched = append(matched, r[3])
			}
		}
		if (tc.want == "" && len(matched) != 0) || (tc.want != "" && (len(matched) != 1 || matched[0] != tc.want)) {
			t.Fatalf("size %d: SQL matched %v, want %q", tc.size, matched, tc.want)
		}
	}
}
func TestMonitorRejectsMissingDataAndOverflow(t *testing.T) {
	for _, body := range []string{`{"meta":{"progress":"Complete"}}`, strings.Repeat("x", 4*1024*1024+1)} {
		c := Client{SecretKey: "key", HTTP: &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
			return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(body))}, nil
		})}}
		if got := c.Monitor(context.Background(), fixture()); got.Code != "alb_invalid_response" {
			t.Fatal(got)
		}
	}
}
