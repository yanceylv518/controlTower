package billing

import (
	"context"
	"crypto/sha256"
	"encoding/csv"
	"encoding/hex"
	"fmt"
	"io"
	"math/big"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"controltower/server/internal/xlsxwriter"
)

const DefaultBillingFileRoot = "data/billing-files"

type UserDailyFileGenerator struct {
	Store UserDailyFileStore
	Root  string
	Spool DetailPageSpool
}

func (g UserDailyFileGenerator) GenerateJobFiles(ctx context.Context, job Job) error {
	if g.Store == nil {
		return fmt.Errorf("billing file store unavailable")
	}
	if g.Spool != nil {
		return g.generateSpooledFiles(ctx, job)
	}
	root := g.Root
	if root == "" {
		root = DefaultBillingFileRoot
	}
	root, err := filepath.Abs(root)
	if err != nil {
		return err
	}
	groups, err := g.Store.ListBillingRequestDetailGroups(ctx, job.ID)
	if err != nil {
		return err
	}
	for _, group := range groups {
		if err = ctx.Err(); err != nil {
			return err
		}
		details, queryErr := g.Store.ListBillingRequestDetails(ctx, job.ID, group.BillDay, group.UserID)
		if queryErr != nil {
			return queryErr
		}
		relative := dailyFileRelativePath(job, group)
		target := filepath.Join(root, relative)
		if err = os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return err
		}
		if info, statErr := os.Stat(target); statErr == nil {
			digest, hashErr := fileSHA256(target)
			if hashErr != nil {
				return hashErr
			}
			group.JobID, group.InstanceID = job.ID, job.InstanceID
			group.RelativePath = filepath.ToSlash(relative)
			group.FileSize, group.SHA256, group.CreatedAt = info.Size(), digest, time.Now().UTC()
			if err = g.Store.PutBillingUserDailyFile(ctx, group); err != nil {
				return err
			}
			continue
		} else if !os.IsNotExist(statErr) {
			return statErr
		}
		tmp, createErr := os.CreateTemp(filepath.Dir(target), ".billing-*.xlsx")
		if createErr != nil {
			return createErr
		}
		tmpName := tmp.Name()
		ok := false
		defer func() {
			if !ok {
				_ = os.Remove(tmpName)
			}
		}()
		if err = WriteUserDailyWorkbook(tmp, job, group, details); err != nil {
			_ = tmp.Close()
			return err
		}
		if err = tmp.Sync(); err != nil {
			_ = tmp.Close()
			return err
		}
		if err = tmp.Close(); err != nil {
			return err
		}
		if err = os.Rename(tmpName, target); err != nil {
			return err
		}
		ok = true
		info, statErr := os.Stat(target)
		if statErr != nil {
			return statErr
		}
		digest, hashErr := fileSHA256(target)
		if hashErr != nil {
			return hashErr
		}
		group.JobID, group.InstanceID = job.ID, job.InstanceID
		group.RelativePath = filepath.ToSlash(relative)
		group.FileSize, group.SHA256, group.CreatedAt = info.Size(), digest, time.Now().UTC()
		if err = g.Store.PutBillingUserDailyFile(ctx, group); err != nil {
			return err
		}
	}
	if job.UserID == 0 {
		if err = g.generateChannelFiles(ctx, root, job); err != nil {
			return err
		}
	}
	return nil
}

