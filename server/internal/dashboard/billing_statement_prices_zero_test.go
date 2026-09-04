package dashboard

import (
	"context"
	"strings"
	"testing"
	"time"

	"controltower/server/internal/billing"
)

func TestStatementPricesPreserveUsedFreeLanes(t *testing.T) {
	for _, kind := range []string{"user_statement", "upstream_statement"} {
		t.Run(kind, func(t *testing.T) {
			root := t.TempDir()
			day := time.Date(2026, 9, 1, 0, 0, 0, 0, billing.BusinessLocation)
			job := billing.Job{ID: "free-" + kind, JobType: kind, UserID: 1}
			file := writeStatementDetailFile(t, root, "details.xlsx", job, day, []billing.RequestDetail{
				{ModelName: "m", ChannelID: 7, PromptTokens: 100, CompletionTokens: 100, CacheReadTokens: 100, CacheWriteTokens: 100,
					Charge: billing.LogCharge{InputPrice: "0", OutputPrice: "0", CacheReadPrice: "0", CacheWritePrice: "0", Total: "0"}},
				{ModelName: "m", ChannelID: 7, PromptTokens: 100, CompletionTokens: 100, CacheReadTokens: 100, CacheWriteTokens: 100,
					Charge: billing.LogCharge{InputPrice: "1", OutputPrice: "2", CacheReadPrice: "0.1", CacheWritePrice: "0.2", Total: "0.000330"}},
				{ModelName: "m", ChannelID: 7, Charge: billing.LogCharge{Total: "0"}}, // Unused placeholder zeros.
			})
			prices, err := loadStatementPrices(context.Background(), job, statementPriceTestStore{files: []billing.UserDailyFile{file}}, root)
			if err != nil {
				t.Fatal(err)
			}
			row := billing.StatementAggregateRow{ChannelID: 7, AggregateRow: billing.AggregateRow{Day: day, ModelName: "m"}}
			price := prices.price(job, row)
			if price.Input != "0.000000 / 1.000000" || price.Output != "0.000000 / 2.000000" || price.Cache != "0.000000 / 0.100000" || price.CacheWrite != "0.000000 / 0.200000" {
				t.Fatalf("actual free prices lost: %+v", price)
			}
		})
	}
}

func TestStatementPricesMissingUsageDoesNotMeanUnused(t *testing.T) {
	// A legacy sheet without token counts cannot prove that its zero is a
	// placeholder. Preserve it when another row has a paid price.
	xml := `<worksheet><sheetData>
<row><c r="A1" t="inlineStr"><is><t>模型</t></is></c><c r="B1" t="inlineStr"><is><t>输入单价</t></is></c></row>
<row><c r="A2" t="inlineStr"><is><t>m</t></is></c><c r="B2"><v>0</v></c></row>
<row><c r="A3" t="inlineStr"><is><t>m</t></is></c><c r="B3"><v>1</v></c></row>
</sheetData></worksheet>`
	prices := statementPrices{}
	if _, err := readStatementPriceSheet(context.Background(), strings.NewReader(xml), prices); err != nil {
		t.Fatal(err)
	}
	for _, tuples := range prices {
		for tuple := range tuples {
			if tuple[0] == unusedStatementPrice {
				t.Fatal("missing token count was treated as zero")
			}
		}
	}
}
