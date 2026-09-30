package dashboard

import (
	"bytes"
	"context"
	"controltower/server/internal/billing"
	"controltower/server/internal/xlsxwriter"
	"fmt"
	"math/big"
	"strconv"
)

func settlementWorkbook(job billing.Job, rows []billing.StatementAggregateRow, store BillingStatementResultStore) ([]byte, error) {
	display, rate, err := billing.SettlementDisplay(job.MoneySnapshot)
	if err != nil {
		return nil, err
	}
	if job.JobType == "user_statement" && job.BillPeriod == "daily" {
		return dailySettlementSummaryWorkbook(job, rows, store)
	}
	userColumns := func(cells []xlsxwriter.Cell) []xlsxwriter.Cell {
		if job.JobType == "user_statement" {
			return append(cells[:2], cells[3:]...)
		}
		return cells
	}
	widths := []float64{16, 36, 30, 13, 17, 17, 17, 17, 18, 18, 18, 18, 20, 16, 20}
	if job.JobType == "user_statement" {
		widths = append(widths[:2], widths[3:]...)
	}
	wb := xlsxwriter.New()
	defer wb.Discard()
	addSheet := func(name string, widths []float64) (*xlsxwriter.Sheet, error) {
		if job.JobType == "user_statement" && job.BillPeriod == "monthly" {
			from, to := job.From.In(billing.BusinessLocation), job.To.In(billing.BusinessLocation).AddDate(0, 0, -1)
			customer := job.UserName
			if customer == "" {
				customer = fmt.Sprintf("用户 #%d", job.UserID)
			}
			return wb.AddReconciliationSheet(name, fmt.Sprintf("%d年%d月对账汇总单", from.Year(), int(from.Month())), customer, "", from.Format("2006-01-02")+" 至 "+to.Format("2006-01-02"), job.InstanceID, widths)
		}
		return wb.AddReportSheet(name, name, billing.SettlementSheetMetadata(job), widths)
	}

	for _, daily := range []bool{false, true} {
		name := "账单统计"
		if job.BillPeriod == "monthly" {
			name = "月统计"
		}
		if daily {
			name = "每日模型统计"
		}
		sheet, err := addSheet(name, widths)
		if err != nil {
			return nil, err
		}
		header := []xlsxwriter.Cell{t("日期"), t("模型"), t("渠道"), t("请求数"), t("普通输入 Token"), t("普通输出 Token"), t("缓存读取 Token"), t("缓存写入 Token"), t("图像输入 Token"), t("图像输出 Token"), t("音频输入 Token"), t("音频输出 Token"), t("原价金额 " + billing.SettlementCurrencyLabel(display)), t("折扣"), t("折后金额 " + billing.SettlementCurrencyLabel(display))}
		for i := range header {
			header[i].Style = 1
		}
		if err = sheet.Row(userColumns(header)); err != nil {
			return nil, err
		}
		for _, g := range groupStatementRows(job, rows, nil, daily) {
			v := g.Row
			date := job.From.In(billing.BusinessLocation).Format("2006-01")
			if daily {
				date = v.Day.Format("2006-01-02")
			}
			if err = sheet.Row(userColumns([]xlsxwriter.Cell{t(date), t(v.ModelName), t(v.ChannelName), n64(v.RequestCount), n64(v.PromptTokens), n64(v.CompletionTokens), n64(v.CacheTokens), n64(v.CacheWriteTokens), n64(v.ImageInputTokens), n64(v.ImageOutputTokens), n64(v.AudioInputTokens), n64(v.AudioOutputTokens), settlementOriginalCell(billing.DisplaySettlementAmount(v.BeforeAmount, rate)), t(billing.DiscountLabel(v.SettlementDiscount)), d(billing.DisplaySettlementAmount(v.Amount, rate))})); err != nil {
				return nil, err
			}
		}
	}
	if job.JobType == "user_statement" {
		tokens, err := store.QueryBillingTokenRows(context.Background(), job.ID, job.UserID, -1, job.From, job.To)
		if err != nil {
			return nil, err
		}
		sheet, err := addSheet("每日令牌统计", []float64{16, 30, 13, 36, 13, 17, 17, 17, 17, 18, 18, 18, 18, 20, 16, 20})
		if err != nil {
			return nil, err
		}
		header := []xlsxwriter.Cell{t("日期"), t("令牌"), t("令牌 ID"), t("模型"), t("请求数"), t("普通输入 Token"), t("普通输出 Token"), t("缓存读取 Token"), t("缓存写入 Token"), t("图像输入 Token"), t("图像输出 Token"), t("音频输入 Token"), t("音频输出 Token"), t("原价金额 " + billing.SettlementCurrencyLabel(display)), t("折扣"), t("折后金额 " + billing.SettlementCurrencyLabel(display))}
		for i := range header {
			header[i].Style = 1
		}
		if err = sheet.Row(header); err != nil {
			return nil, err
		}
		for _, v := range tokens {
			if err = sheet.Row([]xlsxwriter.Cell{t(v.Day.Format("2006-01-02")), t(v.TokenName), n64(v.TokenID), t(v.ModelName), n64(v.RequestCount), n64(v.PromptTokens), n64(v.CompletionTokens), n64(v.CacheTokens), n64(v.CacheWriteTokens), n64(v.ImageInputTokens), n64(v.ImageOutputTokens), n64(v.AudioInputTokens), n64(v.AudioOutputTokens), settlementOriginalCell(billing.DisplaySettlementAmount(v.BeforeAmount, rate)), t(billing.DiscountLabel(v.SettlementDiscount)), d(billing.DisplaySettlementAmount(v.Amount, rate))}); err != nil {
				return nil, err
			}
		}
	}
	var out bytes.Buffer
	err = wb.Write(&out)
	return out.Bytes(), err
}