func (g UserDailyFileGenerator) generateChannelFiles(ctx context.Context, root string, job Job) error {
	groups, err := g.Store.ListBillingChannelRequestDetailGroups(ctx, job.ID)
	if err != nil {
		return err
	}
	for _, group := range groups {
		if err = ctx.Err(); err != nil {
			return err
		}
		rows, queryErr := g.Store.ListBillingChannelRequestDetails(ctx, job.ID, group.BillDay, group.ChannelID)
		if queryErr != nil {
			return queryErr
		}
		anomalies, queryErr := g.Store.ListBillingChannelAnomalyDetails(ctx, job.ID, group.BillDay, group.ChannelID)
		if queryErr != nil {
			return queryErr
		}
		relative := channelDailyFileRelativePath(job, group)
		target := filepath.Join(root, relative)
		if err = os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return err
		}
		tmp, createErr := os.CreateTemp(filepath.Dir(target), ".billing-channel-*.csv")
		if createErr != nil {
			return createErr
		}
		_, _ = tmp.Write([]byte{0xef, 0xbb, 0xbf})
		writer := csv.NewWriter(tmp)
		_ = writer.Write([]string{"请求时间", "请求 ID", "用户", "令牌", "模型", "计费模式", "命中价格层级", "输入 Token", "输出 Token", "缓存读取 Token", "缓存写入 Token", "5m 缓存写入 Token", "1h 缓存写入 Token", "输入单价", "输出单价", "缓存读取单价", "缓存写入单价", "5m 缓存写入单价", "1h 缓存写入单价", "按次单价", "金额", "订单状态", "异常原因"})
		for _, row := range rows {
			_ = writer.Write([]string{time.Unix(row.CreatedUnix, 0).In(BusinessLocation).Format("2006-01-02 15:04:05"), row.RequestID, row.Username, row.TokenName, row.ModelName, billingModeLabel(row.Charge.Mode), row.Charge.MatchedTier, strconv.FormatInt(row.PromptTokens, 10), strconv.FormatInt(row.CompletionTokens, 10), strconv.FormatInt(row.CacheReadTokens, 10), strconv.FormatInt(row.CacheWriteTokens, 10), strconv.FormatInt(row.CacheWrite5mTokens, 10), strconv.FormatInt(row.CacheWrite1hTokens, 10), row.Charge.InputPrice, row.Charge.OutputPrice, row.Charge.CacheReadPrice, row.Charge.CacheWritePrice, row.Charge.CacheWrite5mPrice, row.Charge.CacheWrite1hPrice, row.Charge.PerRequestPrice, row.Charge.Total, "正常", ""})
		}
		for _, row := range anomalies {
			_ = writer.Write([]string{row.CreatedAt.In(BusinessLocation).Format("2006-01-02 15:04:05"), row.RequestID, row.Username, row.TokenName, row.ModelName, "异常订单", "未完成计价", strconv.FormatInt(row.PromptTokens.Int64, 10), strconv.FormatInt(row.CompletionTokens.Int64, 10), strconv.FormatInt(row.CacheTokens, 10), strconv.FormatInt(row.CacheWriteTokens, 10), strconv.FormatInt(row.CacheWrite5mTokens, 10), strconv.FormatInt(row.CacheWrite1hTokens, 10), row.InputPrice, row.OutputPrice, row.CachePrice, row.CacheWritePrice, "", "", "", row.ActualAmount, "异常订单", row.Reasons})
		}
		writer.Flush()
		if writer.Error() != nil {
			_ = tmp.Close()
			_ = os.Remove(tmp.Name())
			return writer.Error()
		}
		if err = tmp.Sync(); err != nil {
			_ = tmp.Close()
			_ = os.Remove(tmp.Name())
			return err
		}
		if err = tmp.Close(); err != nil {
			_ = os.Remove(tmp.Name())
			return err
		}
		_ = os.Remove(target)
		if err = os.Rename(tmp.Name(), target); err != nil {
			_ = os.Remove(tmp.Name())
			return err
		}
		info, statErr := os.Stat(target)
		if statErr != nil {
			return statErr
		}
		digest, hashErr := fileSHA256(target)
		if hashErr != nil {
			return hashErr
		}
		group.JobID, group.InstanceID, group.RelativePath, group.FileSize, group.SHA256, group.CreatedAt = job.ID, job.InstanceID, filepath.ToSlash(relative), info.Size(), digest, time.Now().UTC()
		if err = g.Store.PutBillingChannelDailyFile(ctx, group); err != nil {
			return err
		}
	}
	return nil
}

func channelDailyFileRelativePath(job Job, group ChannelDailyFile) string {
	siteHash := sha256.Sum256([]byte(job.InstanceID))
	day := group.BillDay.In(BusinessLocation)
	return filepath.Join(hex.EncodeToString(siteHash[:8]), day.Format("2006"), day.Format("01"), day.Format("02"), fmt.Sprintf("channel-%d-%s.csv", group.ChannelID, job.ID))
}

