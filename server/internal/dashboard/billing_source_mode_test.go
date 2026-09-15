package dashboard

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/xml"
	"io"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"controltower/server/internal/billing"
)

func TestStatementSourceSelectionDefaultAndDedup(t *testing.T) {
	keys := map[string]string{}
	for _, extra := range []string{"", `,"recalculate":false`, `,"recalculate":true`} {
		store := &statementStoreStub{}
		w := httptest.NewRecorder()
		body := `{"instance_id":"site","statement_type":"user","user_id":7,"from":"2026-09-01","to":"2026-09-02"` + extra + `}`
		BillingStatementsHandler{Store: store}.ServeHTTP(w, httptest.NewRequest("POST", "/", strings.NewReader(body)))
		want := billing.PricingSourceNewAPI
		if strings.HasSuffix(extra, "true") {
			want = billing.PricingSourceRecalculate
		}
		if w.Code != 202 || store.job.PricingSource != want || store.job.UsageVersion != billing.HistoricalPriceUsageVersion {
			t.Fatalf("body=%s code=%d job=%+v", body, w.Code, store.job)
		}
		if key := keys[want]; key != "" && key != store.job.RequestKey {
			t.Fatal("omitted and false must deduplicate")
		}
		keys[want] = store.job.RequestKey
	}
	if keys[billing.PricingSourceNewAPI] == keys[billing.PricingSourceRecalculate] {
		t.Fatal("modes share request key")
	}
}

type mediaResultStore struct {
	statementPriceTestStore
	tokens []billing.TokenDailyRow
}

func (s mediaResultStore) QueryBillingTokenRows(context.Context, string, int64, int64, time.Time, time.Time) ([]billing.TokenDailyRow, error) {
	return s.tokens, nil
}

func TestSourceStatementMediaPreviewAndWorkbook(t *testing.T) {
	day := time.Date(2026, 9, 14, 0, 0, 0, 0, billing.BusinessLocation)
	job := billing.Job{ID: "bill", UserID: 7, JobType: "user_statement", PricingSource: billing.PricingSourceNewAPI, UsageVersion: 1, From: day, To: day.AddDate(0, 0, 1)}
	u := billing.MultimediaUsage{ImageInputTokens: 70, ImageOutputTokens: 20, AudioInputTokens: 30, AudioOutputTokens: 10}
	rows := []billing.StatementAggregateRow{{AggregateRow: billing.AggregateRow{MultimediaUsage: u, ModelName: "m", Day: day, RequestCount: 1, PromptTokens: 90, CompletionTokens: 60, Amount: "0.012345000000"}, ChannelID: 1}, {AggregateRow: billing.AggregateRow{MultimediaUsage: u, ModelName: "m", Day: day, RequestCount: 1, PromptTokens: 90, CompletionTokens: 60, Amount: "0.012345000000"}, ChannelID: 2}}
	store := mediaResultStore{tokens: []billing.TokenDailyRow{{MultimediaUsage: u, TokenID: 8, TokenName: "key", ModelName: "m", Day: day, RequestCount: 1, PromptTokens: 90, CompletionTokens: 60, Amount: "0.012345000000"}, {MultimediaUsage: u, TokenID: 8, TokenName: "key", ModelName: "m", Day: day, RequestCount: 1, PromptTokens: 90, CompletionTokens: 60, Amount: "0.012345000000"}}}
	preview, err := statementPreview(context.Background(), job, rows, store, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if len(preview.Models) != 1 || preview.Models[0]["image_input_tokens"] != int64(140) || preview.Models[0]["audio_output_tokens"] != int64(20) || preview.Daily[0]["input_price"] != "未拆分" || preview.Models[0]["amount"] != "0.02469000" || preview.Tokens[0]["audio_input_tokens"] != int64(30) {
		t.Fatalf("preview=%+v", preview)
	}
	book, err := statementWorkbook(job, rows, store, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	z, err := zip.NewReader(bytes.NewReader(book), int64(len(book)))
	if err != nil {
		t.Fatal(err)
	}
	sheets := 0
	for _, file := range z.File {
		if !strings.HasPrefix(file.Name, "xl/worksheets/sheet") {
			continue
		}
		r, e := file.Open()
		if e != nil {
			t.Fatal(e)
		}
		raw, e := io.ReadAll(r)
		r.Close()
		if e != nil {
			t.Fatal(e)
		}
		var sheet struct {
			Rows []struct {
				Cells []struct {
					Ref    string `xml:"r,attr"`
					Value  string `xml:"v"`
					Inline string `xml:"is>t"`
				} `xml:"c"`
			} `xml:"sheetData>row"`
		}
		if e = xml.Unmarshal(raw, &sheet); e != nil {
			t.Fatal(e)
		}
		column := ""
		seen := false
		for _, row := range sheet.Rows {
			for _, cell := range row.Cells {
				if cell.Inline == "图像输入 Token" {
					column = strings.TrimRight(cell.Ref, "0123456789")
					seen = true
				}
				if column != "" && strings.TrimRight(cell.Ref, "0123456789") == column && cell.Value != "" {
					if cell.Value != "70" && cell.Value != "140" && cell.Value != "140.00000000" {
						t.Fatalf("%s image value=%s", file.Name, cell.Value)
					}
				}
			}
		}
		if !seen || !strings.Contains(string(raw), "音频输出 Token") || !strings.Contains(string(raw), "普通输入 Token") {
			t.Fatalf("%s missing usage columns", file.Name)
		}
		sheets++
	}
	if sheets != 4 {
		t.Fatalf("sheets=%d", sheets)
	}
}

func TestBillingAudioSeparatePriceUsageKey(t *testing.T) {
	usage := parseBillingCacheUsage(`{"audio_input_token_count":321,"audio_input_seperate_price":true}`)
	if usage.AudioInput != 321 {
		t.Fatalf("usage=%+v", usage)
	}
}
