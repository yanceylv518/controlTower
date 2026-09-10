package dashboard

import (
	"context"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"controltower/server/internal/billing"
)

type statementDownloadStore struct {
	statementPriceTestStore
	job billing.Job
}

func (s statementDownloadStore) BillingJob(context.Context, string) (billing.Job, error) {
	return s.job, nil
}
func (s statementDownloadStore) QueryBillingStatementAggregates(context.Context, string) ([]billing.StatementAggregateRow, error) {
	return nil, nil
}

func TestStatementDailyDownload(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "day.xlsx"), []byte("saved statement detail"), 0600); err != nil {
		t.Fatal(err)
	}
	day := time.Date(2026, 9, 1, 0, 0, 0, 0, billing.BusinessLocation)
	for _, kind := range []string{"user_statement", "upstream_statement"} {
		for _, tc := range []struct {
			name, day, path, status string
			code                    int
		}{
			{"ok", "2026-09-01", "day.xlsx", "complete", 200},
			{"other day", "2026-09-02", "day.xlsx", "complete", 404},
			{"invalid date", "bad", "day.xlsx", "complete", 400},
			{"missing file", "2026-09-01", "missing.xlsx", "complete", 404},
			{"outside root", "2026-09-01", "../outside.xlsx", "complete", 500},
			{"unfinished", "2026-09-01", "day.xlsx", "running", 404},
		} {
			t.Run(kind+"/"+tc.name, func(t *testing.T) {
				store := statementDownloadStore{job: billing.Job{ID: "statement", JobType: kind, Status: tc.status}, statementPriceTestStore: statementPriceTestStore{files: []billing.UserDailyFile{{BillDay: day, RelativePath: tc.path}}}}
				w := httptest.NewRecorder()
				BillingStatementResultHandler{Store: store, Root: root}.ServeHTTP(w, httptest.NewRequest("GET", "/?id=statement&export=daily&day="+tc.day, nil))
				if w.Code != tc.code {
					t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
				}
				if tc.code == 200 && (w.Body.String() != "saved statement detail" || !strings.Contains(w.Header().Get("Content-Disposition"), "2026-09-01")) {
					t.Fatalf("wrong download: %s", w.Body.String())
				}
			})
		}
	}
}

func TestStatementSummaryDownload(t *testing.T) {
	store := statementDownloadStore{job: billing.Job{ID: "statement", JobType: "user_statement", Status: "complete"}}
	w := httptest.NewRecorder()
	BillingStatementResultHandler{Store: store, Root: t.TempDir()}.ServeHTTP(w, httptest.NewRequest("GET", "/?id=statement&download=1&export=summary", nil))
	if w.Code != 200 || !strings.Contains(w.Header().Get("Content-Type"), "spreadsheetml.sheet") || !strings.HasPrefix(w.Body.String(), "PK") {
		t.Fatalf("invalid summary: %d %s", w.Code, w.Body.String())
	}
}
