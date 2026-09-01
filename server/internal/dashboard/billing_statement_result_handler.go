package dashboard

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/csv"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"controltower/server/internal/billing"
	"controltower/server/internal/xlsxwriter"
)

type BillingStatementResultStore interface {
	BillingJob(context.Context, string) (billing.Job, error)
	QueryBillingStatementAggregates(context.Context, string) ([]billing.StatementAggregateRow, error)
	ListBillingStatementDiscounts(context.Context, string) ([]billing.StatementDiscount, error)
	QueryBillingTokenRows(context.Context, string, int64, int64, time.Time, time.Time) ([]billing.TokenDailyRow, error)
	ListBillingStatementUserFiles(context.Context, string) ([]billing.UserDailyFile, error)
	ListBillingPrices(context.Context, string) ([]billing.PriceRecord, error)
	QueryBillingAnomalies(context.Context, string, string, int64, int64, time.Time, time.Time, time.Time, int64, int) ([]billing.AnomalyOrder, error)
	QueryBillingStatementReconciliation(context.Context, string, int, int) ([]billing.ReconciliationOrder, error)
	DeleteBillingStatement(context.Context, string) ([]string, error)
}

type BillingStatementResultHandler struct {
	Store BillingStatementResultStore
	Root  string
}

func (h BillingStatementResultHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimSpace(r.URL.Query().Get("id"))
	if r.Method == http.MethodDelete {
		paths, err := h.Store.DeleteBillingStatement(r.Context(), id)
		if err != nil {
			writeDashboardError(w, http.StatusNotFound, "billing_statement_not_found")
			return
		}
		root := h.Root
		if root == "" {
			root = billing.DefaultBillingFileRoot
		}
		root, _ = filepath.Abs(root)
		for _, relative := range paths {
			full := filepath.Join(root, filepath.FromSlash(relative))
			if rel, relErr := filepath.Rel(root, full); relErr == nil && rel != "." && !strings.HasPrefix(rel, "..") {
				_ = os.Remove(full)
			}
		}
		writeDashboardJSON(w, http.StatusOK, map[string]any{"deleted": true, "id": id})
		return
	}
	job, err := h.Store.BillingJob(r.Context(), id)
	if err != nil || job.Status != "complete" || (job.JobType != "user_statement" && job.JobType != "upstream_statement") {
		writeDashboardError(w, http.StatusNotFound, "billing_statement_not_found")
		return
	}
	rows, err := h.Store.QueryBillingStatementAggregates(r.Context(), job.ID)
	if err != nil {
		writeDashboardError(w, http.StatusInternalServerError, "billing_statement_query_failed")
		return
	}
	if r.URL.Query().Get("export") == "reconciliation" {
		h.writeReconciliationCSV(w, r, job)
		return
	}
	files, err := h.Store.ListBillingStatementUserFiles(r.Context(), job.ID)
	if err != nil {
		writeDashboardError(w, http.StatusInternalServerError, "billing_statement_files_failed")
		return
	}
	if r.URL.Query().Get("download") != "1" {
		preview, previewErr := statementPreview(r.Context(), job, rows, h.Store)
		if previewErr != nil {
			writeDashboardError(w, http.StatusInternalServerError, "billing_statement_preview_failed")
			return
		}
		billable := sumStatementRequests(rows)
		verified := billable - job.MismatchRows
		if verified < 0 {
			verified = 0
		}
		writeDashboardJSON(w, http.StatusOK, map[string]any{"job": job, "total_orders": billable + job.AbnormalRows, "normal_orders": verified, "billable_orders": billable, "anomaly_total": job.AbnormalRows, "reconciliation_total": job.MismatchRows, "review_required": job.MismatchRows > 0, "count_balanced": verified+job.AbnormalRows+job.MismatchRows == billable+job.AbnormalRows, "model_summary": preview.Models, "daily_summary": preview.Daily, "token_summary": preview.Tokens, "anomalies": preview.Anomalies, "reconciliation": preview.Reconciliation})
		return
	}
	book, err := statementWorkbook(job, rows, h.Store)
	if err != nil {
		writeDashboardError(w, http.StatusInternalServerError, "billing_statement_xlsx_failed")
		return
	}
	w.Header().Set("Content-Type", "application/zip")
	archiveName := statementArchiveFilename(job)
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="billing-statement-%s.zip"; filename*=UTF-8''%s`, job.ID, url.PathEscape(archiveName)))
	z := zip.NewWriter(w)
	main, _ := z.Create("账单.xlsx")
	_, _ = main.Write(book)
	if job.MismatchRows > 0 {
		var reconciliation bytes.Buffer
		if err := h.writeReconciliationCSVData(r.Context(), &reconciliation, job); err == nil {
			entry, createErr := z.Create("核对差异.csv")
			if createErr == nil {
				_, _ = entry.Write(reconciliation.Bytes())
			}
		}
	}
	root := h.Root
	if root == "" {
		root = billing.DefaultBillingFileRoot
	}
	root, _ = filepath.Abs(root)
	for _, file := range files {
		full := filepath.Join(root, filepath.FromSlash(file.RelativePath))
		if rel, relErr := filepath.Rel(root, full); relErr != nil || strings.HasPrefix(rel, "..") {
			continue
		}
		in, openErr := os.Open(full)
		if openErr != nil {
			continue
		}
		entry, createErr := z.Create("日明细/" + statementDailyFilename(job, file.BillDay))
		if createErr == nil {
			_, _ = io.Copy(entry, in)
		}
		_ = in.Close()
	}
	_ = z.Close()
}

type statementPreviewData struct {
	Models         []map[string]any
	Daily          []map[string]any
	Tokens         []map[string]any
	Anomalies      []map[string]any
	Reconciliation []map[string]any
}

type statementGroupedRow struct {
	Key      string
	Row      billing.StatementAggregateRow
	Discount string
}

func statementSummaryKey(job billing.Job, row billing.StatementAggregateRow, discount string) string {
	if job.JobType == "upstream_statement" {
		return fmt.Sprintf("%d|%s|%s", row.ChannelID, row.ModelName, discount)
	}
	return row.ModelName
}

func groupStatementRows(job billing.Job, rows []billing.StatementAggregateRow, discounts []billing.StatementDiscount, daily bool) []statementGroupedRow {
	grouped := map[string]statementGroupedRow{}
	for _, row := range rows {
		discount := "1.000000"
		if job.JobType == "upstream_statement" {
			discount = billing.DiscountForDay(discounts, job.JobType, row.ChannelID, row.ModelName, row.Day)
		}
		key := statementSummaryKey(job, row, discount)
		if daily {
			key = row.Day.In(billing.BusinessLocation).Format("2006-01-02") + "|" + key
		}
		item := grouped[key]
		item.Key, item.Discount = statementSummaryKey(job, row, discount), discount
		item.Row.ModelName = row.ModelName
		item.Row.Day = row.Day
		if job.JobType == "upstream_statement" {
			item.Row.ChannelID, item.Row.ChannelName = row.ChannelID, row.ChannelName
		}
		item.Row.RequestCount += row.RequestCount
		item.Row.PromptTokens += row.PromptTokens
		item.Row.CompletionTokens += row.CompletionTokens
		item.Row.CacheTokens += row.CacheTokens
		item.Row.CacheWriteTokens += row.CacheWriteTokens
		item.Row.Amount = addDecimal(item.Row.Amount, row.Amount)
		grouped[key] = item
	}
	keys := make([]string, 0, len(grouped))
	for key := range grouped {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	out := make([]statementGroupedRow, 0, len(keys))
	for _, key := range keys {
		out = append(out, grouped[key])
	}
	return out
}

func statementPreview(ctx context.Context, job billing.Job, rows []billing.StatementAggregateRow, store BillingStatementResultStore) (statementPreviewData, error) {
	prices, err := store.ListBillingPrices(ctx, job.InstanceID)
	if err != nil {
		return statementPreviewData{}, err
	}
	discounts := []billing.StatementDiscount{}
	if job.JobType == "upstream_statement" {
		discounts, err = store.ListBillingStatementDiscounts(ctx, job.ID)
		if err != nil {
			return statementPreviewData{}, err
		}
	}
	out := statementPreviewData{Models: []map[string]any{}, Daily: []map[string]any{}, Tokens: []map[string]any{}, Anomalies: []map[string]any{}, Reconciliation: []map[string]any{}}
	for _, grouped := range groupStatementRows(job, rows, discounts, false) {
		v, discount := grouped.Row, grouped.Discount
		out.Models = append(out.Models, map[string]any{"channel_id": v.ChannelID, "channel_name": v.ChannelName, "model_name": statementModelLabel(job, v.ChannelName, v.ModelName), "request_count": v.RequestCount, "prompt_tokens": v.PromptTokens, "completion_tokens": v.CompletionTokens, "cache_read_tokens": v.CacheTokens, "cache_write_tokens": v.CacheWriteTokens, "amount": v.Amount, "discount": discount, "final_amount": multiplyDecimal(v.Amount, discount)})
	}
	for _, grouped := range groupStatementRows(job, rows, discounts, true) {
		v, discount := grouped.Row, grouped.Discount
		price := statementPrice(prices, v.ModelName, v.Day)
		out.Daily = append(out.Daily, map[string]any{"day": v.Day.Format("2006-01-02"), "channel_id": v.ChannelID, "channel_name": v.ChannelName, "model_name": statementModelLabel(job, v.ChannelName, v.ModelName), "request_count": v.RequestCount, "prompt_tokens": v.PromptTokens, "completion_tokens": v.CompletionTokens, "cache_read_tokens": v.CacheTokens, "cache_write_tokens": v.CacheWriteTokens, "input_price": price.Input, "output_price": price.Output, "cache_read_price": price.Cache, "cache_write_price": price.CacheWrite, "amount": v.Amount, "discount": discount, "final_amount": multiplyDecimal(v.Amount, discount), "detail_file": statementDailyFilename(job, v.Day)})
	}
	if job.JobType == "user_statement" {
		tokens, tokenErr := store.QueryBillingTokenRows(ctx, job.ID, job.UserID, -1, job.From, job.To)
		if tokenErr != nil {
			return statementPreviewData{}, tokenErr
		}
		for _, v := range tokens {
			out.Tokens = append(out.Tokens, map[string]any{"token_id": v.TokenID, "token_name": v.TokenName, "day": v.Day.Format("2006-01-02"), "model_name": v.ModelName, "request_count": v.RequestCount, "prompt_tokens": v.PromptTokens, "completion_tokens": v.CompletionTokens, "cache_read_tokens": v.CacheTokens, "cache_write_tokens": v.CacheWriteTokens, "amount": v.Amount, "discount": "1.000000", "final_amount": v.Amount})
		}
	}
	userID := int64(0)
	if job.JobType == "user_statement" {
		userID = job.UserID
	}
	anomalies, anomalyErr := store.QueryBillingAnomalies(ctx, job.InstanceID, job.ID, userID, 0, job.From, job.To, time.Unix(0, 0), 0, 500)
	if anomalyErr != nil {
		return statementPreviewData{}, anomalyErr
	}
	for _, v := range anomalies {
		item := map[string]any{"created_at": v.CreatedAt.In(billing.BusinessLocation).Format("2006-01-02 15:04:05"), "request_id": v.RequestID, "upstream_request_id": v.UpstreamRequestID, "model_name": v.ModelName, "prompt_tokens": nullInt(v.PromptTokens), "completion_tokens": nullInt(v.CompletionTokens), "cache_read_tokens": v.CacheTokens, "cache_write_tokens": v.CacheWriteTokens, "actual_amount": v.ActualAmount, "reasons": localizedReasons(v.Reasons)}
		if job.JobType == "upstream_statement" {
			item["channel_id"], item["channel_name"] = v.ChannelID, v.ChannelName
		} else {
			item["token_id"], item["token_name"] = v.TokenID, v.TokenName
		}
		out.Anomalies = append(out.Anomalies, item)
	}
	mismatches, mismatchErr := store.QueryBillingStatementReconciliation(ctx, job.ID, 500, 0)
	if mismatchErr != nil {
		return statementPreviewData{}, mismatchErr
	}
	for _, v := range mismatches {
		item := map[string]any{"created_at": time.Unix(v.CreatedUnix, 0).In(billing.BusinessLocation).Format("2006-01-02 15:04:05"), "request_id": v.RequestID, "upstream_request_id": v.UpstreamRequestID, "model_name": v.ModelName, "prompt_tokens": v.PromptTokens, "completion_tokens": v.CompletionTokens, "cache_read_tokens": v.CacheReadTokens, "cache_write_tokens": v.CacheWriteTokens, "calculated_quota": v.CalculatedQuota, "logged_quota": v.LoggedQuota, "quota_difference": v.QuotaDifference, "calculated_amount": v.CalculatedAmount, "logged_amount": v.LoggedAmount, "amount_difference": v.AmountDifference, "reason": localizedReasons(v.Reason)}
		if job.JobType == "upstream_statement" {
			item["channel_id"], item["channel_name"] = v.ChannelID, v.ChannelName
		} else {
			item["token_id"], item["token_name"] = v.TokenID, v.TokenName
		}
		out.Reconciliation = append(out.Reconciliation, item)
	}
	return out, nil
}

func (h BillingStatementResultHandler) writeReconciliationCSV(w http.ResponseWriter, r *http.Request, job billing.Job) {
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	name := strings.TrimSuffix(statementArchiveFilename(job), ".zip") + "-核对差异.csv"
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="billing-reconciliation-%s.csv"; filename*=UTF-8''%s`, job.ID, url.PathEscape(name)))
	_ = h.writeReconciliationCSVData(r.Context(), w, job)
}

