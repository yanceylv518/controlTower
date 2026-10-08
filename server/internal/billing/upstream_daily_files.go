package billing

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"controltower/server/internal/xlsxwriter"
)

// Publish from the already priced request snapshot; never query or reprice orders.
func (g UserDailyFileGenerator) publishUpstreamChannelWorkbook(ctx context.Context, root string, job Job, group ChannelDailyFile, iterate func(func(RequestDetail) error) error) error {
	relative := strings.TrimSuffix(channelDailyFileRelativePath(job, group), ".csv") + ".xlsx"
	target := filepath.Join(root, relative)
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(target), ".upstream-*.xlsx")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	defer tmp.Close()
	display, rate, err := SettlementDisplay(job.MoneySnapshot)
	if err != nil {
		return err
	}
	currency := SettlementCurrencyLabel(display)
	job.From, job.To, job.BillPeriod = group.BillDay, group.BillDay.AddDate(0, 0, 1), "daily"
	wb := xlsxwriter.New()
	defer wb.Discard()
	var sheet *xlsxwriter.Sheet
	count, part := 0, 0
	next := func() error {
		part++
		name := "账单明细"
		if part > 1 {
			name = fmt.Sprintf("账单明细-%d", part)
		}
		var e error
		sheet, e = wb.AddReportSheet(name, "上游日账单明细", SettlementSheetMetadata(job), []float64{24, 48, 30, 16, 34, 16, 16, 16, 16, 18, 18, 18, 18, 48, 20, 16, 20})
		if e != nil {
			return e
		}
		headers := []xlsxwriter.Cell{}
		for _, h := range []string{"时间", "请求 ID", "渠道", "渠道 ID", "模型", "普通输入 Token", "普通输出 Token", "缓存读取", "缓存写入", "图像输入 Token", "图像输出 Token", "音频输入 Token", "音频输出 Token", UnitPriceHeader(currency), "原价金额 " + currency, "折扣", "折后金额 " + currency} {
			headers = append(headers, xlsxwriter.Cell{Value: h})
		}
		count = 0
		return sheet.Row(headers)
	}
	if err = next(); err != nil {
		return err
	}
	err = iterate(func(v RequestDetail) error {
		if e := ctx.Err(); e != nil {
			return e
		}
		if v.ChannelID != group.ChannelID {
			return fmt.Errorf("upstream detail channel mismatch")
		}
		if count >= SettlementDetailPartRows {
			if e := next(); e != nil {
				return e
			}
		}
		count++
		before, discount := ChargeOriginal(v.Charge)
		return sheet.Row([]xlsxwriter.Cell{{Value: time.Unix(v.CreatedUnix, 0).In(BusinessLocation).Format("2006-01-02 15:04:05")}, {Value: v.RequestID}, {Value: v.ChannelName}, numberCell(v.ChannelID, 3), {Value: v.ModelName}, numberCell(v.PromptTokens, 3), numberCell(v.CompletionTokens, 3), numberCell(v.CacheReadTokens, 3), numberCell(v.CacheWriteTokens, 3), numberCell(v.ImageInputTokens, 3), numberCell(v.ImageOutputTokens, 3), numberCell(v.AudioInputTokens, 3), numberCell(v.AudioOutputTokens, 3), {Value: UnitPriceLabel(v.Charge.UnitPrices, rate), Style: xlsxwriter.WrappedTextStyle}, originalAmountCell(DisplaySettlementAmount(before, rate)), {Value: DiscountLabel(discount)}, decimalCell(DisplaySettlementAmount(v.Charge.Total, rate))})
	})
	if err != nil {
		return err
	}
	if err = wb.Write(tmp); err != nil {
		return err
	}
	if err = tmp.Sync(); err != nil {
		return err
	}
	if err = tmp.Close(); err != nil {
		return err
	}
	if err = os.Rename(tmp.Name(), target); err != nil {
		return err
	}
	info, err := os.Stat(target)
	if err != nil {
		return err
	}
	digest, err := fileSHA256(target)
	if err != nil {
		return err
	}
	group.RelativePath, group.FileSize, group.SHA256, group.CreatedAt = filepath.ToSlash(relative), info.Size(), digest, time.Now().UTC()
	return g.Store.PutBillingChannelDailyFile(ctx, group)
}
