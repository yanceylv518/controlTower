package dashboard

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

type tokenCSVStub struct{ fail bool }

func (s tokenCSVStub) ServeHTTP(w http.ResponseWriter, _ *http.Request) {
	if s.fail {
		writeDashboardError(w, 500, "source_failed")
		return
	}
	_, _ = w.Write([]byte("ok"))
}

func TestTokenDetailExportJobCompletesAndDownloads(t *testing.T) {
	h := BillingExportJobHandler{Workbook: tokenCSVStub{}, Kind: "token"}
	body := `{"instance_id":"site-a","user_id":"7","token_id":"3","from":"2026-08-01 00:00:00","to":"2026-08-02 00:00:00"}`
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("POST", "/api/dashboard/billing/token-detail-jobs", strings.NewReader(body)))
	var task billingExportTask
	if json.Unmarshal(w.Body.Bytes(), &task) != nil || task.ID == "" {
		t.Fatalf("create=%d %s", w.Code, w.Body.String())
	}
	deadline := time.Now().Add(time.Second)
	for task.Status != "complete" && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
		status := httptest.NewRecorder()
		h.ServeHTTP(status, httptest.NewRequest("GET", "/api/dashboard/billing/token-detail-jobs?id="+task.ID, nil))
		_ = json.Unmarshal(status.Body.Bytes(), &task)
	}
	if task.Status != "complete" {
		t.Fatalf("task=%#v", task)
	}
	download := httptest.NewRecorder()
	h.ServeHTTP(download, httptest.NewRequest("GET", "/api/dashboard/billing/token-detail-jobs?id="+task.ID+"&download=1", nil))
	if download.Code != 200 || download.Body.String() != "ok" || !strings.Contains(download.Header().Get("Content-Disposition"), ".csv") {
		t.Fatalf("download=%d %q %#v", download.Code, download.Body.String(), download.Header())
	}
}
