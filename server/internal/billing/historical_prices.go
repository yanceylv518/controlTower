package billing

import (
	"encoding/base64"
	"fmt"
	"math/big"
	"strings"
)

// Historical prices describe the source snapshot only. In particular we never
// execute an expression or change the logged amount to produce display prices.
const HistoricalPriceUsageVersion = 2

func attachHistoricalPrices(job Job, log PagedLogRecord, quotaPerUnit string, charge *LogCharge) {
	if job.UsageVersion < HistoricalPriceUsageVersion {
		return
	}
	charge.MatchedTier = log.MatchedTier
	charge.InputPrice, charge.OutputPrice, charge.CacheReadPrice = "未记录", "未记录", "未记录"
	charge.CacheWritePrice, charge.CacheWrite5mPrice, charge.CacheWrite1hPrice = "未记录", "未记录", "未记录"
	charge.ImagePrice, charge.PerRequestPrice = "未记录", "未记录"
	parts := []string{}
	if log.BillingMode == "tiered_expr" || log.ExprBase64 != "" {
		charge.InputPrice, charge.OutputPrice, charge.CacheReadPrice = "见计价规则", "见计价规则", "见计价规则"
		charge.CacheWritePrice, charge.CacheWrite5mPrice, charge.CacheWrite1hPrice = "见计价规则", "见计价规则", "见计价规则"
		charge.ImagePrice, charge.PerRequestPrice = "见计价规则", "见计价规则"
		expression, err := base64.StdEncoding.DecodeString(log.ExprBase64)
		if err != nil || len(expression) == 0 {
			parts = append(parts, "表达式计费：历史表达式缺失或无法解码")
		} else {
			parts = append(parts, "表达式："+string(expression))
		}
	} else {
		group, ge := decimalRat(log.GroupRatio)
		qpu, qe := decimalRat(quotaPerUnit)
		model, me := decimalRat(log.ModelRatio)
		perRequest, pe := decimalRat(log.ModelPrice)
		if ge == nil && group.Sign() >= 0 && pe == nil && perRequest.Sign() >= 0 {
			charge.PerRequestPrice = new(big.Rat).Mul(perRequest, group).FloatString(6)
			charge.InputPrice, charge.OutputPrice, charge.CacheReadPrice = "不适用", "不适用", "不适用"
			parts = append(parts, "按次计费："+charge.PerRequestPrice+"/次")
		} else {
			if ge == nil && group.Sign() >= 0 && qe == nil && qpu.Sign() > 0 && me == nil && model.Sign() >= 0 {
				base := new(big.Rat).Quo(new(big.Rat).Mul(new(big.Rat).Mul(model, group), big.NewRat(tokensPerMillion, 1)), qpu)
				charge.InputPrice = base.FloatString(6)
				price := func(raw string) string {
					ratio, err := decimalRat(raw)
					if err != nil || ratio.Sign() < 0 {
						return "未记录"
					}
					return new(big.Rat).Mul(base, ratio).FloatString(6)
				}
				charge.OutputPrice, charge.CacheReadPrice = price(log.CompletionRatio), price(log.CacheRatio)
				charge.CacheWritePrice = price(log.CacheCreationRatio)
				five := log.CacheCreationRatio5m
				if five == "" {
					five = log.CacheCreationRatio
				}
				charge.CacheWrite5mPrice, charge.CacheWrite1hPrice = price(five), price(log.CacheCreationRatio1h)
				charge.ImagePrice = price(log.ImageRatio)
			}
			parts = append(parts, fmt.Sprintf("历史单价（金额/百万 Token，含分组倍率）：输入 %s；输出 %s；缓存读取 %s；缓存写入 %s；5m 写入 %s；1h 写入 %s；图像输入 %s", charge.InputPrice, charge.OutputPrice, charge.CacheReadPrice, charge.CacheWritePrice, charge.CacheWrite5mPrice, charge.CacheWrite1hPrice, charge.ImagePrice))
		}
	}
	for _, item := range []struct{ label, value string }{
		{"分组倍率", log.GroupRatio}, {"命中档位", log.MatchedTier}, {"请求规则", log.RequestRules}, {"工具附加费用快照", log.ToolSurcharges},
	} {
		if item.value != "" {
			parts = append(parts, item.label+"："+item.value)
		}
	}
	if log.AudioInputTokens > 0 || log.AudioOutputTokens > 0 {
		parts = append(parts, "音频分项单价：未单独还原；如为表达式计费请查表达式")
	}
	charge.PricingRule = strings.Join(parts, "\n")
}
