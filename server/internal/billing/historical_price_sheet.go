package billing

import (
	"sort"
	"strconv"
	"strings"

	"controltower/server/internal/xlsxwriter"
)

// One row per distinct historical rule, not per request. The compact sheet lets
// daily previews and summary downloads avoid scanning millions of detail rows.
type historicalPriceRow struct {
	Model, Channel string
	Prices         [8]string
	Rule           string
}

func priceSnapshot(row RequestDetail) historicalPriceRow {
	c := row.Charge
	return historicalPriceRow{row.ModelName, strconv.FormatInt(row.ChannelID, 10), [8]string{c.InputPrice, c.OutputPrice, c.CacheReadPrice, c.CacheWritePrice, c.CacheWrite5mPrice, c.CacheWrite1hPrice, c.ImagePrice, c.PerRequestPrice}, c.PricingRule}
}

func writeHistoricalPriceSheet(wb *xlsxwriter.Workbook, day string, prices map[historicalPriceRow]bool) error {
	sheet, err := wb.AddSheet("历史计价规则", []float64{28, 16, 16, 16, 16, 16, 16, 16, 16, 16, 100})
	if err != nil {
		return err
	}
	if err = sheet.Row([]xlsxwriter.Cell{{Value: "账单日期"}, {Value: day}}); err != nil {
		return err
	}
	if err = sheet.Row([]xlsxwriter.Cell{{Value: "单价单位：金额/百万 Token；按次单价为金额/次。每行是一套历史规则，金额以账单计费来源为准。"}}); err != nil {
		return err
	}
	if err = sheet.Row(nil); err != nil {
		return err
	}
	headers := []string{"模型", "渠道", "输入单价", "输出单价", "缓存读取单价", "普通缓存写入单价", "5m 写入单价", "1h 写入单价", "图像输入单价", "按次单价", "计价规则"}
	cells := make([]xlsxwriter.Cell, len(headers))
	for i, h := range headers {
		cells[i] = xlsxwriter.Cell{Value: h, Style: 1}
	}
	if err = sheet.Row(cells); err != nil {
		return err
	}
	rows := make([]historicalPriceRow, 0, len(prices))
	for row := range prices {
		rows = append(rows, row)
	}
	sort.Slice(rows, func(i, j int) bool {
		a, b := rows[i], rows[j]
		if a.Model != b.Model {
			return a.Model < b.Model
		}
		if a.Channel != b.Channel {
			return a.Channel < b.Channel
		}
		if a.Rule != b.Rule {
			return a.Rule < b.Rule
		}
		return strings.Join(a.Prices[:], "\x00") < strings.Join(b.Prices[:], "\x00")
	})
	for _, row := range rows {
		cells = []xlsxwriter.Cell{{Value: row.Model}, {Value: row.Channel}}
		for _, price := range row.Prices {
			cells = append(cells, historicalPriceCell(price))
		}
		cells = append(cells, xlsxwriter.Cell{Value: row.Rule, Style: xlsxwriter.WrappedTextStyle})
		if err = sheet.Row(cells); err != nil {
			return err
		}
	}
	return nil
}

func historicalPriceCell(value string) xlsxwriter.Cell {
	if value == "" {
		value = "未记录"
	}
	if _, err := decimalRat(value); err != nil {
		return xlsxwriter.Cell{Value: value, Style: 5}
	}
	return decimalCell(value)
}

func hasPerRequestPrice(charge LogCharge) bool {
	return charge.Mode == "per_request" || decimalNonZero(charge.PerRequestPrice) || strings.HasPrefix(charge.PricingRule, "按次计费：")
}
