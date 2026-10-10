package logcollector

import (
	"controltower/agent/internal/errorclass"
	"testing"
)

func TestErrorCodeClassification(t *testing.T) {
	cases := []struct {
		content, other, code string
		legacy               int
	}{
		{"upstream HTTP 502; status code 400", "", "http:400", 400},
		{`{"error":{"code":"rate_limit_exceeded"}}`, "", "business:rate_limit_exceeded", 0},
		{`{"error":{"code":429}}`, "", "business:429", 429},
		{`{"error":{"code":"quota"},"status_code":429}`, "", "http:429", 0},
		{"failed", `{"status_code":503}`, "http:503", 0},
		{"HTTP 502", `{"error_code":"upstream_error"}`, "http:502", 502},
		{`HTTP 502; "code":400`, "", "http:502", 400},
		{`{"error":{"code":"Bearer secret token"}}`, "", "unknown", 0},
		{"failed after 429 tokens", "", "unknown", 0},
	}
	for _, tc := range cases {
		t.Run(tc.code+tc.content, func(t *testing.T) {
			e, ok, err := ConvertRow(Row{Type: 5, Content: tc.content, Other: tc.other})
			if err != nil || !ok || e.ErrorCode != tc.code || e.HTTPStatus != tc.legacy {
				t.Fatalf("got %+v %v", e, err)
			}
			e.ErrorSummary = "HTTP 503"
			e.ClassifyError()
			if e.HTTPStatus != tc.legacy {
				t.Fatal("cached classification reparsed")
			}
		})
	}
	e, _, _ := ConvertRow(Row{Type: 2, Content: "HTTP 500"})
	if e.ErrorParsed || e.ErrorCode != "" {
		t.Fatal("consume treated as error code")
	}
}
func BenchmarkErrorClassificationFanout(b *testing.B) {
	text := "upstream request failed: status code 429, please retry later"
	codes := map[int]bool{400: true, 413: true}
	b.Run("legacy-eight-consumers", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			for j := 0; j < 8; j++ {
				_ = errorclass.IsUserError(text, codes)
			}
		}
	})
	b.Run("cached-eight-consumers", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			e := Event{LogType: "error", ErrorSummary: text}
			classifyRowError(&e, text, "")
			for j := 0; j < 8; j++ {
				e.ClassifyError()
				_ = codes[e.HTTPStatus]
			}
		}
	})
}

func TestStatisticsHTTPCodeCanonicalization(t *testing.T) {
	for _, raw := range []string{`{"status_code":"00429"}`, `{"status_code":"+429"}`} {
		e, _, err := ConvertRow(Row{Type: 5, Other: raw})
		if err != nil || e.ErrorCode != "http:429" {
			t.Fatalf("noncanonical or lost status: %q %v", e.ErrorCode, err)
		}
	}
}
func TestStatisticsNamedTextBusinessCodes(t *testing.T) {
	for _, raw := range []string{"error_code=quota_exhausted", "code: timeout", "http_status_code=503"} {
		e, _, err := ConvertRow(Row{Type: 5, Other: raw})
		if err != nil || e.ErrorCode == "unknown" {
			t.Fatalf("named code lost: %q => %q", raw, e.ErrorCode)
		}
	}
}