func (h BillingStatementResultHandler) writeReconciliationCSVData(ctx context.Context, w io.Writer, job billing.Job) error {
	_, _ = w.Write([]byte{0xef, 0xbb, 0xbf})
	cw := csv.NewWriter(w)
	upstreamStatement := job.JobType == "upstream_statement"
	if upstreamStatement {
		_ = cw.Write([]string{"请求时间", "Request ID", "上游 Request ID", "渠道 ID", "渠道名称", "模型", "输入 Token", "输出 Token", "缓存读取 Token", "缓存写入 Token", "重算 Quota", "日志 Quota", "Quota 差额", "重算金额", "日志金额", "金额差额", "差异原因"})
	} else {
		_ = cw.Write([]string{"请求时间", "Request ID", "令牌 ID", "令牌", "模型", "输入 Token", "输出 Token", "缓存读取 Token", "缓存写入 Token", "重算 Quota", "日志 Quota", "Quota 差额", "重算金额", "日志金额", "金额差额", "差异原因"})
	}
	for offset := 0; ; offset += 5000 {
		items, err := h.Store.QueryBillingStatementReconciliation(ctx, job.ID, 5000, offset)
		if err != nil {
			return err
		}
		for _, v := range items {
			usage := []string{v.ModelName, strconv.FormatInt(v.PromptTokens, 10), strconv.FormatInt(v.CompletionTokens, 10), strconv.FormatInt(v.CacheReadTokens, 10), strconv.FormatInt(v.CacheWriteTokens, 10), strconv.FormatInt(v.CalculatedQuota, 10), strconv.FormatInt(v.LoggedQuota, 10), strconv.FormatInt(v.QuotaDifference, 10), v.CalculatedAmount, v.LoggedAmount, v.AmountDifference, localizedReasons(v.Reason)}
			if upstreamStatement {
				_ = cw.Write(append([]string{time.Unix(v.CreatedUnix, 0).In(billing.BusinessLocation).Format("2006-01-02 15:04:05"), v.RequestID, v.UpstreamRequestID, strconv.FormatInt(v.ChannelID, 10), v.ChannelName}, usage...))
			} else {
				_ = cw.Write(append([]string{time.Unix(v.CreatedUnix, 0).In(billing.BusinessLocation).Format("2006-01-02 15:04:05"), v.RequestID, strconv.FormatInt(v.TokenID, 10), v.TokenName}, usage...))
			}
		}
		if len(items) < 5000 {
			break
		}
	}
	cw.Flush()
	return cw.Error()
}

