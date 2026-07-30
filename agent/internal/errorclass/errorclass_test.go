package errorclass

import "testing"

func TestExtractStatusCodePatterns(t *testing.T) {
	tests := []struct {
		summary string
		want    int
	}{
		{"upstream returned status code 400", 400},
		{"request failed statusCode=413", 413},
		{"request failed statusCode: 422", 422},
		{`provider error {"code": 502}`, 502},
		{"HTTP 504 Gateway Timeout", 504},
	}
	for _, test := range tests {
		got, ok := ExtractStatusCode(test.summary)
		if !ok || got != test.want {
			t.Fatalf("ExtractStatusCode(%q) = %d, %v; want %d, true", test.summary, got, ok, test.want)
		}
	}
	if _, ok := ExtractStatusCode("connection reset by peer"); ok {
		t.Fatal("unparseable summary must not return a status code")
	}
}

func TestExtractStatusCodeUsesPatternPrecedence(t *testing.T) {
	got, ok := ExtractStatusCode(`HTTP 502 then status code 400`)
	if !ok || got != 400 {
		t.Fatalf("got %d, %v; want status-code pattern value 400", got, ok)
	}
}

func TestIsUserError(t *testing.T) {
	codes := map[int]bool{400: true, 413: true, 422: true}
	if !IsUserError("status code 400", codes) {
		t.Fatal("configured status code must be user-side")
	}
	if IsUserError("status code 502", codes) || IsUserError("timeout", codes) {
		t.Fatal("unconfigured or unknown errors must remain channel-side")
	}
}
