package dashboard

import (
	"context"
	"encoding/json"
	"errors"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"controltower/server/internal/billing"
)

type statementLoadingStore struct {
	statementPriceTestStore
	job       billing.Job
	rows      []billing.StatementAggregateRow
	fileCalls int
	fileErr   error
}

func (s *statementLoadingStore) BillingJob(context.Context, string) (billing.Job, error) {
	return s.job, nil
}
func (s *statementLoadingStore) QueryBillingStatementAggregates(context.Context, string) ([]billing.StatementAggregateRow, error) {
	return s.rows, nil
}
func (s *statementLoadingStore) ListBillingStatementUserFiles(context.Context, string) ([]billing.UserDailyFile, error) {
	s.fileCalls++
	return s.files, s.fileErr
}
func (s *statementLoadingStore) ListBillingStatementDiscounts(context.Context, string) ([]billing.StatementDiscount, error) {
	return nil, nil
}

func TestStatementPreviewDefersOnlyPrices(t *testing.T) {
	for _, kind := range []string{"user_statement", "upstream_statement"} {
		t.Run(kind, func(t *testing.T) {
			day := time.Date(2026, 9, 1, 0, 0, 0, 0, billing.BusinessLocation)
			s := &statementLoadingStore{
				job:     billing.Job{ID: "preview", JobType: kind, Status: "complete", UserID: 1},
				rows:    []billing.StatementAggregateRow{{AggregateRow: billing.AggregateRow{Day: day, ModelName: "m", RequestCount: 12, Amount: "7.5"}}},
				fileErr: errors.New("detail storage unavailable"),
			}
			h := BillingStatementResultHandler{Store: s, Root: t.TempDir()}
			w := httptest.NewRecorder()
			h.ServeHTTP(w, httptest.NewRequest("GET", "/?id=preview&defer_prices=1", nil))
			if w.Code != 200 || s.fileCalls != 0 {
				t.Fatalf("preview accessed detail files: status=%d calls=%d body=%s", w.Code, s.fileCalls, w.Body)
			}
			var result struct {
				Billable int              `json:"billable_orders"`
				Daily    []map[string]any `json:"daily_summary"`
			}
			if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
				t.Fatal(err)
			}
			if result.Billable != 12 || len(result.Daily) != 1 || result.Daily[0]["amount"] != "7.50000000" || result.Daily[0]["input_price"] != "待加载" {
				t.Fatalf("wrong deferred preview: %+v", result)
			}
			for _, query := range []string{"", "&section=prices"} {
				w = httptest.NewRecorder()
				h.ServeHTTP(w, httptest.NewRequest("GET", "/?id=preview"+query, nil))
				if w.Code != 500 {
					t.Fatalf("price load should report unavailable storage: %d", w.Code)
				}
			}
		})
	}
}

func clearStatementMemoryCache() {
	statementPriceCache.Lock()
	clear(statementPriceCache.items)
	statementPriceCache.Unlock()
}

