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

type statementPriceTestStore struct {
	BillingStatementResultStore
	files []billing.UserDailyFile
}

func (s statementPriceTestStore) ListBillingStatementUserFiles(context.Context, string) ([]billing.UserDailyFile, error) {
	return s.files, nil
}
func (s statementPriceTestStore) QueryBillingTokenRows(context.Context, string, int64, int64, time.Time, time.Time) ([]billing.TokenDailyRow, error) {
	return nil, nil
}
func (s statementPriceTestStore) QueryBillingAnomalies(context.Context, string, string, int64, int64, time.Time, time.Time, time.Time, int64, int) ([]billing.AnomalyOrder, error) {
	return nil, nil
}
func (s statementPriceTestStore) QueryBillingStatementReconciliation(context.Context, string, int, int) ([]billing.ReconciliationOrder, error) {
	return nil, nil
}

func TestStatementPricesUseImmutableDetails(t *testing.T) {
	root := t.TempDir()
	day := time.Date(2026, 8, 2, 0, 0, 0, 0, billing.BusinessLocation)
	job := billing.Job{ID: "test", JobType: "user_statement", UserID: 28}
	file := billing.UserDailyFile{BillDay: day, RelativePath: "details.xlsx"}
	f, err := os.Create(filepath.Join(root, file.RelativePath))
	if err != nil {
		t.Fatal(err)
	}
	details := []billing.RequestDetail{
		{ModelName: "model", ChannelID: 1, ChannelName: "channel", Charge: billing.LogCharge{InputPrice: "1", OutputPrice: "2", CacheReadPrice: "0.02", Total: "3"}},
		{ModelName: "model", ChannelID: 2, Charge: billing.LogCharge{InputPrice: "4", OutputPrice: "8", CacheReadPrice: "0.04", Total: "6"}},
	}
	if err := billing.WriteUserDailyWorkbook(f, job, file, details); err != nil {
		t.Fatal(err)
	}
	f.Close()
	store := statementPriceTestStore{files: []billing.UserDailyFile{file}}
	prices, err := loadStatementPrices(context.Background(), job, store, root)
	if err != nil {
		t.Fatal(err)
	}
	row := billing.StatementAggregateRow{AggregateRow: billing.AggregateRow{Day: day, ModelName: "model", Amount: "9", RequestCount: 2}}
	price := prices.price(job, row)
	if price.Input != "1.000000 / 4.000000" || price.Output != "2.000000 / 8.000000" {
		t.Fatalf("wrong actual price combinations: %+v", price)
	}
	upstream := job
	upstream.JobType = "upstream_statement"
	upstreamPrices, err := loadStatementPrices(context.Background(), upstream, store, root)
	if err != nil {
		t.Fatal(err)
	}
	row.ChannelID = 1
	if got := upstreamPrices.price(upstream, row).Input; got != "1.000000" {
		t.Fatalf("mixed channels: %s", got)
	}
	preview, err := statementPreview(context.Background(), job, []billing.StatementAggregateRow{row}, store, root)
	if err != nil {
		t.Fatal(err)
	}
	if preview.Daily[0]["input_price"] != price.Input || preview.Daily[0]["amount"] != "9.00000000" {
		t.Fatalf("wrong preview: %+v", preview.Daily)
	}
	book, err := statementWorkbook(job, []billing.StatementAggregateRow{row}, store, root)
	if err != nil {
		t.Fatal(err)
	}
	z, err := zip.NewReader(bytes.NewReader(book), int64(len(book)))
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range z.File {
		if f.Name != "xl/worksheets/sheet2.xml" {
			continue
		}
		in, _ := f.Open()
		data, _ := io.ReadAll(in)
		in.Close()
		if !strings.Contains(string(data), "1.000000 / 4.000000") {
			t.Fatalf("missing prices: %s", data)
		}
		if !strings.Contains(string(data), `t="inlineStr"`) {
			t.Fatal("multi-price values must be text")
		}
	}
}

func TestStatementMissingPricesAreExplicit(t *testing.T) {
	prices, err := loadStatementPrices(context.Background(), billing.Job{}, statementPriceTestStore{files: []billing.UserDailyFile{{RelativePath: "missing.xlsx"}}}, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if got := prices.price(billing.Job{}, billing.StatementAggregateRow{}).Input; got != "明细价格不可用" {
		t.Fatalf("silent missing price: %s", got)
	}
	if statementPriceCell("明细价格不可用").Number {
		t.Fatal("unavailable prices must not be numeric")
	}
	if !statementPriceCell("1.000000").Number {
		t.Fatal("single prices must remain numeric")
	}
}
