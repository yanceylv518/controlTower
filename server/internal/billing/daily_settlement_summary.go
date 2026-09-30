package billing

import (
	"controltower/server/internal/xlsxwriter"
	"fmt"
	"io"
	"math/big"
	"sort"
	"time"
)

func writeDailySettlementSummary(wb *xlsxwriter.Workbook, currency, metadata string, iterate func(func(RequestDetail) error) error) error {
	type totals struct {
		MultimediaUsage
		requests, input, output, cache, write int64
		amount                                *big.Rat
		before, discount                      string
	}
	groups := []map[string]*totals{{"合计": {amount: new(big.Rat)}}, {}, {}}
	if err := iterate(func(v RequestDetail) error {
		for i, key := range []string{"合计", v.ModelName, fmt.Sprintf("%s (#%d)", v.TokenName, v.TokenID)} {
			g := groups[i][key]
			if g == nil {
				g = &totals{amount: new(big.Rat)}
				groups[i][key] = g
			}
			before, discount := ChargeOriginal(v.Charge)
			if g.requests == 0 {
				g.before = before
				g.discount = discount
			} else {
				g.before = MergeBefore(g.before, before)
				g.discount = MergeDiscount(g.discount, discount)
			}
			g.requests++
			g.Add(v.MultimediaUsage)
			g.input += v.PromptTokens
			g.output += v.CompletionTokens
			g.cache += v.CacheReadTokens
			g.write += v.CacheWriteTokens
			if n, ok := new(big.Rat).SetString(v.Charge.Total); ok {
				g.amount.Add(g.amount, n)
			}
		}
		return nil
	}); err != nil {
		return err
	}
	for i, name := range []string{"每日统计", "按模型统计", "按令牌统计"} {
		sheet, err := wb.AddReportSheet(name, name, metadata, []float64{48, 14, 17, 17, 17, 17, 18, 18, 18, 18, 20, 16, 20})
		if err != nil {
			return err
		}
		headers := []xlsxwriter.Cell{}
		for _, h := range []string{"统计对象", "请求数", "普通输入 Token", "普通输出 Token", "缓存读取 Token", "缓存写入 Token", "图像输入 Token", "图像输出 Token", "音频输入 Token", "音频输出 Token", "原价金额 " + currency, "折扣", "折后金额 " + currency} {
			headers = append(headers, xlsxwriter.Cell{Value: h, Style: 1})
		}
		if err = sheet.Row(headers); err != nil {
			return err
		}
		keys := []string{}
		for key := range groups[i] {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		for _, key := range keys {
			v := groups[i][key]
			if err = sheet.Row([]xlsxwriter.Cell{{Value: key}, numberCell(v.requests, 3), numberCell(v.input, 3), numberCell(v.output, 3), numberCell(v.cache, 3), numberCell(v.write, 3), numberCell(v.ImageInputTokens, 3), numberCell(v.ImageOutputTokens, 3), numberCell(v.AudioInputTokens, 3), numberCell(v.AudioOutputTokens, 3), originalAmountCell(v.before), {Value: DiscountLabel(v.discount)}, decimalCell(v.amount.FloatString(12))}); err != nil {
				return err
			}
		}
	}
	return nil
}

func writeSettlementDailyWorkbook(out io.Writer, job Job, rawIterate func(func(RequestDetail) error) error) error {
	return writeSettlementDailyWorkbookMode(out, job, rawIterate, false)
}
func writeSettlementDailyWorkbookMode(out io.Writer, job Job, rawIterate func(func(RequestDetail) error) error, summaryOnly bool) error {
	display, rate, err := SettlementDisplay(job.MoneySnapshot)
	if err != nil {
		return err
	}
	currency := SettlementCurrencyLabel(display)
	iterate := func(visit func(RequestDetail) error) error {
		return rawIterate(func(v RequestDetail) error {
			v.Charge.Total = DisplaySettlementAmount(v.Charge.Total, rate)
			if v.Charge.Settlement != nil {
				snapshot := *v.Charge.Settlement
				snapshot.BeforeAmount = DisplaySettlementAmount(snapshot.BeforeAmount, rate)
				v.Charge.Settlement = &snapshot
			}
			return visit(v)
		})
	}
	wb := xlsxwriter.New()
	defer wb.Discard()
	if err := writeDailySettlementSummary(wb, currency, SettlementSheetMetadata(job), iterate); err != nil {
		return err
	}
	if summaryOnly {
		return wb.Write(out)
	}
	var sheet *xlsxwriter.Sheet
	part, count := 0, 0
	next := func() error {
		part++
		name := "账单明细"
		if part > 1 {
			name = fmt.Sprintf("账单明细-%d", part)
		}
		var err error
		sheet, err = wb.AddReportSheet(name, name, SettlementSheetMetadata(job), []float64{24, 48, 34, 30, 15, 15, 15, 15, 18, 18, 18, 18, 20, 16, 20})
		if err != nil {
			return err
		}
		cells := []xlsxwriter.Cell{}
		for _, label := range []string{"时间", "请求 ID", "模型", "令牌", "普通输入 Token", "普通输出 Token", "缓存读取", "缓存写入", "图像输入 Token", "图像输出 Token", "音频输入 Token", "音频输出 Token", "原价金额 " + currency, "折扣", "折后金额 " + currency} {
			cells = append(cells, xlsxwriter.Cell{Value: label, Style: 1})
		}
		count = 0
		return sheet.Row(cells)
	}
	if err := next(); err != nil {
		return err
	}
	if err := iterate(func(v RequestDetail) error {
		if count >= 1_000_000 {
			if err := next(); err != nil {
				return err
			}
		}
		count++
		before, discount := ChargeOriginal(v.Charge)
		return sheet.Row([]xlsxwriter.Cell{{Value: time.Unix(v.CreatedUnix, 0).In(BusinessLocation).Format("2006-01-02 15:04:05")}, {Value: v.RequestID}, {Value: v.ModelName}, {Value: v.TokenName}, numberCell(v.PromptTokens, 3), numberCell(v.CompletionTokens, 3), numberCell(v.CacheReadTokens, 3), numberCell(v.CacheWriteTokens, 3), numberCell(v.ImageInputTokens, 3), numberCell(v.ImageOutputTokens, 3), numberCell(v.AudioInputTokens, 3), numberCell(v.AudioOutputTokens, 3), originalAmountCell(before), {Value: DiscountLabel(discount)}, decimalCell(v.Charge.Total)})
	}); err != nil {
		return err
	}
	return wb.Write(out)
}

func SettlementSheetMetadata(job Job) string {
	subject := job.UserName
	if subject == "" {
		subject = fmt.Sprintf("用户 #%d", job.UserID)
	}
	if job.JobType == "upstream_statement" {
		subject = job.UpstreamName
		if subject == "" {
			subject = fmt.Sprintf("上游 #%d", job.UpstreamID)
		}
	}
	period := job.From.In(BusinessLocation).Format("2006-01-02 15:04") + " 至 " + job.To.In(BusinessLocation).Format("2006-01-02 15:04")
	if job.BillPeriod == "daily" {
		period = job.From.In(BusinessLocation).Format("2006-01-02")
	}
	return subject + "    " + period
}

func originalAmountCell(v string) xlsxwriter.Cell {
	if v == "" {
		return xlsxwriter.Cell{Value: "—"}
	}
	return decimalCell(v)
}