func TestStatementPricesPersistAndInvalidate(t *testing.T) {
	root := t.TempDir()
	day := time.Date(2026, 9, 1, 0, 0, 0, 0, billing.BusinessLocation)
	job := billing.Job{ID: "persist", JobType: "user_statement", UserID: 1}
	file := writeStatementDetailFile(t, root, "details.xlsx", job, day, []billing.RequestDetail{{ModelName: "m", PromptTokens: 1, Charge: billing.LogCharge{InputPrice: "2"}}})
	store := statementPriceTestStore{files: []billing.UserDailyFile{file}}
	first, err := loadStatementPrices(context.Background(), job, store, root)
	if err != nil {
		t.Fatal(err)
	}
	cachePath := statementPriceSnapshotPath(root, job.ID, job.JobType)
	if _, err := os.Stat(cachePath); err != nil {
		t.Fatalf("no persistent snapshot: %v", err)
	}
	// Simulate restart. Identical file fingerprints must use the persisted result,
	// even when parsing the workbook would now fail.
	path := filepath.Join(root, file.RelativePath)
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	original, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, make([]byte, len(original)), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(path, info.ModTime(), info.ModTime()); err != nil {
		t.Fatal(err)
	}
	clearStatementMemoryCache()
	second, err := loadStatementPrices(context.Background(), job, store, root)
	if err != nil || !reflect.DeepEqual(first, second) {
		t.Fatalf("disk cache not reused: %v", err)
	}
	// A changed fingerprint must invalidate both memory and disk caches.
	if err := os.Chtimes(path, info.ModTime(), info.ModTime().Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	if _, err := loadStatementPrices(context.Background(), job, store, root); err == nil {
		t.Fatal("changed corrupt workbook was hidden by stale cache")
	}
	if err := os.WriteFile(path, original, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(cachePath, []byte("corrupt cache"), 0o600); err != nil {
		t.Fatal(err)
	}
	clearStatementMemoryCache()
	third, err := loadStatementPrices(context.Background(), job, store, root)
	if err != nil || !reflect.DeepEqual(first, third) {
		t.Fatalf("corrupt cache did not rebuild: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := loadStatementPrices(ctx, job, store, root); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation ignored: %v", err)
	}
}

func TestStatementPricesSectionMatchesFullPreview(t *testing.T) {
	for _, kind := range []string{"user_statement", "upstream_statement"} {
		t.Run(kind, func(t *testing.T) {
			root := t.TempDir()
			day := time.Date(2026, 9, 1, 0, 0, 0, 0, billing.BusinessLocation)
			job := billing.Job{ID: "section", JobType: kind, Status: "complete", UserID: 1}
			file := writeStatementDetailFile(t, root, "d.xlsx", job, day, []billing.RequestDetail{{ModelName: "m", ChannelID: 7, PromptTokens: 10, Charge: billing.LogCharge{InputPrice: "3"}}})
			s := &statementLoadingStore{statementPriceTestStore: statementPriceTestStore{files: []billing.UserDailyFile{file}}, job: job,
				rows: []billing.StatementAggregateRow{{ChannelID: 7, AggregateRow: billing.AggregateRow{Day: day, ModelName: "m", RequestCount: 1, Amount: "3"}}}}
			h := BillingStatementResultHandler{Store: s, Root: root}
			var summaries []any
			for _, query := range []string{"", "&section=prices"} {
				w := httptest.NewRecorder()
				h.ServeHTTP(w, httptest.NewRequest("GET", "/?id=section"+query, nil))
				if w.Code != 200 {
					t.Fatalf("status=%d body=%s", w.Code, w.Body)
				}
				var body map[string]any
				if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
					t.Fatal(err)
				}
				summaries = append(summaries, body["daily_summary"])
			}
			if !reflect.DeepEqual(summaries[0], summaries[1]) {
				t.Fatalf("lazy price section differs: %+v", summaries)
			}
		})
	}
}

func BenchmarkStatementPriceLoad(b *testing.B) {
	root := b.TempDir()
	day := time.Date(2026, 9, 1, 0, 0, 0, 0, billing.BusinessLocation)
	job := billing.Job{ID: "bench", JobType: "user_statement", UserID: 1}
	file := billing.UserDailyFile{BillDay: day, RelativePath: "d.xlsx"}
	rows := make([]billing.RequestDetail, 10000)
	for i := range rows {
		rows[i] = billing.RequestDetail{ModelName: "m", PromptTokens: 10, Charge: billing.LogCharge{InputPrice: "3"}}
	}
	f, err := os.Create(filepath.Join(root, file.RelativePath))
	if err != nil {
		b.Fatal(err)
	}
	err = billing.WriteUserDailyWorkbook(f, job, file, rows)
	closeErr := f.Close()
	if err != nil || closeErr != nil {
		b.Fatalf("write workbook: %v %v", err, closeErr)
	}
	store := statementPriceTestStore{files: []billing.UserDailyFile{file}}
	if _, err := loadStatementPrices(context.Background(), job, store, root); err != nil {
		b.Fatal(err)
	}
	b.Run("parse_10000_orders", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			if _, _, err := readStatementFilePrices(context.Background(), filepath.Join(root, file.RelativePath)); err != nil {
				b.Fatal(err)
			}
		}
	})
	b.Run("disk_cache_after_restart", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			clearStatementMemoryCache()
			if _, err := loadStatementPrices(context.Background(), job, store, root); err != nil {
				b.Fatal(err)
			}
		}
	})
}
