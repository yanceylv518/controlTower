package dashboard

import (
	"controltower/server/internal/billing"
	"encoding/json"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestBillingDetailsHandlerSavedFilesOnly(t *testing.T) {
	root := t.TempDir()
	day := time.Date(2026, 9, 11, 0, 0, 0, 0, billing.BusinessLocation)
	job := billing.Job{ID: "daily", UserID: 7, JobType: "user_statement", UsageVersion: 3, BillPeriod: "daily", Status: "complete", From: day, To: day.AddDate(0, 0, 1)}
	store := statementDownloadStore{job: job, statementPriceTestStore: statementPriceTestStore{files: []billing.UserDailyFile{{BillDay: day, UserID: 7, RelativePath: "saved.xlsx"}}}}
	file, err := os.Create(filepath.Join(root, "saved.xlsx"))
	if err != nil {
		t.Fatal(err)
	}
	err = billing.WriteUserDailyWorkbook(file, job, billing.UserDailyFile{}, []billing.RequestDetail{{CreatedUnix: day.Unix(), RequestID: "saved-order", ModelName: "model", TokenName: "token", Charge: billing.LogCharge{Total: "0.5"}}})
	file.Close()
	if err != nil {
		t.Fatal(err)
	}
	h := &BillingDetailsHandler{Store: store, Root: root}
	call := func(method, url, body string) *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest(method, url, strings.NewReader(body)))
		return w
	}
	initial := call("GET", "/?id=daily", "")
	if initial.Code != 202 {
		t.Fatal(initial.Code, initial.Body.String())
	}
	deadline := time.Now().Add(5 * time.Second)
	var page *httptest.ResponseRecorder
	for time.Now().Before(deadline) {
		page = call("GET", "/?id=daily", "")
		if page.Code == 200 && strings.Contains(page.Body.String(), "saved-order") {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if !strings.Contains(page.Body.String(), "saved-order") {
		t.Fatal(page.Body.String())
	}
	// Removing the original file proves later operations use the persisted shards.
	if err = os.Remove(filepath.Join(root, "saved.xlsx")); err != nil {
		t.Fatal(err)
	}
	page = call("GET", "/?id=daily", "")
	if page.Code != 200 || !strings.Contains(page.Body.String(), "saved-order") {
		t.Fatal(page.Code, page.Body.String())
	}
	bad := call("GET", "/?id=daily&from=2020-01-01", "")
	if bad.Code != 400 {
		t.Fatal(bad.Code)
	}
	exported := call("POST", "/?id=daily&action=export", `{"model":"model"}`)
	if exported.Code != 202 {
		t.Fatal(exported.Code, exported.Body.String())
	}
	var task detailTask
	if err = json.Unmarshal(exported.Body.Bytes(), &task); err != nil {
		t.Fatal(err)
	}
	for time.Now().Before(deadline) {
		state := call("GET", "/?id=daily&action=status&key="+task.Key, "")
		json.Unmarshal(state.Body.Bytes(), &task)
		if task.Status != "running" {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if task.Status != "complete" || task.Matched != 1 {
		t.Fatal(task)
	}
	download := call("GET", "/?id=daily&action=download&key="+task.Key, "")
	if download.Code != 200 || !strings.HasPrefix(download.Body.String(), "PK") {
		t.Fatal(download.Code)
	}
	h.Store = statementDownloadStore{job: billing.Job{ID: "other", UserID: 7, JobType: "user_statement", UsageVersion: 3, BillPeriod: "daily", Status: "complete", From: day, To: day.AddDate(0, 0, 1)}, statementPriceTestStore: store.statementPriceTestStore}
	if denied := call("GET", "/?id=other&action=download&key="+task.Key, ""); denied.Code != 404 {
		t.Fatal("cross-job export access", denied.Code)
	}
}
