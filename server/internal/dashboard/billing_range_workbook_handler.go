package dashboard

import (
	"context"
	"fmt"
	"math/big"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"controltower/server/internal/billing"
	"controltower/server/internal/xlsxwriter"
)

type BillingRangeWorkbookStore interface {
	ListBillingUserBillDays(context.Context, string, time.Time, time.Time, int64, string, int) ([]billing.UserBillDay, error)
	ListBillingUserTokenBillDays(context.Context, string, time.Time, time.Time, int64, int) ([]billing.UserTokenBillDay, error)
}

type BillingRangeWorkbookHandler struct{ Store BillingRangeWorkbookStore }

type billingRangeUserTotal struct {
	ID                                                             int64
	Name                                                           string
	Days                                                           map[string]struct{}
	Requests, Prompt, CacheRead, CacheWrite, Completion, Anomalies int64
	Amount, AnomalyAmount                                          *big.Rat
}

type billingRangeTokenTotal struct {
	ID                                                             int64
	Name                                                           string
	Days                                                           map[string]struct{}
	Requests, Prompt, CacheRead, CacheWrite, Completion, Anomalies int64
	Amount, AnomalyAmount                                          *big.Rat
}

func (h BillingRangeWorkbookHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	site := strings.TrimSpace(q.Get("instance_id"))
	from, fromErr := time.ParseInLocation("2006-01-02", strings.TrimSpace(q.Get("from")), billing.BusinessLocation)
	through, throughErr := time.ParseInLocation("2006-01-02", strings.TrimSpace(q.Get("through")), billing.BusinessLocation)
	today := time.Now().In(billing.BusinessLocation)
	today = time.Date(today.Year(), today.Month(), today.Day(), 0, 0, 0, 0, billing.BusinessLocation)
	if site == "" || fromErr != nil || throughErr != nil || through.Before(from) || !through.Before(today) || through.Sub(from) >= 366*24*time.Hour {
		writeDashboardError(w, http.StatusBadRequest, "invalid_date_range")
		return
	}
	userID, _ := strconv.ParseInt(strings.TrimSpace(q.Get("user_id")), 10, 64)
	if userID <= 0 {
		writeDashboardError(w, http.StatusBadRequest, "invalid_user_id")
		return
	}
	if !billingSiteAllowed(r, site, userID) {
		writeDashboardError(w, http.StatusForbidden, "forbidden")
		return
	}
	if !billingReadonlyAvailable(h.Store, site) {
		writeDashboardError(w, http.StatusConflict, "readonly_source_unavailable")
		return
	}
	to := through.AddDate(0, 0, 1)
	rows, err := h.Store.ListBillingUserBillDays(r.Context(), site, from, to, userID, "", 100000)
	if err != nil {
		writeDashboardError(w, http.StatusInternalServerError, "billing_user_days_query_failed")
		return
	}
	period := from.Format("2006-01-02") + " 至 " + through.Format("2006-01-02")
	book := xlsxwriter.New()
	groupByToken := strings.TrimSpace(q.Get("group_by")) == "token"
	if groupByToken {
		tokenRows, tokenErr := h.Store.ListBillingUserTokenBillDays(r.Context(), site, from, to, userID, 100000)
		if tokenErr != nil {
			writeDashboardError(w, http.StatusInternalServerError, "billing_user_token_days_query_failed")
			return
		}
		if err = writeBillingRangeTokenSummary(book, period, tokenRows); err == nil {
			err = writeBillingRangeTokens(book, period, tokenRows)
		}
	} else if err = writeBillingRangeSummary(book, period, rows); err == nil {
		err = writeBillingRangeDaily(book, period, rows)
	}
	if err != nil {
		writeDashboardError(w, http.StatusInternalServerError, "billing_xlsx_failed")
		return
	}
	w.Header().Set("Content-Type", "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="user-%d-billing-%s-%s.xlsx"`, userID, from.Format("20060102"), through.Format("20060102")))
	_ = book.Write(w)
}

func writeBillingRangeTokenSummary(book *xlsxwriter.Workbook, period string, rows []billing.UserTokenBillDay) error {
	byToken := map[int64]*billingRangeTokenTotal{}
	for _, row := range rows {
		item := byToken[row.TokenID]
		if item == nil {
			item = &billingRangeTokenTotal{ID: row.TokenID, Name: row.TokenName, Days: map[string]struct{}{}, Amount: new(big.Rat), AnomalyAmount: new(big.Rat)}
			byToken[row.TokenID] = item
		}
		if item.Name == "" {
			item.Name = row.TokenName
		}
		item.Days[row.Day.Format("2006-01-02")] = struct{}{}
		item.Requests += row.RequestCount
		item.Prompt += row.PromptTokens
		item.CacheRead += row.CacheReadTokens
		item.CacheWrite += row.CacheWriteTokens
		item.Completion += row.CompletionTokens
		item.Anomalies += row.AnomalyRows
		decimalSum(item.Amount, row.Amount)
		decimalSum(item.AnomalyAmount, row.AnomalyAmount)
	}
	items := make([]*billingRangeTokenTotal, 0, len(byToken))
	for _, item := range byToken {
		items = append(items, item)
	}
	sort.Slice(items, func(i, j int) bool { return items[i].ID < items[j].ID })
	s, err := book.AddSheet("令牌汇总", []float64{12, 26, 12, 12, 18, 18, 18, 18, 12, 16, 16})
	if err != nil {
		return err
	}
	titleRows(s, "用户账单令牌汇总", period, []string{"令牌ID", "令牌名称", "账单天数", "请求数", "普通输入Token", "缓存读取Token", "缓存写入Token", "输出Token", "待确认数量", "待确认金额", "正常账单金额"})
	for _, item := range items {
		s.Row([]xlsxwriter.Cell{n64(item.ID), t(item.Name), n(len(item.Days)), n64(item.Requests), n64(item.Prompt), n64(item.CacheRead), n64(item.CacheWrite), n64(item.Completion), n64(item.Anomalies), d(item.AnomalyAmount.FloatString(6)), d(item.Amount.FloatString(6))})
	}
	return nil
}

func writeBillingRangeTokens(book *xlsxwriter.Workbook, period string, rows []billing.UserTokenBillDay) error {
	s, err := book.AddSheet("令牌账单明细", []float64{12, 26, 14, 28, 12, 18, 18, 18, 18, 12, 16, 16})
	if err != nil {
		return err
	}
	titleRows(s, "用户令牌账单明细", period, []string{"令牌ID", "令牌名称", "账单日期", "模型", "请求数", "普通输入Token", "缓存读取Token", "缓存写入Token", "输出Token", "待确认数量", "待确认金额", "正常账单金额"})
	for _, row := range rows {
		s.Row([]xlsxwriter.Cell{n64(row.TokenID), t(row.TokenName), t(row.Day.Format("2006-01-02")), t(row.ModelName), n64(row.RequestCount), n64(row.PromptTokens), n64(row.CacheReadTokens), n64(row.CacheWriteTokens), n64(row.CompletionTokens), n64(row.AnomalyRows), d(row.AnomalyAmount), d(row.Amount)})
	}
	return nil
}

func decimalSum(target *big.Rat, value string) {
	if parsed, ok := new(big.Rat).SetString(strings.TrimSpace(value)); ok {
		target.Add(target, parsed)
	}
}

func writeBillingRangeSummary(book *xlsxwriter.Workbook, period string, rows []billing.UserBillDay) error {
	byUser := map[int64]*billingRangeUserTotal{}
	for _, row := range rows {
		item := byUser[row.UserID]
		if item == nil {
			item = &billingRangeUserTotal{ID: row.UserID, Name: row.Username, Days: map[string]struct{}{}, Amount: new(big.Rat), AnomalyAmount: new(big.Rat)}
			byUser[row.UserID] = item
		}
		if item.Name == "" {
			item.Name = row.Username
		}
		item.Days[row.Day.Format("2006-01-02")] = struct{}{}
		item.Requests += row.RequestCount
		item.Prompt += row.PromptTokens
		item.CacheRead += row.CacheReadTokens
		item.CacheWrite += row.CacheWriteTokens
		item.Completion += row.CompletionTokens
		item.Anomalies += row.AnomalyRows
		decimalSum(item.Amount, row.Amount)
		decimalSum(item.AnomalyAmount, row.AnomalyAmount)
	}
	items := make([]*billingRangeUserTotal, 0, len(byUser))
	for _, item := range byUser {
		items = append(items, item)
	}
	sort.Slice(items, func(i, j int) bool { return items[i].ID < items[j].ID })
	s, err := book.AddSheet("区间汇总", []float64{10, 24, 12, 12, 18, 18, 18, 18, 12, 16, 16})
	if err != nil {
		return err
	}
	titleRows(s, "用户账单区间汇总", period, []string{"用户ID", "用户", "账单天数", "请求数", "普通输入Token", "缓存读取Token", "缓存写入Token", "输出Token", "待确认数量", "待确认金额", "正常账单金额"})
	for _, item := range items {
		s.Row([]xlsxwriter.Cell{n64(item.ID), t(item.Name), n(len(item.Days)), n64(item.Requests), n64(item.Prompt), n64(item.CacheRead), n64(item.CacheWrite), n64(item.Completion), n64(item.Anomalies), d(item.AnomalyAmount.FloatString(6)), d(item.Amount.FloatString(6))})
	}
	return nil
}

func writeBillingRangeDaily(book *xlsxwriter.Workbook, period string, rows []billing.UserBillDay) error {
	s, err := book.AddSheet("日账单列表", []float64{14, 10, 24, 28, 12, 18, 18, 18, 18, 12, 16, 16, 21})
	if err != nil {
		return err
	}
	titleRows(s, "用户每日模型账单", period, []string{"账单日期", "用户ID", "用户", "模型", "请求数", "普通输入Token", "缓存读取Token", "缓存写入Token", "输出Token", "待确认数量", "待确认金额", "正常账单金额", "生成时间"})
	for _, row := range rows {
		s.Row([]xlsxwriter.Cell{t(row.Day.Format("2006-01-02")), n64(row.UserID), t(row.Username), t(row.ModelName), n64(row.RequestCount), n64(row.PromptTokens), n64(row.CacheReadTokens), n64(row.CacheWriteTokens), n64(row.CompletionTokens), n64(row.AnomalyRows), d(row.AnomalyAmount), d(row.Amount), t(row.ActivatedAt.In(billing.BusinessLocation).Format("2006-01-02 15:04:05"))})
	}
	return nil
}
