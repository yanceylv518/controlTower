package billing

import (
	"bufio"
	"compress/gzip"
	"context"
	"encoding/csv"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

type detailJSONWriter struct {
	file *os.File
	zip  *gzip.Writer
	buf  *bufio.Writer
}

func (w *detailJSONWriter) close() error {
	if err := w.buf.Flush(); err != nil {
		_ = w.file.Close()
		return err
	}
	if err := w.zip.Close(); err != nil {
		_ = w.file.Close()
		return err
	}
	if err := w.file.Sync(); err != nil {
		_ = w.file.Close()
		return err
	}
	return w.file.Close()
}

// generateSpooledFiles performs one sequential pass over immutable source
// pages and fans rows out to per-user/per-channel disk files. At most one
// page is resident in memory; normal request rows never return to MySQL.
func (g UserDailyFileGenerator) generateSpooledFiles(ctx context.Context, job Job) error {
	root := g.Root
	if root == "" {
		root = DefaultBillingFileRoot
	}
	root, err := filepath.Abs(root)
	if err != nil {
		return err
	}
	pages, err := g.Spool.OpenPages(ctx, job)
	if err != nil {
		return err
	}
	derived := filepath.Join(root, ".staging", job.ID)
	if err = os.RemoveAll(derived); err != nil {
		return err
	}
	if err = os.MkdirAll(derived, 0o755); err != nil {
		return err
	}
	writers := map[string]*detailJSONWriter{}
	closeWriters := func() error {
		var first error
		for path, writer := range writers {
			if closeErr := writer.close(); first == nil && closeErr != nil {
				first = closeErr
			}
			delete(writers, path)
		}
		return first
	}
	defer closeWriters()
	write := func(path string, row RequestDetail) error {
		writer := writers[path]
		if writer == nil {
			if len(writers) >= 64 {
				for oldPath, old := range writers {
					if err := old.close(); err != nil {
						return err
					}
					delete(writers, oldPath)
					break
				}
			}
			if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
				return err
			}
			file, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
			if err != nil {
				return err
			}
			zipper, zipErr := gzip.NewWriterLevel(file, gzip.BestSpeed)
			if zipErr != nil {
				_ = file.Close()
				return zipErr
			}
			writer = &detailJSONWriter{file: file, zip: zipper, buf: bufio.NewWriterSize(zipper, 128*1024)}
			writers[path] = writer
		}
		payload, err := json.Marshal(row)
		if err != nil {
			return err
		}
		if _, err = writer.buf.Write(payload); err == nil {
			err = writer.buf.WriteByte('\n')
		}
		return err
	}
	for _, page := range pages {
		if err = page.Read(func(row RequestDetail) error {
			day := row.BillDay.In(BusinessLocation).Format("2006-01-02")
			userPath := filepath.Join(derived, "users", day, strconv.FormatInt(row.UserID, 10)+".jsonl.gz")
			if err := write(userPath, row); err != nil {
				return err
			}
			if job.UserID == 0 {
				channelPath := filepath.Join(derived, "channels", day, strconv.FormatInt(row.ChannelID, 10)+".jsonl.gz")
				return write(channelPath, row)
			}
			return nil
		}); err != nil {
			return err
		}
	}
	// A task may have been running during an upgrade from the legacy request
	// ledger. Merge only those already-committed legacy rows into the same disk
	// fan-out so deployment does not produce a truncated bill.
	legacyGroups, err := g.Store.ListBillingRequestDetailGroups(ctx, job.ID)
	if err != nil {
		return err
	}
	for _, group := range legacyGroups {
		legacyRows, queryErr := g.Store.ListBillingRequestDetails(ctx, job.ID, group.BillDay, group.UserID)
		if queryErr != nil {
			return queryErr
		}
		for _, row := range legacyRows {
			day := row.BillDay.In(BusinessLocation).Format("2006-01-02")
			if err = write(filepath.Join(derived, "users", day, strconv.FormatInt(row.UserID, 10)+".jsonl.gz"), row); err != nil {
				return err
			}
			if job.UserID == 0 {
				if err = write(filepath.Join(derived, "channels", day, strconv.FormatInt(row.ChannelID, 10)+".jsonl.gz"), row); err != nil {
					return err
				}
			}
		}
	}
	if err = closeWriters(); err != nil {
		return err
	}
	if job.UserID == 0 {
		anomalyGroups, groupErr := g.Store.ListBillingChannelRequestDetailGroups(ctx, job.ID)
		if groupErr != nil {
			return groupErr
		}
		for _, group := range anomalyGroups {
			path := filepath.Join(derived, "channels", group.BillDay.In(BusinessLocation).Format("2006-01-02"), strconv.FormatInt(group.ChannelID, 10)+".jsonl.gz")
			if err = os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
				return err
			}
			if _, statErr := os.Stat(path); os.IsNotExist(statErr) {
				file, createErr := os.Create(path)
				if createErr != nil {
					return createErr
				}
				zipper, createErr := gzip.NewWriterLevel(file, gzip.BestSpeed)
				if createErr == nil {
					createErr = zipper.Close()
				}
				if closeErr := file.Close(); createErr == nil {
					createErr = closeErr
				}
				if createErr != nil {
					return createErr
				}
			}
		}
	}
	if err = g.publishSpooledUsers(ctx, root, derived, job); err != nil {
		return err
	}
	if job.UserID == 0 {
		if err = g.publishSpooledChannels(ctx, root, derived, job); err != nil {
			return err
		}
	}
	return os.RemoveAll(derived)
}

