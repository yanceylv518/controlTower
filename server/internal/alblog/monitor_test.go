package alblog

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestMonitorAggregateValidation(t *testing.T) {
	now := time.Unix(1791504030, 0)
	minute := now.Truncate(time.Minute).Unix() - 180
	valid := map[string]any{"minute": minute, "host": "example.test", "count": 5, "small": 1, "medium": 1, "large": 1, "huge": 1, "unknown": 1, "request_bytes": 44564480, "response_bytes": 512, "response_unknown": 1, "latest": minute + 50}
	cases := []struct {
		name, progress, code, status string
		change                       func(map[string]any)
		duplicate                    bool
	}{
		{"success", "Complete", "", "success", nil, false},
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
				for _, fragment := range []string{"n < 5242880", "n >= 5242880 AND n < 10485760", "n >= 10485760 AND n < 20971520", "n >= 20971520", "n IS NULL OR n < 0", "b >= 0 THEN b", "GROUP BY t, h", "LIMIT 6001", "app_lb_id = 'alb-test123'"} {
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
		})
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