func sumStatementRequests(rows []billing.StatementAggregateRow) int64 {
	var total int64
	for _, row := range rows {
		total += row.RequestCount
	}
	return total
}

func statementWorkbook(job billing.Job, rows []billing.StatementAggregateRow, store BillingStatementResultStore) ([]byte, error) {
	book := xlsxwriter.New()
	prices, err := store.ListBillingPrices(context.Background(), job.InstanceID)
	if err != nil {
		return nil, err
	}
	discounts := []billing.StatementDiscount{}
	if job.JobType == "upstream_statement" {
		discounts, err = store.ListBillingStatementDiscounts(context.Background(), job.ID)
		if err != nil {
			return nil, err
		}
	}
	modelRows := groupStatementRows(job, rows, discounts, false)
	widths := []float64{28, 14, 18, 18, 18, 18, 16, 12, 16}
	headers := []xlsxwriter.Cell{t("模型"), t("订单数"), t("输入 Token"), t("输出 Token"), t("缓存读取 Token"), t("缓存写入 Token"), t("总费用"), t("折扣"), t("最终费用")}
	if job.JobType == "upstream_statement" {
		widths = append([]float64{24}, widths...)
		headers = append([]xlsxwriter.Cell{t("渠道")}, headers...)
	}
	s, err := book.AddSheet("区间统计", widths)
	if err != nil {
		return nil, err
	}
	_ = s.Row(headers)
	summaryRefs := map[string]string{}
	for index, grouped := range modelRows {
		v, discount := grouped.Row, grouped.Discount
		cells := []xlsxwriter.Cell{t(v.ModelName), n64(v.RequestCount), n64(v.PromptTokens), n64(v.CompletionTokens), n64(v.CacheTokens), n64(v.CacheWriteTokens), d(v.Amount), d(discount), d(multiplyDecimal(v.Amount, discount))}
		if job.JobType == "upstream_statement" {
			cells = append([]xlsxwriter.Cell{t(v.ChannelName)}, cells...)
		}
		_ = s.Row(cells)
		column := "H"
		if job.JobType == "upstream_statement" {
			column = "I"
		}
		summaryRefs[grouped.Key] = fmt.Sprintf("'区间统计'!%s%d", column, index+2)
	}
	dailyWidths := []float64{14, 28, 14, 18, 18, 18, 18, 16, 16, 16, 16, 16, 12, 16, 34}
	dailyHeaders := []xlsxwriter.Cell{t("日期"), t("模型"), t("订单数"), t("输入 Token"), t("输出 Token"), t("缓存读取 Token"), t("缓存写入 Token"), t("输入单价"), t("输出单价"), t("缓存读取单价"), t("缓存写入单价"), t("总费用"), t("折扣"), t("最终费用"), t("明细文件")}
	if job.JobType == "upstream_statement" {
		dailyWidths = append([]float64{24}, dailyWidths...)
		dailyHeaders = append([]xlsxwriter.Cell{t("渠道")}, dailyHeaders...)
	}
	daily, err := book.AddSheet("日账单统计", dailyWidths)
	if err != nil {
		return nil, err
	}
	_ = daily.Row(dailyHeaders)
	dailyTotal := "0"
	for _, grouped := range groupStatementRows(job, rows, discounts, true) {
		v, discount := grouped.Row, grouped.Discount
		price := statementPrice(prices, v.ModelName, v.Day)
		final := multiplyDecimal(v.Amount, discount)
		key := statementSummaryKey(job, v, discount)
		discountCell := d(discount)
		discountCell.Formula = summaryRefs[key]
		cells := []xlsxwriter.Cell{t(v.Day.Format("2006-01-02")), t(v.ModelName), n64(v.RequestCount), n64(v.PromptTokens), n64(v.CompletionTokens), n64(v.CacheTokens), n64(v.CacheWriteTokens), d(price.Input), d(price.Output), d(price.Cache), d(price.CacheWrite), d(v.Amount), discountCell, d(final), t(statementDailyFilename(job, v.Day))}
		if job.JobType == "upstream_statement" {
			cells = append([]xlsxwriter.Cell{t(v.ChannelName)}, cells...)
		}
		_ = daily.Row(cells)
		dailyTotal = addDecimal(dailyTotal, final)
	}
	_ = daily.Row([]xlsxwriter.Cell{t("合计"), t(""), t(""), t(""), t(""), t(""), t(""), t(""), t(""), t(""), t(""), t(""), t(""), d(dailyTotal), t("")})
	if job.JobType == "user_statement" {
		tokens, tokenErr := store.QueryBillingTokenRows(context.Background(), job.ID, job.UserID, -1, job.From, job.To)
		if tokenErr != nil {
			return nil, tokenErr
		}
		ts, sheetErr := book.AddSheet("按令牌统计", []float64{14, 26, 14, 28, 14, 18, 18, 18, 18, 16})
		if sheetErr != nil {
			return nil, sheetErr
		}
		_ = ts.Row([]xlsxwriter.Cell{t("令牌 ID"), t("令牌"), t("日期"), t("模型"), t("订单数"), t("输入 Token"), t("输出 Token"), t("缓存读取 Token"), t("缓存写入 Token"), t("最终费用")})
		tokenTotal := "0"
		for _, v := range tokens {
			final := v.Amount
			_ = ts.Row([]xlsxwriter.Cell{n64(v.TokenID), t(v.TokenName), t(v.Day.Format("2006-01-02")), t(v.ModelName), n64(v.RequestCount), n64(v.PromptTokens), n64(v.CompletionTokens), n64(v.CacheTokens), n64(v.CacheWriteTokens), d(final)})
			tokenTotal = addDecimal(tokenTotal, final)
		}
		_ = ts.Row([]xlsxwriter.Cell{t("合计"), t(""), t(""), t(""), t(""), t(""), t(""), t(""), t(""), d(tokenTotal)})
	}
	var out bytes.Buffer
	if err = book.Write(&out); err != nil {
		return nil, err
	}
	return out.Bytes(), nil
}

