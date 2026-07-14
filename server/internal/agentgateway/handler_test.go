package agentgateway

import (
	"bytes"
	"compress/gzip"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

type memorySink struct {
	heartbeatCount int
	reportCount    int
	lastHeartbeat  AgentHeartbeatRequest
	lastReport     AgentReportRequest
	serverLastLogID int64
}

func (s *memorySink) SaveHeartbeat(req AgentHeartbeatRequest) (int64, error) {
	s.heartbeatCount++
	s.lastHeartbeat = req
	return s.serverLastLogID, nil
}

func (s *memorySink) SaveReport(req AgentReportRequest) error {
	s.reportCount++
	s.lastReport = req
	return nil
}

func TestHandleReportRejectsMissingToken(t *testing.T) {
	sink := &memorySink{}
	handler := NewHandler("expected-token", sink)
	body := bytes.NewBufferString(`{"instance_id":"inst-1"}`)

	req := httptest.NewRequest(http.MethodPost, "/api/agent/report", body)
	rr := httptest.NewRecorder()
	handler.HandleReport(rr, req)

	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", rr.Code)
	}
	if sink.reportCount != 0 {
		t.Fatalf("report should not be saved")
	}
}

func TestHandleReportAcceptsValidPayload(t *testing.T) {
	sink := &memorySink{}
	handler := NewHandler("expected-token", sink)
	payload := AgentReportRequest{
		InstanceID:   "inst-1",
		AgentID:      "agent-1",
		AgentVersion: "0.1.0",
		ReportedAt:   time.Date(2026, 7, 2, 12, 0, 0, 0, time.UTC),
		Sequence:     11,
	}
	data, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/api/agent/report", bytes.NewReader(data))
	req.Header.Set("Authorization", "Bearer expected-token")
	rr := httptest.NewRecorder()
	handler.HandleReport(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", rr.Code, rr.Body.String())
	}
	if sink.reportCount != 1 {
		t.Fatalf("expected saved report")
	}
	if sink.lastReport.InstanceID != "inst-1" {
		t.Fatalf("unexpected instance: %#v", sink.lastReport)
	}
}

func TestHandleReportAcceptsGzipPayload(t *testing.T) {
	sink := &memorySink{}
	handler := NewHandler("expected-token", sink)
	payload := AgentReportRequest{
		InstanceID:   "inst-1",
		AgentID:      "agent-1",
		AgentVersion: "0.1.0",
		ReportedAt:   time.Date(2026, 7, 2, 12, 0, 0, 0, time.UTC),
		Sequence:     11,
	}
	data, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}
	var body bytes.Buffer
	writer := gzip.NewWriter(&body)
	if _, err := writer.Write(data); err != nil {
		t.Fatalf("write gzip payload: %v", err)
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("close gzip payload: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/api/agent/report", &body)
	req.Header.Set("Authorization", "Bearer expected-token")
	req.Header.Set("Content-Encoding", "gzip")
	rr := httptest.NewRecorder()
	handler.HandleReport(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", rr.Code, rr.Body.String())
	}
	if sink.reportCount != 1 || sink.lastReport.InstanceID != "inst-1" {
		t.Fatalf("unexpected saved report: %#v count=%d", sink.lastReport, sink.reportCount)
	}
}
func TestHandleReportRejectsTooManyMetricItems(t *testing.T) {
	sink := &memorySink{}
	handler := NewHandler("expected-token", sink)
	payload := AgentReportRequest{
		InstanceID:        "inst-1",
		AgentID:           "agent-1",
		AggregatedMetrics: make([]AggregatedMetricPayload, 10001),
	}
	data, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, "/api/agent/report", bytes.NewReader(data))
	req.Header.Set("Authorization", "Bearer expected-token")
	rr := httptest.NewRecorder()
	handler.HandleReport(rr, req)
	if rr.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("expected 413, got %d body=%s", rr.Code, rr.Body.String())
	}
	if sink.reportCount != 0 {
		t.Fatal("oversized report should not be saved")
	}
}

func TestHandleReportRejectsTooManyNginxTimingItems(t *testing.T) {
	sink := &memorySink{}; handler := NewHandler("expected-token", sink)
	payload := AgentReportRequest{InstanceID:"inst-1", AgentID:"agent-1", NginxTimingBuckets:make([]NginxTimingBucketPayload,1501)}
	data,_:=json.Marshal(payload); req:=httptest.NewRequest(http.MethodPost,"/api/agent/report",bytes.NewReader(data));req.Header.Set("Authorization","Bearer expected-token");rr:=httptest.NewRecorder();handler.HandleReport(rr,req)
	if rr.Code!=http.StatusRequestEntityTooLarge||sink.reportCount!=0{t.Fatalf("status=%d saved=%d",rr.Code,sink.reportCount)}
}

func TestHandleReportRejectsLargeDecodedGzipPayload(t *testing.T) {
	sink := &memorySink{}
	handler := NewHandler("expected-token", sink)
	var body bytes.Buffer
	writer := gzip.NewWriter(&body)
	largeJSON := []byte(`{"instance_id":"inst-1","agent_id":"agent-1","padding":"`)
	if _, err := writer.Write(largeJSON); err != nil {
		t.Fatalf("write gzip prefix: %v", err)
	}
	if _, err := writer.Write(bytes.Repeat([]byte("x"), maxAgentDecodedBytes)); err != nil {
		t.Fatalf("write gzip padding: %v", err)
	}
	if _, err := writer.Write([]byte(`"}`)); err != nil {
		t.Fatalf("write gzip suffix: %v", err)
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("close gzip payload: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, "/api/agent/report", &body)
	req.Header.Set("Authorization", "Bearer expected-token")
	req.Header.Set("Content-Encoding", "gzip")
	rr := httptest.NewRecorder()
	handler.HandleReport(rr, req)
	if rr.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("expected 413, got %d body=%s", rr.Code, rr.Body.String())
	}
}
func TestHandleHeartbeatAcceptsValidPayload(t *testing.T) {
	sink := &memorySink{}
	handler := NewHandler("expected-token", sink)
	payload := AgentHeartbeatRequest{
		InstanceID:   "inst-1",
		AgentID:      "agent-1",
		AgentVersion: "0.1.0",
		ReportedAt:   time.Date(2026, 7, 2, 12, 0, 0, 0, time.UTC),
		Sequence:     12,
		LastLogID:    99,
	}
	data, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/api/agent/heartbeat", bytes.NewReader(data))
	req.Header.Set("Authorization", "Bearer expected-token")
	rr := httptest.NewRecorder()
	handler.HandleHeartbeat(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", rr.Code, rr.Body.String())
	}
	if sink.heartbeatCount != 1 {
		t.Fatalf("expected saved heartbeat")
	}
	if sink.lastHeartbeat.LastLogID != 99 {
		t.Fatalf("unexpected heartbeat: %#v", sink.lastHeartbeat)
	}
}
