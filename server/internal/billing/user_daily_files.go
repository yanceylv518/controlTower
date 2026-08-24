package billing

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
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
}

func (g UserDailyFileGenerator) GenerateJobFiles(ctx context.Context, job Job) error {
	if g.Store == nil {
		return fmt.Errorf("billing file store unavailable")
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
		if err = writeUserDailyWorkbook(tmp, job, group, details); err != nil {
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
	return nil
}

func dailyFileRelativePath(job Job, group UserDailyFile) string {
	siteHash := sha256.Sum256([]byte(job.InstanceID))
	day := group.BillDay.In(BusinessLocation)
	name := fmt.Sprintf("user-%d-%s.xlsx", group.UserID, job.ID)
	return filepath.Join(hex.EncodeToString(siteHash[:8]), day.Format("2006"), day.Format("01"), day.Format("02"), name)
}

func writeUserDailyWorkbook(out io.Writer, job Job, group UserDailyFile, rows []RequestDetail) error {
	wb := xlsxwriter.New()
	sheet, err := wb.AddSheet("账单明细", []float64{20, 28, 12, 18, 14, 18, 14, 16, 14, 14, 14, 14, 14, 14, 16, 16, 16, 16, 16, 16, 16, 16})
	if err != nil {
		return err
	}
	day := group.BillDay.In(BusinessLocation).Format("2006-01-02")
	_ = sheet.Row([]xlsxwriter.Cell{{Value: "用户每日账单明细", Style: 2}})
	_ = sheet.Row([]xlsxwriter.Cell{{Value: "账单日期"}, {Value: day}, {Value: "用户 ID"}, {Value: strconv.FormatInt(group.UserID, 10)}, {Value: "站点"}, {Value: job.InstanceID}})
	_ = sheet.Row([]xlsxwriter.Cell{{Value: "计价说明"}, {Value: "单价单位：金额/百万 Token；按次计费除外"}})
	headers := []string{"时间", "请求 ID", "用户", "令牌", "渠道", "模型", "计费模式", "命中价格层级", "输入 Token", "输出 Token", "缓存读取 Token", "缓存写入 Token", "5m 写入 Token", "1h 写入 Token", "输入单价", "输出单价", "缓存读取单价", "缓存写入单价", "5m 写入单价", "1h 写入单价", "按次单价", "总费用"}
	headerCells := make([]xlsxwriter.Cell, len(headers))
	for i, value := range headers {
		headerCells[i] = xlsxwriter.Cell{Value: value, Style: 1}
	}
	_ = sheet.Row(headerCells)
	for _, row := range rows {
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
			numberCell(row.PromptTokens, 3), numberCell(row.CompletionTokens, 3), numberCell(row.CacheReadTokens, 3), numberCell(writeTokens, 3), numberCell(row.CacheWrite5mTokens, 3), numberCell(row.CacheWrite1hTokens, 3),
			decimalCell(row.Charge.InputPrice), decimalCell(row.Charge.OutputPrice), decimalCell(row.Charge.CacheReadPrice), decimalCell(row.Charge.CacheWritePrice), decimalCell(row.Charge.CacheWrite5mPrice), decimalCell(row.Charge.CacheWrite1hPrice), decimalCell(row.Charge.PerRequestPrice), decimalCell(row.Charge.Total),
		}
		if err = sheet.Row(cells); err != nil {
			return err
		}
	}
	return wb.Write(out)
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
