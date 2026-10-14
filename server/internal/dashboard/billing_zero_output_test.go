package dashboard

import (
	"context"
	"controltower/server/internal/billing"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestZeroOutputTaskRequestAndSourcePredicate(t *testing.T) {
	for _, kind := range []string{"user", "upstream"} {
		store := &statementStoreStub{upstream: billing.Upstream{Channels: []billing.UpstreamChannel{{}}}}
		w := httptest.NewRecorder()
		body := `{"instance_id":"site","statement_type":"` + kind + `","user_id":7,"upstream_id":9,"new_edition":true,"period":"zero_output","exclude_zero_output":true,"from":"2025-09-01T00:00:00+08:00","to":"2025-09-02T00:00:00+08:00"}`
		BillingStatementsHandler{Store: store}.ServeHTTP(w, httptest.NewRequest("POST", "/", strings.NewReader(body)))
		if w.Code != 202 || store.job.BillPeriod != billing.ZeroOutputPeriod || store.job.ExcludeZeroOutput || store.job.UsageVersion != 3 {
			t.Fatal(w.Code, w.Body.String(), store.job)
		}
	}
	user, _ := billingLogsPageQuery(7, 0, 0)
	upstream, _ := billingChannelsModelsPageQuery([]int64{1}, map[int64][]string{1: {"m"}})
	for _, q := range []string{user, upstream} {
		if billingZeroOutputPageQuery(context.Background(), q) != q {
			t.Fatal("ordinary SQL changed")
		}
		filtered := billingZeroOutputPageQuery(billing.WithZeroOutputOnly(context.Background()), q)
		if strings.Count(filtered, "AND l.completion_tokens=0") != 1 || !strings.Contains(filtered, "AND l.completion_tokens=0 ORDER BY l.created_at,l.id LIMIT ?") {
			t.Fatal(filtered)
		}
	}
}

type zeroFileStore struct {
	BillingStatementResultStore
	job   billing.Job
	files []billing.UserDailyFile
}

func (s zeroFileStore) BillingJob(context.Context, string) (billing.Job, error) { return s.job, nil }
func (s zeroFileStore) ListBillingStatementUserFiles(context.Context, string) ([]billing.UserDailyFile, error) {
	return s.files, nil
}
func TestZeroOutputDownloadUsesPublishedFile(t *testing.T) {
	root := t.TempDir()
	data := []byte("published workbook")
	if err := os.WriteFile(filepath.Join(root, "file.xlsx"), data, 0600); err != nil {
		t.Fatal(err)
	}
	store := zeroFileStore{job: billing.Job{ID: "j", JobType: "user_statement", BillPeriod: billing.ZeroOutputPeriod, Status: "complete"}, files: []billing.UserDailyFile{{RelativePath: "file.xlsx", FileSize: int64(len(data))}}}
	for _, path := range []string{"file.xlsx", "../outside.xlsx", "missing.xlsx"} {
		store.files[0].RelativePath = path
		w := httptest.NewRecorder()
		BillingStatementResultHandler{Store: store, Root: root}.ServeHTTP(w, httptest.NewRequest("GET", "/?id=j&download=1", nil))
		if path == "file.xlsx" {
			if w.Code != 200 || w.Body.String() != string(data) || !strings.Contains(w.Header().Get("Content-Disposition"), ".xlsx") {
				t.Fatal(w.Code, w.Body.String())
			}
		} else if w.Code != 404 {
			t.Fatal(path, w.Code)
		}
	}
}
