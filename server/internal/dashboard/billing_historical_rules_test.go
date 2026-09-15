package dashboard

import (
	"archive/zip"
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"controltower/server/internal/billing"
)

func TestHistoricalRulesDailyPreviewAndExport(t *testing.T) {
	root := t.TempDir()
	day := time.Date(2026, 9, 16, 0, 0, 0, 0, billing.BusinessLocation)
	job := billing.Job{ID: "historic", JobType: "user_statement", UsageVersion: billing.HistoricalPriceUsageVersion, PricingSource: billing.PricingSourceNewAPI}
	file := billing.UserDailyFile{BillDay: day, RelativePath: "day.xlsx"}
	rows := []billing.RequestDetail{
		{ModelName: "m", ChannelID: 1, Charge: billing.LogCharge{Mode: "newapi", InputPrice: "20", OutputPrice: "100", CacheReadPrice: "2", Total: "3", PricingRule: "输入20；输出100；缓存2"}},
		{ModelName: "m", ChannelID: 2, Charge: billing.LogCharge{Mode: "newapi", InputPrice: "10", OutputPrice: "50", CacheReadPrice: "1", Total: "4", PricingRule: "输入10；输出50；缓存1"}},
		{ModelName: "expr", ChannelID: 1, Charge: billing.LogCharge{Mode: "newapi", InputPrice: "见计价规则", OutputPrice: "见计价规则", Total: "5", PricingRule: "表达式：p > 1000 ? p*20 : p*10\n命中档位：small"}},
	}
	var book bytes.Buffer
	if err := billing.WriteUserDailyWorkbook(&book, job, file, rows); err != nil {
		t.Fatal(err)
	}
	// Corrupt detail XML: the compact rule sheet must be sufficient on its own.
	z, err := zip.NewReader(bytes.NewReader(book.Bytes()), int64(book.Len()))
	if err != nil {
		t.Fatal(err)
	}
	out, err := os.Create(filepath.Join(root, file.RelativePath))
	if err != nil {
		t.Fatal(err)
	}
	w := zip.NewWriter(out)
	for _, entry := range z.File {
		dst, e := w.Create(entry.Name)
		if e != nil {
			t.Fatal(e)
		}
		if entry.Name == "xl/worksheets/sheet1.xml" {
			io.WriteString(dst, "invalid detail XML")
			continue
		}
		src, e := entry.Open()
		if e != nil {
			t.Fatal(e)
		}
		_, e = io.Copy(dst, src)
		src.Close()
		if e != nil {
			t.Fatal(e)
		}
	}
	if err = w.Close(); err != nil {
		t.Fatal(err)
	}
	out.Close()
	store := statementPriceTestStore{files: []billing.UserDailyFile{file}}
	prices, err := loadStatementPrices(context.Background(), job, store, root)
	if err != nil {
		t.Fatal(err)
	}
	agg := billing.StatementAggregateRow{AggregateRow: billing.AggregateRow{ModelName: "m", Day: day, Amount: "7"}}
	price := prices.price(job, agg)
	if price.Input != "10.000000 / 20.000000" || price.Output != "50.000000 / 100.000000" {
		t.Fatalf("%+v", price)
	}
	rules := prices.rules(job, agg)
	if !strings.Contains(rules, "当日 2 套") || !strings.Contains(rules, "输入20；输出100；缓存2") || !strings.Contains(rules, "输入10；输出50；缓存1") {
		t.Fatal(rules)
	}
	daily := statementDailySummary(job, []billing.StatementAggregateRow{agg}, nil, prices)[0]
	if daily["price_rules"] != rules || daily["final_amount"] != "7.00000000" {
		t.Fatalf("%+v", daily)
	}
	job.JobType = "upstream_statement"
	agg.ChannelID = 1
	channelPrices, err := loadStatementPrices(context.Background(), job, store, root)
	if err != nil {
		t.Fatal(err)
	}
	if channelPrices.price(job, agg).Input != "20.000000" {
		t.Fatal("channel boundaries lost")
	}
	job.JobType = "user_statement"
	data, err := statementWorkbook(job, []billing.StatementAggregateRow{agg}, store, root)
	if err != nil {
		t.Fatal(err)
	}
	result, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range result.File {
		if entry.Name == "xl/worksheets/sheet2.xml" {
			r, e := entry.Open()
			if e != nil {
				t.Fatal(e)
			}
			raw, e := io.ReadAll(r)
			r.Close()
			if e != nil {
				t.Fatal(e)
			}
			for _, want := range []string{"输入单价", "计价规则", "当日 2 套", "10.000000 / 20.000000"} {
				if !strings.Contains(string(raw), want) {
					t.Fatalf("missing %s", want)
				}
			}
		}
	}
	agg.ModelName = "expr"
	if !strings.Contains(prices.rules(job, agg), "p > 1000 ? p*20 : p*10") {
		t.Fatal("expression lost")
	}
}
