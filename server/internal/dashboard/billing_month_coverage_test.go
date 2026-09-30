package dashboard

import (
	"archive/zip"
	"bytes"
	"controltower/server/internal/billing"
	"encoding/json"
	"io"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestMonthlyCoveragePreviewAndExportAgree(t *testing.T) {
	job, rows, store := discountGroupFixture()
	for _, full := range []bool{false, true} {
		days := map[string]bool{"2026-09-01": false, "2026-09-02": false}
		if full {
			for day := job.From.AddDate(0, 0, 2); day.Before(job.To); day = day.AddDate(0, 0, 1) {
				days[day.Format("2006-01-02")] = true
			}
		}
		job.MonthlyCoverage = billing.NewMonthlyCoverage(job.From, job.To, days)
		for _, kind := range []string{"user_statement", "upstream_statement"} {
			job.JobType = kind
			for _, dimension := range []string{"month", "daily", "token"} {
				if kind == "upstream_statement" && dimension == "token" {
					continue
				}
				w := httptest.NewRecorder()
				BillingStatementResultHandler{Store: store}.writeMonthlyPreview(w, httptest.NewRequest("GET", "/?dimension="+dimension, nil), job, rows)
				var result struct {
					Coverage billing.MonthlyCoverage
					Totals   []string
				}
				if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil || w.Code != 200 || result.Coverage.Complete != full || result.Coverage.RangeLabel() != job.MonthlyCoverage.RangeLabel() || result.Totals[len(result.Totals)-1] != "17.136000" {
					t.Fatal(w.Body.String(), err)
				}
			}
			book, err := settlementWorkbook(job, rows, store)
			if err != nil {
				t.Fatal(err)
			}
			z, err := zip.NewReader(bytes.NewReader(book), int64(len(book)))
			if err != nil {
				t.Fatal(err)
			}
			for _, file := range z.File {
				if !strings.HasPrefix(file.Name, "xl/worksheets/sheet") {
					continue
				}
				r, err := file.Open()
				if err != nil {
					t.Fatal(err)
				}
				data, err := io.ReadAll(r)
				r.Close()
				if err != nil || !strings.Contains(string(data), job.MonthlyCoverage.Description()) {
					t.Fatal(file.Name, "missing actual coverage", err)
				}
				if !full && strings.Contains(string(data), "2026-09-01 至 2026-09-30") {
					t.Fatal("partial bill labelled full month", file.Name)
				}
				if !full && kind == "user_statement" && (!strings.Contains(string(data), `r="4" ht="60"`) || !strings.Contains(string(data), `mergeCell ref="A4:`)) {
					t.Fatal("partial coverage header must have room to wrap", file.Name)
				}
			}
		}
	}
}
