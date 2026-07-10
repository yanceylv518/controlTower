package healthcheck

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestCheckerReportsUpForSuccessStatus(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()

	checker := New(time.Second)
	payload := checker.Check(context.Background(), server.URL)
	if payload.Status != "up" || payload.HTTPStatusCode != http.StatusNoContent {
		t.Fatalf("unexpected payload: %#v", payload)
	}
	if payload.LatencyMS < 0 {
		t.Fatalf("latency should not be negative: %#v", payload)
	}
}

func TestCheckerReportsDownForServerError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()

	checker := New(time.Second)
	payload := checker.Check(context.Background(), server.URL)
	if payload.Status != "down" || payload.HTTPStatusCode != http.StatusInternalServerError {
		t.Fatalf("unexpected payload: %#v", payload)
	}
}

func TestSummarizeErrorTrimsControlCharactersAndLength(t *testing.T) {
	message := summarizeError(errors.New("first line\r\n" + strings.Repeat("x", 250)))
	if len(message) > 200 {
		t.Fatalf("message too long: %d", len(message))
	}
	if strings.ContainsAny(message, "\r\n") {
		t.Fatalf("message contains control newline: %q", message)
	}
}
