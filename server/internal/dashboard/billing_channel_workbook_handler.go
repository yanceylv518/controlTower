package dashboard

import (
	"context"
	"controltower/server/internal/billing"
	"controltower/server/internal/xlsxwriter"
	"fmt"
	"net/http"
	"strconv"
	"time"
)

type BillingChannelWorkbookStore interface {
	BillingChannelStore
	ListBillingModelMetadata(context.Context, string) ([]billing.ModelMetadata, error)
}
type BillingChannelWorkbookSource interface {
	ChannelLogsPage(context.Context, string, int64, time.Time, time.Time, billing.LogCursor, int) ([]billing.PagedLogRecord, error)
}
type BillingChannelWorkbookHandler struct {
	Store  BillingChannelWorkbookStore
	Source BillingChannelWorkbookSource
}

func (h BillingChannelWorkbookHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	site := q.Get("instance_id")
	channelID, idErr := strconv.ParseInt(q.Get("channel_id"), 10, 64)
	from, to, period, rangeErr := billingPeriodQuery(r)
	if idErr != nil || rangeErr != nil || site == "" || channelID <= 0 || !billingSiteAllowed(r, site, 0) {
		writeDashboardError(w, 400, "invalid_query")
		return
	}
	job, jobErr := billingJobForRead(r, h.Store, site, "channel_generate", from, to)
	var rows []billing.AggregateRow
	var err error
	if jobErr == nil && job.Status == "complete" {
		rows, err = h.Store.QueryBillingChannelAggregatesForJob(r.Context(), job.ID, channelID)
	} else {
		writeBillingReadConflict(w, jobErr)
		return
	}
	if err != nil {
		writeDashboardError(w, 500, "billing_query_failed")
		return
	}
	prices, err := h.Store.ListBillingPrices(r.Context(), site)
	if err != nil {
		writeDashboardError(w, 500, "billing_query_failed")
		return
	}
	ratios, err := h.Store.ListBillingGroupRatios(r.Context(), site)
	if err != nil {
		writeDashboardError(w, 500, "billing_query_failed")
		return
	}
	snapshots, err := h.Store.BillingRatioSnapshots(r.Context(), site, from, to)
	if err != nil {
		writeDashboardError(w, 500, "billing_query_failed")
		return
	}
	metadata, err := h.Store.ListBillingModelMetadata(r.Context(), site)
	if err != nil {
		writeDashboardError(w, 500, "billing_query_failed")
		return
	}
	settings, err := h.Store.ListBillingChannelSettings(r.Context(), site)
	if err != nil {
		writeDashboardError(w, 500, "billing_query_failed")
		return
	}
	discount := "1"
	if setting, ok := settings[channelID]; ok && setting.Discount != "" {
		discount = setting.Discount
	}
	name := fmt.Sprintf("渠道 %d", channelID)
	if len(rows) > 0 && rows[0].Username != "" {
		name = rows[0].Username
	}
	book := xlsxwriter.New()
	if err = writeChannelOverview(book, name, period, discount, rows, prices, ratios, snapshots); err == nil {
		err = writeDaily(book, channelID, period, billing.BuildDetails(rows, prices, ratios, snapshots))
	}
	if err == nil {
		err = writeRequestPages(r.Context(), book, period, from, to, prices, ratios, metadata, func(cursor billing.LogCursor, limit int) ([]billing.PagedLogRecord, error) {
			return h.Source.ChannelLogsPage(r.Context(), site, channelID, from, to, cursor, limit)
		})
	}
	if err != nil {
		writeDashboardError(w, 500, "billing_xlsx_failed")
		return
	}
	w.Header().Set("Content-Type", "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet")
	w.Header().Set("Content-Disposition", `attachment; filename="`+billingDownloadName("channel-billing", 0, channelID, from, to)+`.xlsx"`)
	_ = book.Write(w)
}

func writeChannelOverview(book *xlsxwriter.Workbook, name, month, discount string, rows []billing.AggregateRow, prices []billing.PriceRecord, ratios []billing.GroupRatio, snapshots map[string]string) error {
	items, total, err := billing.BuildInvoice(rows, prices, ratios, snapshots, discount)
	if err != nil {
		return err
	}
	s, err := book.AddSheet("账单概览", []float64{8, 28, 12, 18, 16, 18, 18, 16, 18, 18, 16, 18, 18, 12, 20})
	if err != nil {
		return err
	}
	titleRows(s, fmt.Sprintf("%s · %s 渠道对账单", name, month), month, []string{"序号", "模型名称", "请求数", "输入Token单价/1M", "普通输入Token", "输入Token总价", "输出Token单价/1M", "输出Token", "输出Token总价", "缓存读取单价/1M", "缓存读取Token", "缓存读取总价", "缓存写入单价/1M", "缓存写入Token", "缓存写入总价", "账单总金额", "折扣", "折扣后总金额"})
	for i, item := range items {
		model := item.ModelName
		if item.TierFrom > 0 {
			model += fmt.Sprintf("（≥%d Token）", item.TierFrom)
		}
		s.Row([]xlsxwriter.Cell{n(i + 1), t(model), n64(item.RequestCount), d(item.InputPrice), n64(item.PromptTokens), d(item.InputAmount), d(item.OutputPrice), n64(item.CompletionTokens), d(item.OutputAmount), d(item.CachePrice), n64(item.CacheTokens), d(item.CacheAmount), d(item.CacheWritePrice), n64(item.CacheWriteTokens), d(item.CacheWriteAmount), d(item.Amount), d(item.Discount), d(item.DiscountedAmount)})
	}
	totalRow := make([]xlsxwriter.Cell, 18)
	totalRow[0] = t("合计")
	totalRow[15] = d(total.Amount)
	totalRow[16] = d(total.Discount)
	totalRow[17] = d(total.DiscountedAmount)
	s.Row(totalRow)
	return nil
}