func dailyFileRelativePath(job Job, group UserDailyFile) string {
	siteHash := sha256.Sum256([]byte(job.InstanceID))
	day := group.BillDay.In(BusinessLocation)
	name := fmt.Sprintf("user-%d-%s.xlsx", group.UserID, job.ID)
	return filepath.Join(hex.EncodeToString(siteHash[:8]), day.Format("2006"), day.Format("01"), day.Format("02"), name)
}

func WriteUserDailyWorkbook(out io.Writer, job Job, group UserDailyFile, rows []RequestDetail) error {
	columns := workbookOptionalColumns(rows)
	return writeUserDailyWorkbook(out, job, group, columns, func(visit func(RequestDetail) error) error {
		for _, row := range rows {
			if err := visit(row); err != nil {
				return err
			}
		}
		return nil
	})
}

func writeUserDailyWorkbook(out io.Writer, job Job, group UserDailyFile, columns optionalWorkbookColumns, iterate func(func(RequestDetail) error) error) error {
	var err error
	widths := []float64{20, 28, 12, 18, 14, 18, 14, 16, 14, 14, 14}
	headers := []string{"时间", "请求 ID", "用户", "令牌", "渠道", "模型", "计费模式", "命中价格层级", "输入 Token", "输出 Token", "缓存读取 Token"}
	appendColumn := func(header string, show bool) {
		if show {
			headers = append(headers, header)
			widths = append(widths, 16)
		}
	}
	appendColumn("普通缓存写入 Token", columns.genericWrite)
	appendColumn("5m 写入 Token", columns.write5m)
	appendColumn("1h 写入 Token", columns.write1h)
	headers = append(headers, "输入单价", "输出单价", "缓存读取单价")
	widths = append(widths, 16, 16, 16)
	appendColumn("普通缓存写入单价", columns.genericWrite)
	appendColumn("5m 写入单价", columns.write5m)
	appendColumn("1h 写入单价", columns.write1h)
	appendColumn("按次单价", columns.perRequest)
	headers = append(headers, "总费用")
	widths = append(widths, 16)

	wb := xlsxwriter.New()
	day := group.BillDay.In(BusinessLocation).Format("2006-01-02")
	priceNote := "单价单位：金额/百万 Token"
	if columns.perRequest {
		priceNote += "；按次单价单位：金额/次"
	}
	headerCells := make([]xlsxwriter.Cell, len(headers))
	for i, value := range headers {
		headerCells[i] = xlsxwriter.Cell{Value: value, Style: 1}
	}
	part, dataRows := 0, 0
	var sheet *xlsxwriter.Sheet
	newSheet := func() error {
		part++
		name := "账单明细"
		if part > 1 {
			name = fmt.Sprintf("账单明细-%d", part)
		}
		var addErr error
		sheet, addErr = wb.AddSheet(name, widths)
		if addErr != nil {
			return addErr
		}
		if addErr = sheet.Row([]xlsxwriter.Cell{{Value: "用户每日账单明细", Style: 2}}); addErr != nil {
			return addErr
		}
		if addErr = sheet.Row([]xlsxwriter.Cell{{Value: "账单日期"}, {Value: day}, {Value: "用户 ID"}, {Value: strconv.FormatInt(group.UserID, 10)}, {Value: "站点"}, {Value: job.InstanceID}}); addErr != nil {
			return addErr
		}
		if addErr = sheet.Row([]xlsxwriter.Cell{{Value: "计价说明"}, {Value: priceNote}}); addErr != nil {
			return addErr
		}
		dataRows = 0
		return sheet.Row(headerCells)
	}
	if err := newSheet(); err != nil {
		return err
	}
	err = iterate(func(row RequestDetail) error {
		if dataRows >= 1_000_000 {
			if err := newSheet(); err != nil {
				return err
			}
		}
		writeTokens := row.CacheWriteTokens
		if row.CacheWrite5mTokens+row.CacheWrite1hTokens > 0 {
			writeTokens -= row.CacheWrite5mTokens + row.CacheWrite1hTokens
			if writeTokens < 0 {
				writeTokens = 0
			}
		}
		cells := []xlsxwriter.Cell{
			{Value: time.Unix(row.CreatedUnix, 0).In(BusinessLocation).Format("2006-01-02 15:04:05"), Style: 5},
			{Value: row.RequestID, Style: 5}, {Value: row.Username, Style: 5}, {Value: row.TokenName, Style: 5},
			{Value: channelLabel(row), Style: 5}, {Value: row.ModelName, Style: 5}, {Value: billingModeLabel(row.Charge.Mode), Style: 5}, {Value: row.Charge.MatchedTier, Style: 5},
			numberCell(row.PromptTokens, 3), numberCell(row.CompletionTokens, 3), numberCell(row.CacheReadTokens, 3),
		}
		if columns.genericWrite {
			cells = append(cells, numberCell(writeTokens, 3))
		}
		if columns.write5m {
			cells = append(cells, numberCell(row.CacheWrite5mTokens, 3))
		}
		if columns.write1h {
			cells = append(cells, numberCell(row.CacheWrite1hTokens, 3))
		}
		cells = append(cells, decimalCell(row.Charge.InputPrice), decimalCell(row.Charge.OutputPrice), decimalCell(row.Charge.CacheReadPrice))
		if columns.genericWrite {
			cells = append(cells, decimalCell(row.Charge.CacheWritePrice))
		}
		if columns.write5m {
			cells = append(cells, decimalCell(row.Charge.CacheWrite5mPrice))
		}
		if columns.write1h {
			cells = append(cells, decimalCell(row.Charge.CacheWrite1hPrice))
		}
		if columns.perRequest {
			cells = append(cells, decimalCell(row.Charge.PerRequestPrice))
		}
		cells = append(cells, decimalCell(row.Charge.Total))
		if err := sheet.Row(cells); err != nil {
			return err
		}
		dataRows++
		return nil
	})
	if err != nil {
		return err
	}
	return wb.Write(out)
}

