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
				if !strings.Contains(xml, `r="`+column+`3"`) {
					t.Fatalf("total is in wrong column: %s", xml)
				}
				return
			}
			t.Fatal("daily summary sheet missing")
		})
	}
}
