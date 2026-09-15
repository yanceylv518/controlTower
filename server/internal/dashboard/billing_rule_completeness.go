package dashboard

import (
	"math/big"
	"sort"
	"strings"
)

const ordinaryRulePrefix = "历史单价（金额/百万 Token，含分组倍率）："

var ordinaryPriceLabels = [...]string{"输入", "输出", "缓存读取", "缓存写入", "5m 写入", "1h 写入", "图像输入"}

type ordinaryRuleSnapshot struct {
	text, context string
	values        [7]string
}

// Only the exact ordinary-price format written by CT is merged. Expressions,
// tiers, request conditions and surcharge snapshots must retain their identity.
func parseOrdinaryRule(rule string) (ordinaryRuleSnapshot, bool) {
	first, context, _ := strings.Cut(rule, "\n")
	if !strings.HasPrefix(first, ordinaryRulePrefix) {
		return ordinaryRuleSnapshot{}, false
	}
	parts := strings.Split(strings.TrimPrefix(first, ordinaryRulePrefix), "；")
	if len(parts) != len(ordinaryPriceLabels) {
		return ordinaryRuleSnapshot{}, false
	}
	out := ordinaryRuleSnapshot{text: rule, context: context}
	for i, label := range ordinaryPriceLabels {
		part := strings.TrimSpace(parts[i])
		if !strings.HasPrefix(part, label+" ") {
			return ordinaryRuleSnapshot{}, false
		}
		value := strings.TrimPrefix(part, label+" ")
		if value != "未记录" {
			price, ok := new(big.Rat).SetString(value)
			if !ok || price.Sign() < 0 {
				return ordinaryRuleSnapshot{}, false
			}
			value = price.FloatString(6)
		}
		out.values[i] = value
	}
	return out, true
}

// Missing is not a price. Merge compatible observed prices for display only,
// retaining an explicit warning that missing prices were never established.
// If any observed values conflict, never bridge them through a partial row.
func groupRuleCompleteness(rules []string) ([]string, bool) {
	groups := map[string][]ordinaryRuleSnapshot{}
	out := []string{}
	for _, rule := range rules {
		row, ok := parseOrdinaryRule(rule)
		if !ok {
			out = append(out, rule)
			continue
		}
		groups[row.context] = append(groups[row.context], row)
	}
	ambiguous := false
	for context, rows := range groups {
		var observed [7]map[string]bool
		var missing [7]bool
		conflict, partial := false, false
		for i := range observed {
			observed[i] = map[string]bool{}
			for _, row := range rows {
				if row.values[i] == "未记录" {
					missing[i] = true
				} else {
					observed[i][row.values[i]] = true
				}
			}
			conflict = conflict || len(observed[i]) > 1
			partial = partial || (missing[i] && len(observed[i]) > 0)
		}
		if conflict {
			ambiguous = ambiguous || partial
			for _, row := range rows {
				out = append(out, row.text)
			}
			continue
		}
		fields, missingLabels := []string{}, []string{}
		for i, label := range ordinaryPriceLabels {
			value := "未记录"
			for price := range observed[i] {
				value = price
			}
			fields = append(fields, label+" "+value)
			if missing[i] && len(observed[i]) > 0 {
				missingLabels = append(missingLabels, label)
			}
		}
		text := ordinaryRulePrefix + strings.Join(fields, "；")
		if context != "" {
			text += "\n" + context
		}
		if len(missingLabels) > 0 {
			text += "\n已记录的单价一致；部分订单未记录：" + strings.Join(missingLabels, "、") + "。缺失信息不作为另一套价格，也不代表这些订单已确认使用上述单价。"
		}
		out = append(out, text)
	}
	sort.Strings(out)
	return out, ambiguous
}