type optionalWorkbookColumns struct {
	genericWrite bool
	write5m      bool
	write1h      bool
	perRequest   bool
}

func workbookOptionalColumns(rows []RequestDetail) optionalWorkbookColumns {
	var columns optionalWorkbookColumns
	for _, row := range rows {
		genericWriteTokens := row.CacheWriteTokens - row.CacheWrite5mTokens - row.CacheWrite1hTokens
		columns.genericWrite = columns.genericWrite || genericWriteTokens > 0 || decimalNonZero(row.Charge.CacheWritePrice)
		columns.write5m = columns.write5m || row.CacheWrite5mTokens > 0 || decimalNonZero(row.Charge.CacheWrite5mPrice)
		columns.write1h = columns.write1h || row.CacheWrite1hTokens > 0 || decimalNonZero(row.Charge.CacheWrite1hPrice)
		columns.perRequest = columns.perRequest || row.Charge.Mode == "per_request" || decimalNonZero(row.Charge.PerRequestPrice)
	}
	return columns
}

func decimalNonZero(value string) bool {
	number, ok := new(big.Rat).SetString(value)
	return ok && number.Sign() != 0
}

func numberCell(value int64, style int) xlsxwriter.Cell {
	return xlsxwriter.Cell{Value: strconv.FormatInt(value, 10), Number: true, Style: style}
}

func decimalCell(value string) xlsxwriter.Cell {
	if value == "" {
		value = "0"
	}
	return xlsxwriter.Cell{Value: value, Number: true, Style: 4}
}

func channelLabel(row RequestDetail) string {
	if row.ChannelName == "" {
		return strconv.FormatInt(row.ChannelID, 10)
	}
	return fmt.Sprintf("%s (#%d)", row.ChannelName, row.ChannelID)
}

func billingModeLabel(mode string) string {
	if mode == "tiered_expr" {
		return "表达式计费"
	}
	if mode == "per_request" {
		return "按次"
	}
	return "按 Token"
}

func fileSHA256(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err = io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}
