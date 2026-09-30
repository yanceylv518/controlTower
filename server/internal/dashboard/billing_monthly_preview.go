package dashboard

import (
	"controltower/server/internal/billing"
	"math/big"
	"net/http"
	"strconv"
)

// Monthly preview reads the same saved aggregates and currency snapshot as XLSX.
func (h BillingStatementResultHandler) writeMonthlyPreview(w http.ResponseWriter, r *http.Request, job billing.Job, rows []billing.StatementAggregateRow) {
	dimension := r.URL.Query().Get("dimension")
	if job.BillPeriod != "monthly" || job.UsageVersion < 3 || (dimension != "month" && dimension != "daily" && dimension != "token") || (dimension == "token" && job.JobType != "user_statement") {
		writeDashboardError(w, 400, "invalid_query")
		return
	}
	display, rate, err := billing.SettlementDisplay(job.MoneySnapshot)
	if err != nil {
		writeDashboardError(w, 500, "billing_statement_preview_failed")
		return
	}
	currency := billing.SettlementCurrencyLabel(display)
	headers := []string{"日期", "模型"}
	if job.JobType == "upstream_statement" {
		headers = append(headers, "渠道")
	}
	if dimension == "token" {
		headers = []string{"日期", "令牌", "令牌 ID", "模型"}
	}
	start := len(headers)
	headers = append(headers, "请求数", "普通输入 Token", "普通输出 Token", "缓存读取 Token", "缓存写入 Token", "图像输入 Token", "图像输出 Token", "音频输入 Token", "音频输出 Token", "原价金额", "折扣", "折后金额")
	data := [][]string{}
	appendRow := func(prefix []string, count, input, output, read, write int64, media billing.MultimediaUsage, before, discount, amount string) {
		for _, v := range []int64{count, input, output, read, write, media.ImageInputTokens, media.ImageOutputTokens, media.AudioInputTokens, media.AudioOutputTokens} {
			prefix = append(prefix, strconv.FormatInt(v, 10))
		}
		before = billing.DisplaySettlementAmount(before, rate)
		if before == "" {
			before = "—"
		}
		data = append(data, append(prefix, before, billing.DiscountLabel(discount), billing.DisplaySettlementAmount(amount, rate)))
	}
	if dimension == "token" {
		tokens, e := h.Store.QueryBillingTokenRows(r.Context(), job.ID, job.UserID, -1, job.From, job.To)
		if e != nil {
			writeDashboardError(w, 500, "billing_statement_query_failed")
			return
		}
		for _, v := range tokens {
			appendRow([]string{v.Day.Format("2006-01-02"), v.TokenName, strconv.FormatInt(v.TokenID, 10), v.ModelName}, v.RequestCount, v.PromptTokens, v.CompletionTokens, v.CacheTokens, v.CacheWriteTokens, v.MultimediaUsage, v.BeforeAmount, v.SettlementDiscount, v.Amount)
		}
	} else {
		for _, g := range groupStatementRows(job, rows, nil, dimension == "daily") {
			v := g.Row
			date := job.From.In(billing.BusinessLocation).Format("2006-01")
			if dimension == "daily" {
				date = v.Day.Format("2006-01-02")
			}
			prefix := []string{date, v.ModelName}
			if job.JobType == "upstream_statement" {
				prefix = append(prefix, v.ChannelName)
			}
			appendRow(prefix, v.RequestCount, v.PromptTokens, v.CompletionTokens, v.CacheTokens, v.CacheWriteTokens, v.MultimediaUsage, v.BeforeAmount, v.SettlementDiscount, v.Amount)
		}
	}
	totals := make([]string, len(headers))
	totals[0] = "合计"
	for col := start; col < len(headers); col++ {
		if col == len(headers)-2 {
			continue
		}
		sum := new(big.Rat)
		known := true
		for _, row := range data {
			n, ok := new(big.Rat).SetString(row[col])
			if !ok {
				known = false
				break
			}
			sum.Add(sum, n)
		}
		if !known {
			totals[col] = "—"
		} else {
			digits := 0
			if col >= len(headers)-3 {
				digits = 6
			}
			totals[col] = sum.FloatString(digits)
		}
	}
	page, _ := strconv.Atoi(r.URL.Query().Get("page"))
	if page < 1 {
		page = 1
	}
	const size = 50
	lo := (page - 1) * size
	if lo < 0 || lo > len(data) {
		lo = len(data)
	}
	hi := lo + size
	if hi > len(data) {
		hi = len(data)
	}
	writeDashboardJSON(w, 200, map[string]any{"headers": headers, "rows": data[lo:hi], "totals": totals, "total": len(data), "page_size": size, "currency": currency, "numeric_start": start})
}
