package billing

import (
	"context"
	"fmt"
	"io"
	"time"

	"controltower/server/internal/xlsxwriter"
)

// Export only the on-demand task snapshot, using its captured currency and exchange rate.
func WriteZeroOutputWorkbook(ctx context.Context, out io.Writer, job Job, iterate func(func(RequestDetail) error) error) error {
	display, rate, err := SettlementDisplay(job.MoneySnapshot)
	if err != nil {
		return err
	}
	currency := SettlementCurrencyLabel(display)
	wb := xlsxwriter.NewBuffered()
	defer wb.Discard()
	var sheet *xlsxwriter.Sheet
	count, part := 0, 0
	next := func() error {
		if sheet != nil {
			if err := sheet.Close(); err != nil {
				return err
			}
		}
		part++
		count = 0
		var err error
		sheet, err = wb.AddReportSheet(fmt.Sprintf("空输出订单-%d", part), "空输出订单", SettlementSheetMetadata(job)+"    数据来源："+job.DataSource+"    任务提交："+job.CreatedAt.In(BusinessLocation).Format("2006-01-02 15:04:05")+"    按生成时可用数据及计费配置生成，不修改结算账单", []float64{24, 20, 48, 48, 16, 28, 16, 28, 16, 28, 34, 18, 18, 18, 18, 18, 18, 18, 18, 48, 20, 16, 20})
		if err != nil {
			return err
		}
		headers := []string{"时间", "源日志 ID", "请求 ID", "上游请求 ID", "用户 ID", "用户", "令牌 ID", "令牌", "渠道 ID", "渠道", "模型", "普通输入 Token", "普通输出 Token", "缓存读取 Token", "缓存写入 Token", "图像输入 Token", "图像输出 Token", "音频输入 Token", "音频输出 Token", UnitPriceHeader(currency), "原价金额 " + currency, "折扣", "空输出金额 " + currency}
		cells := make([]xlsxwriter.Cell, len(headers))
		for i, v := range headers {
			cells[i] = xlsxwriter.Cell{Value: v}
		}
		return sheet.Row(cells)
	}
	if err = next(); err != nil {
		return err
	}
	err = iterate(func(v RequestDetail) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		if !v.EmptyOutput {
			return fmt.Errorf("invalid zero output snapshot")
		}
		if count >= SettlementDetailPartRows {
			if err := next(); err != nil {
				return err
			}
		}
		count++
		before, discount := ChargeOriginal(v.Charge)
		return sheet.Row([]xlsxwriter.Cell{
			{Value: time.Unix(v.CreatedUnix, 0).In(BusinessLocation).Format("2006-01-02 15:04:05")},
			{Value: fmt.Sprint(v.SourceLogID)}, {Value: v.RequestID}, {Value: v.UpstreamRequestID},
			{Value: fmt.Sprint(v.UserID)}, {Value: v.Username}, {Value: fmt.Sprint(v.TokenID)}, {Value: v.TokenName},
			{Value: fmt.Sprint(v.ChannelID)}, {Value: v.ChannelName}, {Value: v.ModelName},
			numberCell(v.PromptTokens, 3), numberCell(v.CompletionTokens, 3), numberCell(v.CacheReadTokens, 3), numberCell(v.CacheWriteTokens, 3),
			numberCell(v.ImageInputTokens, 3), numberCell(v.ImageOutputTokens, 3), numberCell(v.AudioInputTokens, 3), numberCell(v.AudioOutputTokens, 3),
			{Value: UnitPriceLabel(v.Charge.UnitPrices, rate)}, originalAmountCell(DisplaySettlementAmount(before, rate)),
			{Value: DiscountLabel(discount)}, decimalCell(DisplaySettlementAmount(v.Charge.Total, rate)),
		})
	})
	if err != nil {
		return err
	}
	return wb.Write(zeroOutputWriter{ctx: ctx, out: out})
}

type zeroOutputWriter struct {
	ctx context.Context
	out io.Writer
}

func (w zeroOutputWriter) Write(p []byte) (int, error) {
	if err := w.ctx.Err(); err != nil {
		return 0, err
	}
	return w.out.Write(p)
}