func visitJSONDetails(path string, visit func(RequestDetail) error) error {
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	defer file.Close()
	zipper, err := gzip.NewReader(file)
	if err != nil {
		return err
	}
	defer zipper.Close()
	scanner := bufio.NewScanner(zipper)
	scanner.Buffer(make([]byte, 64*1024), 2*1024*1024)
	for scanner.Scan() {
		var row RequestDetail
		if err = json.Unmarshal(scanner.Bytes(), &row); err != nil {
			return err
		}
		if err = visit(row); err != nil {
			return err
		}
	}
	return scanner.Err()
}

func (g UserDailyFileGenerator) publishSpooledUsers(ctx context.Context, root, derived string, job Job) error {
	paths, err := filepath.Glob(filepath.Join(derived, "users", "*", "*.jsonl.gz"))
	if err != nil {
		return err
	}
	sort.Strings(paths)
	for _, path := range paths {
		if err = ctx.Err(); err != nil {
			return err
		}
		day, err := time.ParseInLocation("2006-01-02", filepath.Base(filepath.Dir(path)), BusinessLocation)
		if err != nil {
			return err
		}
		userID, err := strconv.ParseInt(strings.TrimSuffix(filepath.Base(path), ".jsonl.gz"), 10, 64)
		if err != nil {
			return err
		}
		group := UserDailyFile{JobID: job.ID, InstanceID: job.InstanceID, BillDay: day, UserID: userID}
		relative := dailyFileRelativePath(job, group)
		target := filepath.Join(root, relative)
		if err = os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return err
		}
		tmp, err := os.CreateTemp(filepath.Dir(target), ".billing-*.xlsx")
		if err != nil {
			return err
		}
		var columns optionalWorkbookColumns
		if err = visitJSONDetails(path, func(row RequestDetail) error {
			generic := row.CacheWriteTokens - row.CacheWrite5mTokens - row.CacheWrite1hTokens
			columns.genericWrite = columns.genericWrite || generic > 0 || decimalNonZero(row.Charge.CacheWritePrice)
			columns.write5m = columns.write5m || row.CacheWrite5mTokens > 0 || decimalNonZero(row.Charge.CacheWrite5mPrice)
			columns.write1h = columns.write1h || row.CacheWrite1hTokens > 0 || decimalNonZero(row.Charge.CacheWrite1hPrice)
			columns.perRequest = columns.perRequest || row.Charge.Mode == "per_request" || decimalNonZero(row.Charge.PerRequestPrice)
			return nil
		}); err != nil {
			_ = tmp.Close()
			_ = os.Remove(tmp.Name())
			return err
		}
		if err = writeUserDailyWorkbook(tmp, job, group, columns, func(visit func(RequestDetail) error) error { return visitJSONDetails(path, visit) }); err == nil {
			err = tmp.Sync()
		}
		if closeErr := tmp.Close(); err == nil {
			err = closeErr
		}
		if err != nil {
			_ = os.Remove(tmp.Name())
			return err
		}
		if err = os.Rename(tmp.Name(), target); err != nil {
			_ = os.Remove(tmp.Name())
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
		if err = g.Store.PutBillingUserDailyFile(ctx, group); err != nil {
			return err
		}
	}
	return nil
}

