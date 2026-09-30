package billing

import (
	"encoding/json"
	"math/big"
	"sort"
	"strings"
)

// Flat set permits idempotent JSON_MERGE_PATCH across pages without storing
// expressions or averaging distinct historical rates. Values are before the
// settlement discount, in the bill's base currency per million tokens (/call).
type UnitPrices map[string]bool

func (p *UnitPrices) Merge(other UnitPrices) {
	if *p == nil {
		*p = UnitPrices{}
	}
	if len(other) == 0 {
		(*p)["unknown|未记录"] = true
		return
	}
	for key, present := range other {
		if present {
			(*p)[key] = true
		}
	}
}

func ParseUnitPriceGroups(raw []byte) (UnitPrices, error) {
	var groups []UnitPrices
	if err := json.Unmarshal(raw, &groups); err != nil {
		return nil, err
	}
	var out UnitPrices
	for _, group := range groups {
		out.Merge(group)
	}
	return out, nil
}

func SimpleUnitPrices(c LogCharge) UnitPrices {
	out := UnitPrices{}
	for _, v := range []struct{ key, value string }{
		{"输入", c.InputPrice}, {"输出", c.OutputPrice}, {"缓存读取", c.CacheReadPrice},
		{"缓存写入", c.CacheWritePrice}, {"5m写入", c.CacheWrite5mPrice}, {"1h写入", c.CacheWrite1hPrice},
		{"图像输入", c.ImagePrice}, {"按次", c.PerRequestPrice},
	} {
		if strings.HasPrefix(v.value, "不适用") {
			continue
		}
		if n, ok := new(big.Rat).SetString(v.value); ok && n.Sign() >= 0 {
			out[v.key+"|"+n.RatString()] = true
		} else if v.key == "输入" || v.key == "输出" || strings.Contains(v.value, "有用量") {
			out[v.key+"|未记录"] = true
		}
	}
	return out
}

func UnitPriceLabel(prices UnitPrices, rate *big.Rat) string {
	if len(prices) == 0 {
		return "未记录"
	}
	r := rate
	if r == nil || r.Sign() <= 0 {
		return "未记录"
	}
	parts := []string{}
	for _, kind := range []string{"输入", "输出", "缓存读取", "缓存写入", "5m写入", "1h写入", "图像输入", "图像输出", "音频输入", "音频输出", "按次", "unknown"} {
		values := map[string]bool{}
		for key, present := range prices {
			prefix, value, found := strings.Cut(key, "|")
			if !found || !present || prefix != kind {
				continue
			}
			if n, ok := new(big.Rat).SetString(value); ok && n.Sign() >= 0 {
				value = strings.TrimRight(strings.TrimRight(new(big.Rat).Mul(n, r).FloatString(10), "0"), ".")
				if value == "" {
					value = "0"
				}
			}
			values[value] = true
		}
		list := []string{}
		for value := range values {
			list = append(list, value)
		}
		sort.Slice(list, func(i, j int) bool {
			a, aok := new(big.Rat).SetString(list[i])
			b, bok := new(big.Rat).SetString(list[j])
			if aok && bok {
				return a.Cmp(b) < 0
			}
			if aok != bok {
				return aok
			}
			return list[i] < list[j]
		})
		if len(list) > 0 {
			if kind == "unknown" {
				if len(parts) == 0 {
					parts = append(parts, "未记录")
				} else {
					parts = append(parts, "部分未记录")
				}
			} else {
				suffix := ""
				if kind == "按次" {
					suffix = "/次"
				}
				parts = append(parts, kind+" "+strings.Join(list, " / ")+suffix)
			}
		}
	}
	if len(parts) == 0 {
		return "未记录"
	}
	return strings.Join(parts, "；")
}

func UnitPriceHeader(currency string) string {
	return "模型单价（" + currency + "/百万 Token；按次另标）"
}

func attachSimplePrices(job Job, log PagedLogRecord, qpu string, c *LogCharge) {
	if job.UsageVersion < SettlementUsageVersion {
		return
	}
	price := *c
	if log.BillingMode == "tiered_expr" || log.ExprBase64 != "" {
		if calculated, err := calculateTieredExprCharge(log, qpu); err == nil {
			price = calculated
		}
	}
	c.UnitPrices = SimpleUnitPrices(price)
	if _, ok := new(big.Rat).SetString(price.PerRequestPrice); ok {
		return
	}
	// Do not silently omit a used lane whose historical unit price is absent.
	for _, lane := range []struct {
		key, price string
		usage      int64
	}{
		{"缓存读取", price.CacheReadPrice, log.CacheTokens},
		{"缓存写入", price.CacheWritePrice, log.CacheWriteTokens - log.CacheWrite5mTokens - log.CacheWrite1hTokens},
		{"5m写入", price.CacheWrite5mPrice, log.CacheWrite5mTokens},
		{"1h写入", price.CacheWrite1hPrice, log.CacheWrite1hTokens},
		{"图像输入", price.ImagePrice, log.ImageInputTokens},
		{"图像输出", "", log.ImageOutputTokens},
		{"音频输入", "", log.AudioInputTokens},
		{"音频输出", "", log.AudioOutputTokens},
	} {
		if _, ok := new(big.Rat).SetString(lane.price); lane.usage > 0 && !ok {
			c.UnitPrices[lane.key+"|未记录"] = true
		}
	}
}

func unitPriceOrUnknown(v string) string {
	if v == "" {
		return "未记录"
	}
	return v
}
