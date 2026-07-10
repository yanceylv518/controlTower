package reporter

import (
	"compress/gzip"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestHeartbeatSendsBearerTokenAndPayload(t *testing.T) {
	var path string
	var authHeader string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path = r.URL.Path
		authHeader = r.Header.Get("Authorization")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"accepted":true,"server_last_log_id":123}`))
	}))
	defer server.Close()

	client := NewClient(server.URL, "agent-token", time.Second)
	response, err := client.Heartbeat(context.Background(), AgentHeartbeatRequest{
		InstanceID:   "inst-1",
		AgentID:      "agent-1",
		AgentVersion: "0.1.0",
		ReportedAt:   time.Date(2026, 7, 2, 12, 0, 0, 0, time.UTC),
		Sequence:     1,
		LastLogID:    100,
	})
	if err != nil {
		t.Fatalf("heartbeat: %v", err)
	}
	if response.ServerLastLogID != 123 {
		t.Fatalf("server last log id = %d, want 123", response.ServerLastLogID)
	}
	if path != "/api/agent/heartbeat" {
		t.Fatalf("path = %s", path)
	}
	if authHeader != "Bearer agent-token" {
		t.Fatalf("unexpected auth header: %s", authHeader)
	}
}

func TestReportSendsGzippedBearerTokenAndPayload(t *testing.T) {
	var authHeader string
	var method string
	var encoding string
	var decoded AgentReportRequest
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		method = r.Method
		authHeader = r.Header.Get("Authorization")
		encoding = r.Header.Get("Content-Encoding")
		reader, err := gzip.NewReader(r.Body)
		if err != nil {
			t.Fatalf("open gzip body: %v", err)
		}
		defer reader.Close()
		data, err := io.ReadAll(reader)
		if err != nil {
			t.Fatalf("read gzip body: %v", err)
		}
		if err := json.Unmarshal(data, &decoded); err != nil {
			t.Fatalf("decode body: %v", err)
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"accepted":true,"server_last_log_id":123}`))
	}))
	defer server.Close()

	client := NewClient(server.URL, "agent-token", time.Second)
	err := client.Report(context.Background(), AgentReportRequest{
		InstanceID:   "inst-1",
		AgentID:      "agent-1",
		AgentVersion: "0.1.0",
		ReportedAt:   time.Date(2026, 7, 2, 12, 0, 0, 0, time.UTC),
		Sequence:     1,
	})
	if err != nil {
		t.Fatalf("report: %v", err)
	}

	if method != http.MethodPost {
		t.Fatalf("unexpected method: %s", method)
	}
	if authHeader != "Bearer agent-token" {
		t.Fatalf("unexpected auth header: %s", authHeader)
	}
	if encoding != "gzip" {
		t.Fatalf("unexpected encoding: %s", encoding)
	}
	if decoded.InstanceID != "inst-1" || decoded.AgentID != "agent-1" {
		t.Fatalf("unexpected payload: %#v", decoded)
	}
}

func TestReportReturnsErrorOnNonSuccess(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer server.Close()

	client := NewClient(server.URL, "bad-token", time.Second)
	err := client.Report(context.Background(), AgentReportRequest{})
	if err == nil {
		t.Fatalf("expected error")
	}
}

func TestBackoffDelay(t *testing.T) {
	cases := []struct {
		failures int
		want     time.Duration
	}{
		{0, 0},
		{1, 10 * time.Second},
		{2, 30 * time.Second},
		{3, 60 * time.Second},
		{4, 120 * time.Second},
		{5, 300 * time.Second},
		{9, 300 * time.Second},
	}
	for _, tc := range cases {
		if got := BackoffDelay(tc.failures); got != tc.want {
			t.Fatalf("BackoffDelay(%d)=%s want %s", tc.failures, got, tc.want)
		}
	}
}
