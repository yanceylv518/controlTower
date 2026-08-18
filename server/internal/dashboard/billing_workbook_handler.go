package dashboard

import (
	"context"
	ctauth "controltower/server/internal/auth"
	"controltower/server/internal/billing"
	"controltower/server/internal/xlsxwriter"
	"fmt"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"
)

type BillingWorkbookStore interface {
	BillingSummaryStore
	ListBillingModelMetadata(context.Context, string) ([]billing.ModelMetadata, error)
}
type BillingWorkbookSource interface {
	DetailedLogsPage(context.Context, string, int64, time.Time, time.Time, billing.LogCursor, int) ([]billing.PagedLogRecord, error)
}
type BillingWorkbookHandler struct {
	Store  BillingWorkbookStore
	Source BillingWorkbookSource
}

func (h BillingWorkbookHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	site := q.Get("instance_id")
	uid, e := strconv.ParseInt(q.Get("user_id"), 10, 64)
	from, to, period, rangeErr := billingPeriodQuery(r)
	if e != nil || rangeErr != nil || site == "" || uid <= 0 {
		writeDashboardError(w, 400, "invalid_query")
		return
	}
	if u, ok := ctauth.CurrentUser(r); ok && u.Role != "admin" && (u.ScopeSite != site || !containsBillingUser(u.ScopeUserIDs, uid)) {
		writeDashboardError(w, 403, "forbidden")
		return
	}
	job, jobErr := billingJobForRead(r, h.Store, site, "generate", from, to)
	var rows []billing.AggregateRow
	if jobErr == nil && job.Status == "complete" {
		rows, e = h.Store.QueryBillingAggregatesForJob(r.Context(), job.ID, []int64{uid})
	} else {
		writeBillingReadConflict(w, jobErr)
		return
	}
	if e != nil {
		writeDashboardError(w, 500, "billing_query_failed")
		return
	}
	prices, e := h.Store.ListBillingPrices(r.Context(), site)
	if e != nil {
		writeDashboardError(w, 500, "billing_query_failed")
		return
	}
	ratios, e := h.Store.ListBillingGroupRatios(r.Context(), site)
	if e != nil {
		writeDashboardError(w, 500, "billing_query_failed")
		return
	}
	snapshots, e := h.Store.BillingRatioSnapshots(r.Context(), site, from, to)
	if e != nil {
		writeDashboardError(w, 500, "billing_query_failed")
		return
	}
	metadata, e := h.Store.ListBillingModelMetadata(r.Context(), site)
	if e != nil {
		writeDashboardError(w, 500, "billing_query_failed")
		return
	}
	book := xlsxwriter.New()
	stage := "overview"
	if e = writeOverview(book, uid, period, rows, prices, ratios, snapshots); e == nil {
		stage = "daily"
		e = writeDaily(book, uid, period, billing.BuildDetails(rows, prices, ratios, snapshots))
	}
	if e == nil && r.URL.Query().Get("include_requests") != "0" {
		stage = "request_details"
		e = writeRequests(r.Context(), book, h.Source, site, uid, period, from, to, prices, ratios, metadata)
	}
	if e != nil {
		log.Printf("billing user workbook failed site=%s user=%d from=%s to=%s stage=%s: %v", site, uid, from.Format(time.RFC3339), to.Format(time.RFC3339), stage, e)
		writeDashboardError(w, 500, "billing_xlsx_failed")
		return
	}
	w.Header().Set("Content-Type", "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet")
	w.Header().Set("Content-Disposition", `attachment; filename="`+billingDownloadName("billing", uid, 0, from, to)+`.xlsx"`)
	if e = book.Write(w); e != nil {
		return
	}
}
func titleRows(s *xlsxwriter.Sheet, title, period string, headers []string) {
	s.Row([]xlsxwriter.Cell{{Value: title, Style: 2}})
	s.Row([]xlsxwriter.Cell{{Value: "结算周期"}, {Value: period}})
	s.Row([]xlsxwriter.Cell{})
	cells := make([]xlsxwriter.Cell, len(headers))
	for i, v := range headers {
		cells[i] = xlsxwriter.Cell{Value: v, Style: 1}
	}
	s.Row(cells)
}
func writeOverview(book *xlsxwriter.Workbook, uid int64, month string, rows []billing.AggregateRow, prices []billing.PriceRecord, ratios []billing.GroupRatio, snapshots map[string]string) error {
	items, total, e := billing.BuildInvoice(rows, prices, ratios, snapshots, "1")
	if e != nil {
		return e
	}
	s, e := book.AddSheet("账单概览", []float64{8, 28, 12, 18, 16, 18, 18, 16, 18, 18, 16, 18, 18, 12, 20})
	if e != nil {
		return e
	}
	titleRows(s, fmt.Sprintf("%s · %s 对账单", billingUserName(rows), month), month, []string{"序号", "模型名称", "请求数", "输入Token单价/1M", "普通输入Token", "输入Token总价", "输出Token单价/1M", "输出Token", "输出Token总价", "缓存读取单价/1M", "缓存读取Token", "缓存读取总价", "缓存写入单价/1M", "缓存写入Token", "缓存写入总价", "账单总金额", "折扣", "账单折扣后总金额"})
	for i, v := range items {
		name := v.ModelName
		if v.TierFrom > 0 {
			name += fmt.Sprintf("（大于等于 %d Token）", v.TierFrom)
		}
		s.Row([]xlsxwriter.Cell{n(i + 1), t(name), n64(v.RequestCount), d(v.InputPrice), n64(v.PromptTokens), d(v.InputAmount), d(v.OutputPrice), n64(v.CompletionTokens), d(v.OutputAmount), d(v.CachePrice), n64(v.CacheTokens), d(v.CacheAmount), d(v.CacheWritePrice), n64(v.CacheWriteTokens), d(v.CacheWriteAmount), d(v.Amount), t(""), t("")})
	}
	totalRow := make([]xlsxwriter.Cell, 18)
	totalRow[0] = t("合计")
	totalRow[15] = d(total.Amount)
	totalRow[16] = t("")
	totalRow[17] = t("")
	s.Row(totalRow)
	return nil
}
func writeDaily(book *xlsxwriter.Workbook, uid int64, month string, items []billing.DetailItem) error {
	s, e := book.AddSheet("日统计", []float64{14, 26, 14, 12, 16, 16, 16, 16, 16, 16, 18})
	if e != nil {
		return e
	}
	titleRows(s, fmt.Sprintf("%s 日统计", month), month, []string{"日期", "模型名称", "阶梯起点", "请求数", "普通输入Token", "缓存读取Token", "缓存写入Token", "输出Token", "输入单价/1M", "缓存读取单价/1M", "缓存写入单价/1M", "输出单价/1M", "金额"})
	for _, v := range items {
		s.Row([]xlsxwriter.Cell{t(v.Day), t(v.ModelName), n64(v.TierFrom), n64(v.RequestCount), n64(v.PromptTokens), n64(v.CacheTokens), n64(v.CacheWriteTokens), n64(v.CompletionTokens), d(v.InputPrice), d(v.CachePrice), d(v.CacheWritePrice), d(v.OutputPrice), d(v.Amount)})
	}
	return nil
}
func writeRequests(ctx context.Context, book *xlsxwriter.Workbook, source BillingWorkbookSource, site string, uid int64, month string, from, to time.Time, prices []billing.PriceRecord, ratios []billing.GroupRatio, metadata []billing.ModelMetadata) error {
	return writeRequestPages(ctx, book, month, from, to, prices, ratios, metadata, func(cursor billing.LogCursor, limit int) ([]billing.PagedLogRecord, error) {
		return source.DetailedLogsPage(ctx, site, uid, from, to, cursor, limit)
	})
}
func writeRequestPages(ctx context.Context, book *xlsxwriter.Workbook, month string, from, to time.Time, prices []billing.PriceRecord, ratios []billing.GroupRatio, metadata []billing.ModelMetadata, readPage func(billing.LogCursor, int) ([]billing.PagedLogRecord, error)) error {
	byModel := map[string][]billing.Price{}
	for _, v := range prices {
		byModel[v.ModelName] = append(byModel[v.ModelName], v.Price)
	}
	byGroup := map[string]string{}
	for _, v := range ratios {
		byGroup[v.GroupName] = v.Ratio
	}
	maxContext := map[string]int64{}
	for _, v := range metadata {
		maxContext[v.ModelName] = v.MaxContextTokens
	}
	headers := []string{"请求时间", "Request ID", "用户名称", "模型名称", "普通输入 Token", "缓存读取 Token", "缓存写入 Token", "输出 Token", "输入单价/1M", "输出单价/1M", "缓存读取单价/1M", "缓存写入单价/1M", "输入费用", "输出费用", "缓存读取费用", "缓存写入费用", "请求总金额", "实际扣除 Quota"}
	sheetNo, rowNo := 1, 0
	var s *xlsxwriter.Sheet
	newSheet := func() error {
		name := "请求明细"
		if sheetNo > 1 {
			name = fmt.Sprintf("请求明细-%03d", sheetNo)
		}
		var e error
		s, e = book.AddSheet(name, []float64{21, 30, 20, 26, 16, 16, 18, 16, 16, 16, 16, 16, 16, 16, 18, 18})
		if e == nil {
			titleRows(s, fmt.Sprintf("%s 请求明细", month), month, headers)
			rowNo = 4
		}
		return e
	}
	if e := newSheet(); e != nil {
		return e
	}
	cursor := billing.LogCursor{}
	pageNumber := 0
	for {
		pageNumber++
		logs, e := readPage(cursor, billing.BillingPageSize)
		if e != nil {
			return fmt.Errorf("request details page=%d cursor=%d/%d: %w", pageNumber, cursor.CreatedUnix, cursor.ID, e)
		}
		if len(logs) == 0 {
			break
		}
		for _, v := range logs {
			if len(billing.AnomalyReasons(v, maxContext[v.ModelName])) != 0 {
				continue
			}
			price, ok := billing.SelectPrice(byModel[v.ModelName], time.Unix(v.CreatedUnix, 0), billing.RequestContextTokens(v))
			if !ok {
				continue
			}
			price, logGroupRatio := billing.RequestPrice(v, price)
			if logGroupRatio == "" {
				logGroupRatio = byGroup[v.GroupName]
			}
			charge := billing.PriceRequest(v, price, logGroupRatio)
			if rowNo >= 1_000_000 {
				sheetNo++
				if e = newSheet(); e != nil {
					return e
				}
			}
			s.Row([]xlsxwriter.Cell{t(billing.FormatBusinessTime(v.CreatedUnix)), t(v.RequestID), t(v.Username), t(v.ModelName), n64(v.PromptTokens.Int64), n64(v.CacheTokens), n64(v.CacheWriteTokens), n64(v.CompletionTokens.Int64), d(charge.InputPrice), d(charge.OutputPrice), d(charge.CachePrice), d(charge.CacheWritePrice), d(charge.InputAmount), d(charge.OutputAmount), d(charge.CacheAmount), d(charge.CacheWriteAmount), d(charge.Total), n64(v.Quota)})
			rowNo++
		}
		last := logs[len(logs)-1]
		cursor = billing.LogCursor{CreatedUnix: last.CreatedUnix, ID: last.ID}
		if len(logs) < billing.BillingPageSize {
			break
		}
	}
	return nil
}

func billingUserName(rows []billing.AggregateRow) string {
	for _, v := range rows {
		if strings.TrimSpace(v.Username) != "" {
			return v.Username
		}
	}
	return "用户账单"
}
func t(v string) xlsxwriter.Cell { return xlsxwriter.Cell{Value: v, Style: 5} }
func n(v int) xlsxwriter.Cell    { return xlsxwriter.Cell{Value: strconv.Itoa(v), Number: true, Style: 3} }
func n64(v int64) xlsxwriter.Cell {
	return xlsxwriter.Cell{Value: strconv.FormatInt(v, 10), Number: true, Style: 3}
}
func d(v string) xlsxwriter.Cell {
	if strings.TrimSpace(v) == "" {
		v = "0"
	}
	return xlsxwriter.Cell{Value: v, Number: true, Style: 4}
}