func statementPrice(records []billing.PriceRecord, model string, day time.Time) billing.Price {
	items := make([]billing.Price, 0)
	for _, record := range records {
		if record.ModelName == model {
			items = append(items, record.Price)
		}
	}
	price, _ := billing.SelectPrice(items, day, 0)
	return price
}

func statementDailyFilename(job billing.Job, day time.Time) string {
	name := job.UserName
	if job.JobType == "upstream_statement" {
		name = job.UpstreamName
	}
	name = strings.TrimSpace(name)
	if name == "" {
		name = fmt.Sprintf("对象-%d", map[bool]int64{true: job.UserID, false: job.UpstreamID}[job.JobType == "user_statement"])
	}
	name = strings.Map(func(r rune) rune {
		if strings.ContainsRune(`<>:"/\|?*`, r) || r < 32 {
			return '-'
		}
		return r
	}, name)
	return name + "-" + day.In(billing.BusinessLocation).Format("2006-01-02") + "-明细.xlsx"
}

func statementModelLabel(job billing.Job, channel, model string) string {
	if job.JobType == "upstream_statement" && strings.TrimSpace(channel) != "" {
		return channel + " / " + model
	}
	return model
}

func statementArchiveFilename(job billing.Job) string {
	kind := "用户账单"
	if job.JobType == "upstream_statement" {
		kind = "上游账单"
	}
	name := job.BillNo
	if name == "" {
		name = job.ID
	}
	name = strings.Map(func(r rune) rune {
		if strings.ContainsRune(`<>:"/\|?*`, r) || r < 32 {
			return '-'
		}
		return r
	}, strings.TrimSpace(name))
	return kind + "-" + name + ".zip"
}

func addDecimal(a, b string) string {
	x, ok := new(big.Rat).SetString(a)
	if !ok {
		x = new(big.Rat)
	}
	y, ok := new(big.Rat).SetString(b)
	if !ok {
		y = new(big.Rat)
	}
	return new(big.Rat).Add(x, y).FloatString(8)
}

func multiplyDecimal(a, b string) string {
	x, ok := new(big.Rat).SetString(a)
	if !ok {
		x = new(big.Rat)
	}
	y, ok := new(big.Rat).SetString(b)
	if !ok {
		y = big.NewRat(1, 1)
	}
	return new(big.Rat).Mul(x, y).FloatString(8)
}