func dailySettlementSummaryWorkbook(job billing.Job, rows []billing.StatementAggregateRow, store BillingStatementResultStore) ([]byte, error) {
	display, rate, err := billing.SettlementDisplay(job.MoneySnapshot)
	if err != nil {
		return nil, err
	}
	wb := xlsxwriter.New()
	defer wb.Discard()
	add := func(name string, headers []string, widths []float64) (*xlsxwriter.Sheet, error) {
		s, e := wb.AddReportSheet(name, name, billing.SettlementSheetMetadata(job), widths)
		if e != nil {
			return nil, e
		}
		cells := []xlsxwriter.Cell{}
		for _, h := range headers {
			cells = append(cells, t(h))
		}
		return s, s.Row(cells)
	}
	cost := "折后金额 " + billing.SettlementCurrencyLabel(display)
	headers := []string{"统计对象", "请求数", "普通输入 Token", "普通输出 Token", "缓存读取 Token", "缓存写入 Token", "图像输入 Token", "图像输出 Token", "音频输入 Token", "音频输出 Token", "原价金额 " + billing.SettlementCurrencyLabel(display), "折扣", cost}
	summary, err := add("日汇总", headers, []float64{40, 14, 18, 18, 18, 18, 18, 18, 18, 18, 20, 16, 20})
	if err != nil {
		return nil, err
	}
	var count, input, output, read, write int64
	var media billing.MultimediaUsage
	amount := new(big.Rat)
	before, discount := "", ""
	for _, v := range rows {
		if count == 0 {
			before = v.BeforeAmount
			discount = v.SettlementDiscount
		} else {
			before = billing.MergeBefore(before, v.BeforeAmount)
			discount = billing.MergeDiscount(discount, v.SettlementDiscount)
		}
		count += v.RequestCount
		media.Add(v.MultimediaUsage)
		input += v.PromptTokens
		output += v.CompletionTokens
		read += v.CacheTokens
		write += v.CacheWriteTokens
		if a, ok := new(big.Rat).SetString(v.Amount); ok {
			amount.Add(amount, a)
		}
	}
	if err = summary.Row([]xlsxwriter.Cell{t("合计"), n64(count), n64(input), n64(output), n64(read), n64(write), n64(media.ImageInputTokens), n64(media.ImageOutputTokens), n64(media.AudioInputTokens), n64(media.AudioOutputTokens), settlementOriginalCell(billing.DisplaySettlementAmount(before, rate)), t(billing.DiscountLabel(discount)), d(billing.DisplaySettlementAmount(amount.FloatString(12), rate))}); err != nil {
		return nil, err
	}
	models, err := add("模型统计", headers, []float64{40, 14, 18, 18, 18, 18, 18, 18, 18, 18, 20, 16, 20})
	if err != nil {
		return nil, err
	}
	for _, g := range groupStatementRows(job, rows, nil, false) {
		v := g.Row
		if err = models.Row([]xlsxwriter.Cell{t(v.ModelName), n64(v.RequestCount), n64(v.PromptTokens), n64(v.CompletionTokens), n64(v.CacheTokens), n64(v.CacheWriteTokens), n64(v.ImageInputTokens), n64(v.ImageOutputTokens), n64(v.AudioInputTokens), n64(v.AudioOutputTokens), settlementOriginalCell(billing.DisplaySettlementAmount(v.BeforeAmount, rate)), t(billing.DiscountLabel(v.SettlementDiscount)), d(billing.DisplaySettlementAmount(v.Amount, rate))}); err != nil {
			return nil, err
		}
	}
	tokens, err := store.QueryBillingTokenRows(context.Background(), job.ID, job.UserID, -1, job.From, job.To)
	if err != nil {
		return nil, err
	}
	sheet, err := add("令牌统计", []string{"令牌", "令牌 ID", "模型", "请求数", "普通输入 Token", "普通输出 Token", "缓存读取 Token", "缓存写入 Token", "图像输入 Token", "图像输出 Token", "音频输入 Token", "音频输出 Token", "原价金额 " + billing.SettlementCurrencyLabel(display), "折扣", cost}, []float64{30, 14, 36, 14, 18, 18, 18, 18, 18, 18, 18, 18, 20, 16, 20})
	if err != nil {
		return nil, err
	}
	for _, v := range tokens {
		if err = sheet.Row([]xlsxwriter.Cell{t(v.TokenName), t(strconv.FormatInt(v.TokenID, 10)), t(v.ModelName), n64(v.RequestCount), n64(v.PromptTokens), n64(v.CompletionTokens), n64(v.CacheTokens), n64(v.CacheWriteTokens), n64(v.ImageInputTokens), n64(v.ImageOutputTokens), n64(v.AudioInputTokens), n64(v.AudioOutputTokens), settlementOriginalCell(billing.DisplaySettlementAmount(v.BeforeAmount, rate)), t(billing.DiscountLabel(v.SettlementDiscount)), d(billing.DisplaySettlementAmount(v.Amount, rate))}); err != nil {
			return nil, err
		}
	}
	var out bytes.Buffer
	err = wb.Write(&out)
	return out.Bytes(), err
}

func settlementOriginalCell(v string) xlsxwriter.Cell {
	if v == "" {
		return t("—")
	}
	return d(v)
}
