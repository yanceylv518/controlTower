package dashboard

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
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

type countingTokenCSVStub struct{ calls atomic.Int32 }

func (s *countingTokenCSVStub) ServeHTTP(w http.ResponseWriter, _ *http.Request) {
	s.calls.Add(1)
	_, _ = w.Write([]byte("cached"))
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

func TestTokenDetailExportJobReusesMatchingFile(t *testing.T) {
	stub := &countingTokenCSVStub{}
	h := BillingExportJobHandler{Workbook: stub, Kind: "token"}
	body := `{"instance_id":"cache-site","user_id":"17","token_id":"13","from":"2026-08-03 00:00:00","to":"2026-08-04 00:00:00","job_id":"bill-v1"}`

	create := func() billingExportTask {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/api/dashboard/billing/token-detail-jobs", strings.NewReader(body)))
		var task billingExportTask
		if err := json.Unmarshal(w.Body.Bytes(), &task); err != nil || task.ID == "" {
			t.Fatalf("create=%d %s err=%v", w.Code, w.Body.String(), err)
		}
		return task
	}

	task := create()
	t.Cleanup(func() {
		_ = os.Remove(billingExportPath(task.ID, "token"))
		billingExports.Lock()
		delete(billingExports.tasks, task.ID)
		billingExports.Unlock()
	})
	deadline := time.Now().Add(time.Second)
	for task.Status != "complete" && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
		task = create()
	}
	if task.Status != "complete" || stub.calls.Load() != 1 {
		t.Fatalf("first task=%#v calls=%d", task, stub.calls.Load())
	}

	if reused := create(); reused.Status != "complete" || stub.calls.Load() != 1 {
		t.Fatalf("memory reuse=%#v calls=%d", reused, stub.calls.Load())
	}
	billingExports.Lock()
	delete(billingExports.tasks, task.ID)
	billingExports.Unlock()
	if recovered := create(); recovered.Status != "complete" || stub.calls.Load() != 1 {
		t.Fatalf("file reuse=%#v calls=%d", recovered, stub.calls.Load())
	}
}
