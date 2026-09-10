package dashboard

import (
	"archive/zip"
	"bytes"
	"context"
	"io"
	"strings"
	"testing"
	"time"

	"controltower/server/internal/billing"
)

type fastSummaryStore struct{ BillingStatementResultStore }

func (fastSummaryStore) ListBillingStatementUserFiles(context.Context, string) ([]billing.UserDailyFile, error) {
	panic("summary must not read daily detail files")
}
func (fastSummaryStore) QueryBillingTokenRows(context.Context, string, int64, int64, time.Time, time.Time) ([]billing.TokenDailyRow, error) {
	return nil, nil
}
func (fastSummaryStore) ListBillingStatementDiscounts(context.Context, string) ([]billing.StatementDiscount, error) {
	return nil, nil
}

func TestStatementSummarySkipsDetailPrices(t *testing.T) {
	for _, kind := range []string{"user_statement", "upstream_statement"} {
		t.Run(kind, func(t *testing.T) {
			rows := []billing.StatementAggregateRow{{AggregateRow: billing.AggregateRow{Day: time.Date(2026, 9, 1, 0, 0, 0, 0, billing.BusinessLocation), ModelName: "model", RequestCount: 3, PromptTokens: 120, Amount: "12.50000000"}}}
			book, err := statementWorkbook(billing.Job{ID: "summary", JobType: kind}, rows, fastSummaryStore{}, t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			z, err := zip.NewReader(bytes.NewReader(book), int64(len(book)))
			if err != nil {
				t.Fatal(err)
			}
			for _, file := range z.File {
				if file.Name != "xl/worksheets/sheet2.xml" {
					continue
				}
				r, err := file.Open()
				if err != nil {
					t.Fatal(err)
				}
				data, err := io.ReadAll(r)
				r.Close()
				if err != nil {
					t.Fatal(err)
				}
				xml := string(data)
				for _, label := range []string{"输入单价", "输出单价", "缓存读取单价", "缓存写入单价"} {
					if strings.Contains(xml, label) {
						t.Fatalf("unexpected price column %s", label)
					}
				}
				for _, value := range []string{"订单数", "输入 Token", "折扣", "最终费用", "12.50000000", ">120<", ">3<"} {
					if !strings.Contains(xml, value) {
						t.Fatalf("missing summary data %s: %s", value, xml)
					}
				}
				column := "J"
				if kind == "upstream_statement" {
					column = "K"
				}
				if !strings.Contains(xml, `r="`+column+`6"`) {
					t.Fatalf("total is in wrong column: %s", xml)
				}
				return
			}
			t.Fatal("daily summary sheet missing")
		})
	}
}

type layoutTokenStore struct{ fastSummaryStore }

func (layoutTokenStore) QueryBillingTokenRows(context.Context, string, int64, int64, time.Time, time.Time) ([]billing.TokenDailyRow, error) {
	return []billing.TokenDailyRow{
		{TokenID: 7, TokenName: "same", RequestCount: 2, PromptTokens: 10, Amount: "1.25000000"},
		{TokenID: 7, TokenName: "same", RequestCount: 3, PromptTokens: 20, Amount: "2.50000000"},
		{TokenID: 8, TokenName: "same", RequestCount: 1, PromptTokens: 5, Amount: "0.25000000"},
	}, nil
}
func TestStatementWorkbookLayoutAndTokenTotals(t *testing.T) {
	day := time.Date(2026, 8, 1, 0, 0, 0, 0, billing.BusinessLocation)
	data, err := statementWorkbook(billing.Job{JobType: "user_statement", UserName: "上海万联", From: day, To: day.AddDate(0, 1, 0)}, nil, layoutTokenStore{})
	if err != nil {
		t.Fatal(err)
	}
	z, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatal(err)
	}
	parts := map[string]string{}
	for _, f := range z.File {
		r, e := f.Open()
		if e != nil {
			t.Fatal(e)
		}
		b, e := io.ReadAll(r)
		r.Close()
		if e != nil {
			t.Fatal(e)
		}
		parts[f.Name] = string(b)
	}
	for _, name := range []string{"账单总览", "每日账单", "令牌用量汇总", "令牌每日用量"} {
		if !strings.Contains(parts["xl/workbook.xml"], name) {
			t.Fatal(name)
		}
	}
	for _, path := range []string{"xl/worksheets/sheet1.xml", "xl/worksheets/sheet2.xml", "xl/worksheets/sheet3.xml", "xl/worksheets/sheet4.xml"} {
		xml := parts[path]
		for _, value := range []string{"上海万联 · 20260801至20260831 · 对账单", "mergeCell ref=", "s=\"10\"", "合计"} {
			if !strings.Contains(xml, value) {
				t.Fatalf("%s missing %s", path, value)
			}
		}
	}
	xml := parts["xl/worksheets/sheet3.xml"]
	for _, value := range []string{`r="C5" s="3"><v>5.00000000</v>`, `r="H5" s="4"><v>3.75000000</v>`, `r="C7" s="8"><v>6.00000000</v>`, `r="H7" s="10"><v>4.00000000</v>`} {
		if !strings.Contains(xml, value) {
			t.Fatalf("missing %s: %s", value, xml)
		}
	}
}
