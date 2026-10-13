package dashboard

import (
	"context"
	"controltower/server/internal/billing"
	"math/big"
	"net/http"
	"regexp"
	"strconv"
	"strings"
)

// Monthly preview reads the same saved aggregates and currency snapshot as XLSX.
func (h BillingStatementResultHandler) writeMonthlyPreview(w http.ResponseWriter, r *http.Request, job billing.Job, rows []billing.StatementAggregateRow) {
	coveredDays := map[string]bool{}
	for _, row := range rows {
		coveredDays[row.Day.In(billing.BusinessLocation).Format("2006-01-02")] = true
	}
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
		headers = []string{"日期", "令牌", "模型"}
	}
	start := len(headers)
	headers = append(headers, "请求数", "普通输入 Token", "普通输出 Token", "缓存读取 Token", "缓存写入 Token", "图像输入 Token", "图像输出 Token", "音频输入 Token", "音频输出 Token", billing.UnitPriceHeader(currency), "原价金额", "折扣", "折后金额")
	tierRows := map[int][][]string{}
	tiers := map[[3]string][]billing.TierStatistics{}
	if dimension == "daily" && job.JobType == "user_statement" {
		if store, ok := h.Store.(interface {
			QueryBillingDailyTierStatistics(context.Context, string) ([]billing.DailyTierStatistics, error)
		}); ok {
			saved, e := store.QueryBillingDailyTierStatistics(r.Context(), job.ID)
			if e != nil {
				writeDashboardError(w, 500, "billing_statement_query_failed")
				return
			}
			for _, v := range saved {
				if monthlyPeakTier.MatchString(strings.TrimSpace(v.Tier)) {
					key := [3]string{v.Day, v.Model, billing.DiscountGroupKey(v.Discount)}
					tiers[key] = append(tiers[key], v.TierStatistics)
				}
			}
		}
	}
	data := [][]string{}
	appendRow := func(prefix []string, count, input, output, read, write int64, media billing.MultimediaUsage, before, discount, amount string, prices billing.UnitPrices) {
		for _, v := range []int64{count, input, output, read, write, media.ImageInputTokens, media.ImageOutputTokens, media.AudioInputTokens, media.AudioOutputTokens} {
			prefix = append(prefix, strconv.FormatInt(v, 10))
		}
		before = billing.DisplaySettlementAmount(before, rate)
		if before == "" {
			before = "—"
		}
		data = append(data, append(prefix, billing.UnitPriceLabel(prices, rate), before, billing.DiscountGroupLabel(discount), billing.DisplaySettlementAmount(amount, rate)))
	}
	if dimension == "token" {
		tokens, e := h.Store.QueryBillingTokenRows(r.Context(), job.ID, job.UserID, -1, job.From, job.To)
		if e != nil {
			writeDashboardError(w, 500, "billing_statement_query_failed")
			return
		}
		for _, v := range tokens {
			appendRow([]string{v.Day.Format("2006-01-02"), v.TokenName, v.ModelName}, v.RequestCount, v.PromptTokens, v.CompletionTokens, v.CacheTokens, v.CacheWriteTokens, v.MultimediaUsage, v.BeforeAmount, v.SettlementDiscount, v.Amount, v.UnitPrices)
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
			appendRow(prefix, v.RequestCount, v.PromptTokens, v.CompletionTokens, v.CacheTokens, v.CacheWriteTokens, v.MultimediaUsage, v.BeforeAmount, v.SettlementDiscount, v.Amount, v.UnitPrices)
			index := len(data) - 1
			for _, tier := range tiers[[3]string{date, v.ModelName, billing.DiscountGroupKey(v.SettlementDiscount)}] {
				tierRows[index] = append(tierRows[index], monthlyTierPreviewRow(date, tier, rate))
			}
		}
	}
	legacyMixed := false
	for _, row := range data {
		if row[len(headers)-2] == billing.DiscountGroupLabel("mixed") {
			legacyMixed = true
		}
	}
	totals := make([]string, len(headers))
	totals[0] = "合计"
	for col := start; col < len(headers); col++ {
		if col == len(headers)-2 || col == len(headers)-4 {
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
	pageTiers := map[int][][]string{}
	for i := lo; i < hi; i++ {
		if children := tierRows[i]; len(children) > 0 {
			pageTiers[i-lo] = children
		}
	}
	writeDashboardJSON(w, 200, map[string]any{"tier_rows": pageTiers, "coverage": billing.MonthlyCoverageFromRows(job, rows), "legacy_mixed_discounts": legacyMixed, "covered_days": len(coveredDays), "headers": headers, "rows": data[lo:hi], "totals": totals, "total": len(data), "page_size": size, "currency": currency, "numeric_start": start})
}

var monthlyPeakTier = regexp.MustCompile(`(?i)^(高峰(?:时段|期)?|低谷(?:时段|期)?|空闲(?:时段|期)?|peak(?:[_ -]hours)?|off[_ -]?peak(?:[_ -]hours)?|valley)$`)

func monthlyTierPreviewRow(day string, v billing.TierStatistics, rate *big.Rat) []string {
	row := []string{day, v.Tier}
	for _, n := range []int64{v.Requests, v.Input, v.Output, v.CacheRead, v.CacheWrite, v.ImageInputTokens, v.ImageOutputTokens, v.AudioInputTokens, v.AudioOutputTokens} {
		row = append(row, strconv.FormatInt(n, 10))
	}
	before := billing.DisplaySettlementAmount(v.BeforeAmount, rate)
	if before == "" {
		before = "—"
	}
	return append(row, billing.UnitPriceLabel(v.Prices, rate), before, billing.DiscountGroupLabel(v.Discount), billing.DisplaySettlementAmount(v.Amount, rate))
}
