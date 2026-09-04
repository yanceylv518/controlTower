package dashboard

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"controltower/server/internal/billing"
)

func writeStatementDetailFile(t *testing.T, root, name string, job billing.Job, day time.Time, details []billing.RequestDetail) billing.UserDailyFile {
	t.Helper()
	file := billing.UserDailyFile{BillDay: day, RelativePath: name}
	f, err := os.Create(filepath.Join(root, name))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if err := billing.WriteUserDailyWorkbook(f, job, file, details); err != nil {
		t.Fatal(err)
	}
	return file
}

// A request without cache reads or output carries a zero unit price for that
// lane; the day's price list must not repeat the input price per tuple or show
// "0 / 0.1" for the cache lane.
func TestStatementPricesDedupePerColumn(t *testing.T) {
	root := t.TempDir()
	day := time.Date(2026, 9, 1, 0, 0, 0, 0, billing.BusinessLocation)
	job := billing.Job{ID: "dedupe", JobType: "user_statement", UserID: 1}
	file := writeStatementDetailFile(t, root, "d.xlsx", job, day, []billing.RequestDetail{
		{ModelName: "m", ChannelID: 1, Charge: billing.LogCharge{InputPrice: "1", OutputPrice: "2", CacheReadPrice: "0.1", Total: "3"}},
		{ModelName: "m", ChannelID: 1, Charge: billing.LogCharge{InputPrice: "1", OutputPrice: "2", Total: "3"}},
		{ModelName: "m", ChannelID: 1, Charge: billing.LogCharge{InputPrice: "1", Total: "3"}},
		{ModelName: "free", ChannelID: 1, Charge: billing.LogCharge{InputPrice: "0", OutputPrice: "0", Total: "0"}},
	})
	prices, err := loadStatementPrices(context.Background(), job, statementPriceTestStore{files: []billing.UserDailyFile{file}}, root)
	if err != nil {
		t.Fatal(err)
	}
	row := billing.StatementAggregateRow{AggregateRow: billing.AggregateRow{Day: day, ModelName: "m"}}
	if p := prices.price(job, row); p.Input != "1.000000" || p.Output != "2.000000" || p.Cache != "0.100000" || p.CacheWrite != "未使用" {
		t.Fatalf("mixed lanes: %+v", p)
	}
	row.ModelName = "free"
	if p := prices.price(job, row); p.Input != "0.000000" || p.Output != "0.000000" {
		t.Fatalf("all-zero prices must stay visible: %+v", p)
	}
}

// Completed statements are immutable: the whole statement is cached under one
// signature, so a statement listing more files than any per-file cap does not
// re-parse on every preview. A corrupt detail file fails the whole load.
func TestStatementPricesCachePerStatement(t *testing.T) {
	root := t.TempDir()
	day := time.Date(2026, 9, 1, 0, 0, 0, 0, billing.BusinessLocation)
	job := billing.Job{ID: "cache", JobType: "user_statement", UserID: 1}
	files := []billing.UserDailyFile{}
	for i := 0; i < 3; i++ {
		d := day.AddDate(0, 0, i)
		files = append(files, writeStatementDetailFile(t, root, "f"+d.Format("02")+".xlsx", job, d, []billing.RequestDetail{{ModelName: "m", ChannelID: 1, Charge: billing.LogCharge{InputPrice: "1", Total: "1"}}}))
	}
	store := statementPriceTestStore{files: files}
	statementPriceCache.Lock()
	before := len(statementPriceCache.items)
	statementPriceCache.Unlock()
	for i := 0; i < 2; i++ {
		prices, err := loadStatementPrices(context.Background(), job, store, root)
		if err != nil {
			t.Fatal(err)
		}
		if got := prices.price(job, billing.StatementAggregateRow{AggregateRow: billing.AggregateRow{Day: day.AddDate(0, 0, 2), ModelName: "m"}}).Input; got != "1.000000" {
			t.Fatalf("day 3 price = %s", got)
		}
	}
	statementPriceCache.Lock()
	after := len(statementPriceCache.items)
	statementPriceCache.Unlock()
	if after != before+1 {
		t.Fatalf("cache entries grew by %d, want exactly one per statement", after-before)
	}
	if err := os.WriteFile(filepath.Join(root, "broken.xlsx"), []byte("not a workbook"), 0o644); err != nil {
		t.Fatal(err)
	}
	store.files = append(store.files, billing.UserDailyFile{BillDay: day, RelativePath: "broken.xlsx"})
	if _, err := loadStatementPrices(context.Background(), job, store, root); err == nil {
		t.Fatal("corrupt detail file must fail the load instead of silently dropping prices")
	}
}

// Registrations written before f7a8fa4 may carry bill_day one day early. The
// workbook declares its own bill day, which must win over the registration.
func TestStatementPricesUseDeclaredBillDay(t *testing.T) {
	root := t.TempDir()
	day := time.Date(2026, 8, 20, 0, 0, 0, 0, billing.BusinessLocation)
	job := billing.Job{ID: "legacy", JobType: "user_statement", UserID: 7}
	file := writeStatementDetailFile(t, root, "legacy.xlsx", job, day, []billing.RequestDetail{{ModelName: "m", ChannelID: 12, Charge: billing.LogCharge{InputPrice: "3", OutputPrice: "15", Total: "1"}}})
	file.BillDay = time.Date(2026, 8, 19, 0, 0, 0, 0, time.UTC) // shifted registration
	prices, err := loadStatementPrices(context.Background(), job, statementPriceTestStore{files: []billing.UserDailyFile{file}}, root)
	if err != nil {
		t.Fatal(err)
	}
	row := billing.StatementAggregateRow{AggregateRow: billing.AggregateRow{Day: time.Date(2026, 8, 20, 0, 0, 0, 0, time.UTC), ModelName: "m"}}
	if p := prices.price(job, row); p.Input != "3.000000" || p.Output != "15.000000" {
		t.Fatalf("declared bill day ignored: %+v", p)
	}
}
