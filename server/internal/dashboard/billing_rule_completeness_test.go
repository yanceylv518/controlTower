package dashboard

import (
	"strings"
	"testing"
	"time"

	"controltower/server/internal/billing"
)

func screenshotPriceRule(image string) string {
	return ordinaryRulePrefix + "输入 20.000000；输出 100.000000；缓存读取 2.000000；缓存写入 未记录；5m 写入 未记录；1h 写入 未记录；图像输入 " + image + "\n分组倍率：1"
}

func TestRuleCompletenessDoesNotInventPriceChanges(t *testing.T) {
	day := time.Date(2026, 8, 8, 0, 0, 0, 0, billing.BusinessLocation)
	key := statementPriceKey{Day: "2026-08-08", Model: "kimi-k3"}
	prices := statementPrices{key: map[statementPriceTuple]bool{
		{"20", "100", "2", "未记录", screenshotPriceRule("20.000000")}: true,
		{"20", "100", "2", "未记录", screenshotPriceRule("未记录")}:       true,
	}}
	got := prices.rules(billing.Job{UsageVersion: 2, JobType: "user_statement"}, billing.StatementAggregateRow{AggregateRow: billing.AggregateRow{Day: day, ModelName: "kimi-k3"}})
	if strings.Contains(got, "2 套") || !strings.Contains(got, "图像输入 20.000000") || !strings.Contains(got, "历史记录未区分无用量与单价缺失：图像输入") {
		t.Fatal(got)
	}
	if len(prices[key]) != 2 {
		t.Fatal("must not mutate cached source evidence")
	}
}

func TestRuleCompletenessPreservesActualChangesAndUnknowns(t *testing.T) {
	for _, tc := range []struct {
		name      string
		rules     []string
		count     int
		ambiguous bool
	}{
		{"different image prices", []string{screenshotPriceRule("20"), screenshotPriceRule("30")}, 2, false},
		{"free is known", []string{screenshotPriceRule("0"), screenshotPriceRule("20")}, 2, false},
		{"partial cannot bridge", []string{screenshotPriceRule("20"), screenshotPriceRule("未记录"), screenshotPriceRule("30")}, 3, true},
		{"tier context", []string{screenshotPriceRule("20") + "\n命中档位：a", screenshotPriceRule("未记录") + "\n命中档位：b"}, 2, false},
		{"expressions", []string{"表达式：p*20", "表达式：p*30"}, 2, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, ambiguous := groupRuleCompleteness(tc.rules)
			if len(got) != tc.count || ambiguous != tc.ambiguous {
				t.Fatalf("%v %v", got, ambiguous)
			}
		})
	}
}

func TestRuleCompletenessOptionalUsage(t *testing.T) {
	for _, tc := range []struct{ value, warning string }{
		{"不适用（无用量）", ""},
		{"有用量但未记录", "部分订单有对应计费用量但未记录单价：图像输入"},
	} {
		got, ambiguous := groupRuleCompleteness([]string{screenshotPriceRule("20"), screenshotPriceRule(tc.value)})
		if len(got) != 1 || ambiguous {
			t.Fatalf("%v %v", got, ambiguous)
		}
		if tc.warning == "" {
			if strings.Contains(got[0], "部分订单") || strings.Contains(got[0], "历史记录未区分") {
				t.Fatal(got)
			}
		} else if !strings.Contains(got[0], tc.warning) {
			t.Fatal(got)
		}
	}
}