func (g UserDailyFileGenerator) publishSpooledChannels(ctx context.Context, root, derived string, job Job) error {
	paths, err := filepath.Glob(filepath.Join(derived, "channels", "*", "*.jsonl.gz"))
	if err != nil {
		return err
	}
	sort.Strings(paths)
	for _, path := range paths {
		if err = ctx.Err(); err != nil {
			return err
		}
		day, err := time.ParseInLocation("2006-01-02", filepath.Base(filepath.Dir(path)), BusinessLocation)
		if err != nil {
			return err
		}
		channelID, err := strconv.ParseInt(strings.TrimSuffix(filepath.Base(path), ".jsonl.gz"), 10, 64)
		if err != nil {
			return err
		}
		anomalies, err := g.Store.ListBillingChannelAnomalyDetails(ctx, job.ID, day, channelID)
		if err != nil {
			return err
		}
		group := ChannelDailyFile{JobID: job.ID, InstanceID: job.InstanceID, BillDay: day, ChannelID: channelID}
		relative := channelDailyFileRelativePath(job, group)
		target := filepath.Join(root, relative)
		if err = os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return err
		}
		tmp, err := os.CreateTemp(filepath.Dir(target), ".billing-channel-*.csv")
		if err != nil {
			return err
		}
		_, _ = tmp.Write([]byte{0xef, 0xbb, 0xbf})
		writer := csv.NewWriter(tmp)
		_ = writer.Write([]string{"请求时间", "Request ID", "上游 Request ID", "渠道", "模型", "计费模式", "命中价格层级", "输入 Token", "输出 Token", "缓存读取 Token", "缓存写入 Token", "5m 缓存写入 Token", "1h 缓存写入 Token", "输入单价", "输出单价", "缓存读取单价", "缓存写入单价", "5m 缓存写入单价", "1h 缓存写入单价", "按次单价", "金额", "订单状态", "异常原因"})
		if err = visitJSONDetails(path, func(row RequestDetail) error {
			return writer.Write([]string{time.Unix(row.CreatedUnix, 0).In(BusinessLocation).Format("2006-01-02 15:04:05"), row.RequestID, row.UpstreamRequestID, row.ChannelName, row.ModelName, billingModeLabel(row.Charge.Mode), row.Charge.MatchedTier, strconv.FormatInt(row.PromptTokens, 10), strconv.FormatInt(row.CompletionTokens, 10), strconv.FormatInt(row.CacheReadTokens, 10), strconv.FormatInt(row.CacheWriteTokens, 10), strconv.FormatInt(row.CacheWrite5mTokens, 10), strconv.FormatInt(row.CacheWrite1hTokens, 10), row.Charge.InputPrice, row.Charge.OutputPrice, row.Charge.CacheReadPrice, row.Charge.CacheWritePrice, row.Charge.CacheWrite5mPrice, row.Charge.CacheWrite1hPrice, row.Charge.PerRequestPrice, row.Charge.Total, "正常", ""})
		}); err != nil {
			_ = tmp.Close()
			_ = os.Remove(tmp.Name())
			return err
		}
		for _, row := range anomalies {
			_ = writer.Write([]string{row.CreatedAt.In(BusinessLocation).Format("2006-01-02 15:04:05"), row.RequestID, row.UpstreamRequestID, row.ChannelName, row.ModelName, "异常订单", "未完成计价", strconv.FormatInt(row.PromptTokens.Int64, 10), strconv.FormatInt(row.CompletionTokens.Int64, 10), strconv.FormatInt(row.CacheTokens, 10), strconv.FormatInt(row.CacheWriteTokens, 10), strconv.FormatInt(row.CacheWrite5mTokens, 10), strconv.FormatInt(row.CacheWrite1hTokens, 10), row.InputPrice, row.OutputPrice, row.CachePrice, row.CacheWritePrice, "", "", "", row.ActualAmount, "异常订单", row.Reasons})
		}
		writer.Flush()
		if err = writer.Error(); err == nil {
			err = tmp.Sync()
		}
		if closeErr := tmp.Close(); err == nil {
			err = closeErr
		}
		if err != nil {
			_ = os.Remove(tmp.Name())
			return err
		}
		if err = os.Rename(tmp.Name(), target); err != nil {
			_ = os.Remove(tmp.Name())
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
		if err = g.Store.PutBillingChannelDailyFile(ctx, group); err != nil {
			return err
		}
	}
	return nil
}
