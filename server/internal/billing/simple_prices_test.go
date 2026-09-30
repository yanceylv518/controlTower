package billing

import (
	"bytes"
	"database/sql"
	"encoding/base64"
	"math/big"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDailySummaryDistinctUnitPrices(t *testing.T) {
	var out bytes.Buffer
	err := writeSettlementDailyWorkbookMode(&out, Job{UsageVersion: 3}, func(visit func(RequestDetail) error) error {
		for _, price := range []string{"2", "3", "2"} {
			if err := visit(RequestDetail{ModelName: "m", TokenName: "t", Charge: LogCharge{Total: "1", UnitPrices: UnitPrices{"输入|" + price: true, "输出|8": true}}}); err != nil {
				return err
			}
		}
		return nil
	}, true)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "summary.xlsx")
	if err = os.WriteFile(path, out.Bytes(), 0600); err != nil {
		t.Fatal(err)
	}
	content := workbookText(t, path)
	if !strings.Contains(content, "输入 2 / 3；输出 8") || strings.Contains(content, "计价规则") {
		t.Fatal(content)
	}
}

func TestSimpleUnitPriceSets(t *testing.T) {
	var prices UnitPrices
	prices.Merge(SimpleUnitPrices(LogCharge{InputPrice: "3", OutputPrice: "8", CacheReadPrice: "0"}))
	prices.Merge(SimpleUnitPrices(LogCharge{InputPrice: "2.000000", OutputPrice: "8.00", CacheReadPrice: "0"}))
	prices.Merge(SimpleUnitPrices(LogCharge{InputPrice: "2", OutputPrice: "8", CacheReadPrice: "0"}))
	if got := UnitPriceLabel(prices, big.NewRat(1, 1)); got != "输入 2 / 3；输出 8；缓存读取 0" {
		t.Fatal(got)
	}
	if got := UnitPriceLabel(prices, big.NewRat(72, 10)); got != "输入 14.4 / 21.6；输出 57.6；缓存读取 0" {
		t.Fatal(got)
	}
	prices.Merge(nil)
	if got := UnitPriceLabel(prices, big.NewRat(1, 1)); got != "输入 2 / 3；输出 8；缓存读取 0；部分未记录" {
		t.Fatal(got)
	}
	parsed, err := ParseUnitPriceGroups([]byte(`[{"输入|2":true},{"输入|2":true,"输入|3":true},null]`))
	if err != nil || UnitPriceLabel(parsed, big.NewRat(1, 1)) != "输入 2 / 3；部分未记录" {
		t.Fatal(parsed, err)
	}
	parsed, err = ParseUnitPriceGroups([]byte(`[null]`))
	if err != nil || UnitPriceLabel(parsed, big.NewRat(1, 1)) != "未记录" {
		t.Fatal(parsed, err)
	}
}

func TestSimplePricesKeepSettlementUnchanged(t *testing.T) {
	job := Job{UsageVersion: 3}
	for _, tc := range []struct {
		log  PagedLogRecord
		want string
	}{
		{PagedLogRecord{ModelRatio: "1", CompletionRatio: "4", GroupRatio: "1"}, "输入 2；输出 8"},
		{PagedLogRecord{ModelPrice: "0", GroupRatio: "1"}, "按次 0/次"},
		{PagedLogRecord{BillingMode: "tiered_expr", ExprBase64: base64.StdEncoding.EncodeToString([]byte(`v1:p * 2 + c * 8`)), GroupRatio: "1", PromptTokens: sql.NullInt64{Int64: 10, Valid: true}, CompletionTokens: sql.NullInt64{Int64: 2, Valid: true}}, "输入 2；输出 8"},
		{PagedLogRecord{BillingMode: "tiered_expr", ExprBase64: "invalid"}, "输入 未记录；输出 未记录"},
	} {
		settlement := &Settlement{BeforeAmount: "99", Discount: "0.5", Amount: "49.5"}
		c := LogCharge{Total: "49.5", Settlement: settlement, Mode: "newapi"}
		attachHistoricalPrices(job, tc.log, "500000", &c)
		attachSimplePrices(job, tc.log, "500000", &c)
		if got := UnitPriceLabel(c.UnitPrices, big.NewRat(1, 1)); got != tc.want {
			t.Fatalf("got %q want %q", got, tc.want)
		}
		if c.Total != "49.5" || c.Settlement != settlement || c.Settlement.BeforeAmount != "99" || c.Mode != "newapi" {
			t.Fatalf("changed charge: %+v", c)
		}
	}
}
