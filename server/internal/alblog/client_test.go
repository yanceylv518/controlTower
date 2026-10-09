package alblog

import (
	"bytes"
	"compress/gzip"
	"context"
	"controltower/server/internal/secrets"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func fixture() Config {
	cipher, _ := secrets.Encrypt("key", "fixture-secret")
	return Config{Endpoint: "cn-hangzhou.log.aliyuncs.com", Project: "pinducloud-alb-logs", Logstore: "alb-access-log", ALBID: "alb-test123", AccessKeyID: "testAccessKey123", SecretCipher: cipher}
}
func TestSignatureGolden(t *testing.T) {
	r, _ := http.NewRequest("POST", "https://pinducloud-alb-logs.cn-hangzhou.log.aliyuncs.com/logstores/alb-access-log/logs", strings.NewReader("{}"))
	sign(r, []byte("{}"), "fixture-id", "fixture-secret", time.Date(2026, 10, 9, 0, 0, 0, 0, time.UTC))
	if got := r.Header.Get("Authorization"); got != "LOG fixture-id:0p1zD39JU54OaRxgiBJ0RWnnEjE=" {
		t.Fatal(got)
	}
	if r.Header.Get("Content-MD5") != "99914B932BD37A50B983C5E7C90AE93B" {
		t.Fatal("wrong body digest")
	}
}
func TestProbeResultsAndBoundedRetries(t *testing.T) {
	now := time.Unix(1791504000, 0).UTC()
	cases := []struct {
		name, body, status, code string
		httpCode, calls          int
		gzip                     bool
	}{
		{"success", `{"meta":{"progress":"Complete"},"data":[{"request_count":"12","latest_log_time":"1791503990"}]}`, "success", "", 200, 1, false},
		{"gzip", `{"meta":{"progress":"Complete"},"data":[{"request_count":12,"latest_log_time":1791503990}]}`, "success", "", 200, 1, true},
		{"empty", `{"meta":{"progress":"Complete"},"data":[{"request_count":"0","latest_log_time":null}]}`, "no_data", "", 200, 1, false},
		{"incomplete", `{"meta":{"progress":"Incomplete"},"data":[{"request_count":"999"}]}`, "incomplete", "alb_query_incomplete", 200, 3, false},
		{"denied", `{"message":"credential that must not escape"}`, "failed", "alb_auth_failed", 403, 1, false},
		{"query", `{}`, "failed", "alb_query_failed", 400, 1, false},
		{"missing", `{}`, "failed", "alb_source_not_found", 404, 1, false},
		{"limit", `{}`, "failed", "alb_rate_limited", 429, 1, false},
		{"redirect", `{}`, "failed", "alb_service_failed", 302, 1, false},
		{"bad", `{"meta":{"progress":"Complete"},"data":[{"request_count":"12","latest_log_time":"bad"}]}`, "failed", "alb_invalid_response", 200, 1, false},
		{"missing_progress", `{"data":[]}`, "failed", "alb_invalid_response", 200, 1, false},
		{"negative", `{"meta":{"progress":"Complete"},"data":[{"request_count":"-1"}]}`, "failed", "alb_invalid_response", 200, 1, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			client := Client{SecretKey: "key", Now: func() time.Time { return now }, HTTP: &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
				calls++
				if r.Method != "POST" || r.URL.Path != "/logstores/alb-access-log/logs" || r.URL.Host != "pinducloud-alb-logs.cn-hangzhou.log.aliyuncs.com" {
					t.Fatal("wrong destination")
				}
				if !strings.HasPrefix(r.Header.Get("Authorization"), "LOG testAccessKey123:") {
					t.Fatal("unsigned")
				}
				body, _ := io.ReadAll(r.Body)
				if !strings.Contains(string(body), "WHERE app_lb_id = 'alb-test123' LIMIT 1") || strings.Contains(string(body), "host =") || strings.Contains(string(body), "fixture-secret") {
					t.Fatal(string(body))
				}
				data := []byte(tc.body)
				headers := http.Header{}
				if tc.gzip {
					var b bytes.Buffer
					w := gzip.NewWriter(&b)
					w.Write(data)
					w.Close()
					data = b.Bytes()
					headers.Set("Content-Encoding", "gzip")
				}
				if tc.httpCode == 302 {
					headers.Set("Location", "https://other.example")
				}
				return &http.Response{StatusCode: tc.httpCode, Body: io.NopCloser(bytes.NewReader(data)), Header: headers}, nil
			})}}
			got := client.Probe(context.Background(), fixture())
			if got.Status != tc.status || got.Code != tc.code || calls != tc.calls {
				t.Fatalf("%+v calls=%d", got, calls)
			}
			if got.From != now.Add(-15*time.Minute).Unix() || got.To != now.Unix() {
				t.Fatal("wrong window")
			}
			if tc.status == "failed" || tc.status == "incomplete" {
				if got.RequestCount != nil || got.LatestLogTime != nil {
					t.Fatal("partial count escaped")
				}
			}
		})
	}
}
func TestConfigRejectsUnexpectedCredentialDestinations(t *testing.T) {
	for _, endpoint := range []string{"http://cn-hangzhou.log.aliyuncs.com", "127.0.0.1", "evil.example", "cn-hangzhou.log.aliyuncs.com.evil.example", "cn-hangzhou.log.aliyuncs.com:8443", "cn-hangzhou.log.aliyuncs.com/path", "cn-hangzhou-intranet.log.aliyuncs.com"} {
		c := fixture()
		c.Endpoint = endpoint
		if c.Normalize() == nil {
			t.Fatal(endpoint)
		}
	}
	c := fixture()
	c.Endpoint = "https://" + c.Project + ".cn-hangzhou.log.aliyuncs.com/"
	if c.Normalize() != nil || c.Endpoint != "cn-hangzhou.log.aliyuncs.com" {
		t.Fatal(c.Endpoint)
	}
	c.ALBID = "alb-test' OR 1=1"
	if c.Normalize() == nil {
		t.Fatal("SQL accepted")
	}
}
func TestProbeCancellationAndInvalidCipher(t *testing.T) {
	calls := 0
	c := Client{SecretKey: "wrong", HTTP: &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) { calls++; return nil, r.Context().Err() })}}
	if got := c.Probe(context.Background(), fixture()); got.Code != "alb_secret_unavailable" || calls != 0 {
		t.Fatal(got)
	}
	c.SecretKey = "key"
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if got := c.Probe(ctx, fixture()); got.Code != "alb_timeout" {
		t.Fatal(got)
	}
}
