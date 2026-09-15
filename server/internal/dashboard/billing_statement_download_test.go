package dashboard

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"io"
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

func (s statementDownloadStore) ListBillingStatementDiscounts(context.Context, string) ([]billing.StatementDiscount, error) {
	return nil, nil
}

func TestUpstreamDailyDownloadsIncludeEveryUser(t *testing.T) {
	root := t.TempDir()
	day := time.Date(2026, 8, 28, 0, 0, 0, 0, billing.BusinessLocation)
	files := []billing.UserDailyFile{
		{BillDay: day.AddDate(0, 0, 1), UserID: 7, RelativePath: "next.xlsx"},
		{BillDay: day, UserID: 7, RelativePath: "first.xlsx"},
		{BillDay: day.UTC(), UserID: 9, RelativePath: "second.xlsx"},
	}
	for _, f := range files {
		if err := os.WriteFile(filepath.Join(root, f.RelativePath), []byte(f.RelativePath), 0600); err != nil {
			t.Fatal(err)
		}
	}
	store := statementDownloadStore{job: billing.Job{ID: "statement", JobType: "upstream_statement", Status: "complete", UpstreamName: "东方"}, statementPriceTestStore: statementPriceTestStore{files: files}}
	handler := BillingStatementResultHandler{Store: store, Root: root}
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, httptest.NewRequest("GET", "/?id=statement&defer_prices=1", nil))
	var preview struct {
		Files []struct {
			Day      string
			Filename string
		} `json:"daily_files"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &preview); err != nil {
		t.Fatal(err)
	}
	if w.Code != 200 || len(preview.Files) != 2 || preview.Files[0].Day != "2026-08-28" || preview.Files[0].Filename != "东方-2026-08-28-明细.zip" || preview.Files[1].Filename != "东方-2026-08-29-明细.xlsx" {
		t.Fatalf("preview: %d %s", w.Code, w.Body.String())
	}
	for _, tc := range []struct {
		query string
		count int
	}{{"export=daily&day=2026-08-28", 2}, {"download=1", 3}} {
		w = httptest.NewRecorder()
		handler.ServeHTTP(w, httptest.NewRequest("GET", "/?id=statement&"+tc.query, nil))
		if w.Code != 200 {
			t.Fatalf("download: %d %s", w.Code, w.Body.String())
		}
		z, err := zip.NewReader(bytes.NewReader(w.Body.Bytes()), int64(w.Body.Len()))
		if err != nil {
			t.Fatal(err)
		}
		seen := map[string]bool{}
		contents := map[string]bool{}
		for _, f := range z.File {
			if seen[f.Name] {
				t.Fatalf("duplicate entry %s", f.Name)
			}
			seen[f.Name] = true
			if f.Name == "账单.xlsx" {
				continue
			}
			in, err := f.Open()
			if err != nil {
				t.Fatal(err)
			}
			data, err := io.ReadAll(in)
			in.Close()
			if err != nil {
				t.Fatal(err)
			}
			contents[string(data)] = true
			if !strings.Contains(f.Name, "用户-") {
				t.Fatalf("ambiguous member %s", f.Name)
			}
		}
		if len(contents) != tc.count || !contents["first.xlsx"] || !contents["second.xlsx"] {
			t.Fatalf("missing contents: %v", contents)
		}
		if tc.count == 2 && contents["next.xlsx"] {
			t.Fatal("other day included")
		}
	}
	if err := os.Remove(filepath.Join(root, "second.xlsx")); err != nil {
		t.Fatal(err)
	}
	w = httptest.NewRecorder()
	handler.ServeHTTP(w, httptest.NewRequest("GET", "/?id=statement&export=daily&day=2026-08-28", nil))
	if w.Code != 404 || strings.Contains(w.Header().Get("Content-Type"), "zip") {
		t.Fatalf("partial success: %d %s", w.Code, w.Body.String())
	}
}
