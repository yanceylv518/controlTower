package billing

import (
	"encoding/json"
	"fmt"
	"math/big"
	"sort"
	"strconv"
	"time"
)

type AggregateRow struct {
	InstanceID                                                       string
	UserID                                                           int64
	Username, ModelName, GroupName                                   string
	TierFrom                                                         int64
	Day                                                              time.Time
	RequestCount, PromptTokens, CompletionTokens, CacheTokens, Quota int64
}

type UserSummary struct {
	UserID           int64    `json:"user_id"`
	Username         string   `json:"username"`
	RequestCount     int64    `json:"request_count"`
	PromptTokens     int64    `json:"prompt_tokens"`
	CompletionTokens int64    `json:"completion_tokens"`
	CacheTokens      int64    `json:"cache_tokens"`
	Quota            int64    `json:"quota"`
	Amount           string   `json:"amount"`
	Balance          int64    `json:"balance"`
	UnpricedModels   []string `json:"unpriced_models"`
	PriceSources     []string `json:"price_sources"`
}

type DetailItem struct {
	Day              string `json:"day"`
	ModelName        string `json:"model_name"`
	GroupName        string `json:"group_name"`
	TierFrom         int64  `json:"tier_from"`
	RequestCount     int64  `json:"request_count"`
	PromptTokens     int64  `json:"prompt_tokens"`
	CompletionTokens int64  `json:"completion_tokens"`
	CacheTokens      int64  `json:"cache_tokens"`
	Quota            int64  `json:"quota"`
	Amount           string `json:"amount"`
	PriceSource      string `json:"price_source"`
	Unpriced         bool   `json:"unpriced"`
}
type SummaryTotal struct {
	Users            int    `json:"users"`
	RequestCount     int64  `json:"request_count"`
	PromptTokens     int64  `json:"prompt_tokens"`
	CompletionTokens int64  `json:"completion_tokens"`
	CacheTokens      int64  `json:"cache_tokens"`
	Quota            int64  `json:"quota"`
	Amount           string `json:"amount"`
}

func SummarizeUsers(items []UserSummary) SummaryTotal {
	total := SummaryTotal{Users: len(items)}
	amount := new(big.Rat)
	for _, item := range items {
		total.RequestCount += item.RequestCount
		total.PromptTokens += item.PromptTokens
		total.CompletionTokens += item.CompletionTokens
		total.CacheTokens += item.CacheTokens
		total.Quota += item.Quota
		if value, err := decimalRat(item.Amount); err == nil {
			amount.Add(amount, value)
		}
	}
	total.Amount = FormatAmount(amount, 6)
	return total
}

type RatioSnapshot struct {
	ModelRatio, CompletionRatio, CacheRatio, GroupRatio map[string]string
	QuotaPerUnit                                        string
}

func ParseRatioSnapshot(raw string) (RatioSnapshot, error) {
	var outer map[string]json.RawMessage
	if err := json.Unmarshal([]byte(raw), &outer); err != nil {
		return RatioSnapshot{}, err
	}
	result := RatioSnapshot{ModelRatio: map[string]string{}, CompletionRatio: map[string]string{}, CacheRatio: map[string]string{}, GroupRatio: map[string]string{}}
	for key, target := range map[string]*map[string]string{"ModelRatio": &result.ModelRatio, "CompletionRatio": &result.CompletionRatio, "CacheRatio": &result.CacheRatio, "GroupRatio": &result.GroupRatio} {
		value := outer[key]
		if len(value) == 0 {
			continue
		}
		var encoded string
		if json.Unmarshal(value, &encoded) == nil {
			value = []byte(encoded)
		}
		var numbers map[string]any
		if json.Unmarshal(value, &numbers) != nil {
			continue
		}
		for name, v := range numbers {
			switch x := v.(type) {
			case float64:
				(*target)[name] = strconv.FormatFloat(x, 'f', -1, 64)
			case string:
				(*target)[name] = x
			}
		}
	}
	if rawValue := outer["QuotaPerUnit"]; len(rawValue) > 0 {
		if json.Unmarshal(rawValue, &result.QuotaPerUnit) != nil {
			var n json.Number
			if json.Unmarshal(rawValue, &n) == nil {
				result.QuotaPerUnit = n.String()
			}
		}
	}
	if result.QuotaPerUnit == "" {
		return result, fmt.Errorf("QuotaPerUnit missing")
	}
	return result, nil
}

func FallbackPrice(snapshot RatioSnapshot, model, group string) (Price, string, error) {
	modelRatio, ok := snapshot.ModelRatio[model]
	if !ok {
		return Price{}, "", fmt.Errorf("ModelRatio missing for %s", model)
	}
	mr, e := decimalRat(modelRatio)
	if e != nil {
		return Price{}, "", e
	}
	q, e := decimalRat(snapshot.QuotaPerUnit)
	if e != nil || q.Sign() == 0 {
		return Price{}, "", fmt.Errorf("invalid QuotaPerUnit")
	}
	input := new(big.Rat).Quo(new(big.Rat).Mul(mr, big.NewRat(tokensPerMillion, 1)), q)
	completion := snapshot.CompletionRatio[model]
	if completion == "" {
		completion = "1"
	}
	cr, e := decimalRat(completion)
	if e != nil {
		return Price{}, "", e
	}
	cache := snapshot.CacheRatio[model]
	if cache == "" {
		cache = "1"
	}
	car, e := decimalRat(cache)
	if e != nil {
		return Price{}, "", e
	}
	groupRatio := snapshot.GroupRatio[group]
	if groupRatio == "" {
		groupRatio = "1"
	}
	return Price{Input: input.FloatString(12), Output: new(big.Rat).Mul(input, cr).FloatString(12), Cache: new(big.Rat).Mul(input, car).FloatString(12)}, groupRatio, nil
}

func BuildSummary(rows []AggregateRow, prices []PriceRecord, ratios []GroupRatio, snapshots map[string]string, balances map[int64]int64) ([]UserSummary, SummaryTotal) {
	priceByModel := map[string][]Price{}
	for _, p := range prices {
		priceByModel[p.ModelName] = append(priceByModel[p.ModelName], p.Price)
	}
	ratioByGroup := map[string]string{}
	for _, r := range ratios {
		ratioByGroup[r.GroupName] = r.Ratio
	}
	type acc struct {
		value             UserSummary
		amount            *big.Rat
		unpriced, sources map[string]bool
	}
	users := map[int64]*acc{}
	for _, row := range rows {
		a := users[row.UserID]
		if a == nil {
			a = &acc{value: UserSummary{UserID: row.UserID, Username: row.Username, Balance: balances[row.UserID]}, amount: new(big.Rat), unpriced: map[string]bool{}, sources: map[string]bool{}}
			users[row.UserID] = a
		}
		a.value.RequestCount += row.RequestCount
		a.value.PromptTokens += row.PromptTokens
		a.value.CompletionTokens += row.CompletionTokens
		a.value.CacheTokens += row.CacheTokens
		a.value.Quota += row.Quota
		price, ok := selectPriceForTier(priceByModel[row.ModelName], row.Day, row.TierFrom)
		ratio, source := "1", "ct"
		if ok {
			if configured := ratioByGroup[row.GroupName]; configured != "" {
				ratio = configured
			}
		} else {
			snap, e := ParseRatioSnapshot(snapshots[row.Day.Format("2006-01-02")])
			if e == nil {
				price, ratio, e = FallbackPrice(snap, row.ModelName, row.GroupName)
			}
			if e != nil {
				a.unpriced[row.ModelName] = true
				continue
			}
			source = "newapi"
		}
		amount, e := Amount(Usage{PromptTokens: row.PromptTokens, CompletionTokens: row.CompletionTokens, CacheTokens: row.CacheTokens}, price, ratio)
		if e != nil {
			a.unpriced[row.ModelName] = true
			continue
		}
		a.amount.Add(a.amount, amount)
		a.sources[source] = true
	}
	out := make([]UserSummary, 0, len(users))
	total := SummaryTotal{Users: len(users)}
	totalAmount := new(big.Rat)
	for _, a := range users {
		for m := range a.unpriced {
			a.value.UnpricedModels = append(a.value.UnpricedModels, m)
		}
		for s := range a.sources {
			a.value.PriceSources = append(a.value.PriceSources, s)
		}
		sort.Strings(a.value.UnpricedModels)
		sort.Strings(a.value.PriceSources)
		a.value.Amount = FormatAmount(a.amount, 6)
		out = append(out, a.value)
		total.RequestCount += a.value.RequestCount
		total.PromptTokens += a.value.PromptTokens
		total.CompletionTokens += a.value.CompletionTokens
		total.CacheTokens += a.value.CacheTokens
		total.Quota += a.value.Quota
		totalAmount.Add(totalAmount, a.amount)
	}
	sort.Slice(out, func(i, j int) bool {
		ai, _ := decimalRat(out[i].Amount)
		aj, _ := decimalRat(out[j].Amount)
		cmp := ai.Cmp(aj)
		if cmp == 0 {
			return out[i].UserID < out[j].UserID
		}
		return cmp > 0
	})
	total.Amount = FormatAmount(totalAmount, 6)
	return out, total
}

func BuildDetails(rows []AggregateRow, prices []PriceRecord, ratios []GroupRatio, snapshots map[string]string) []DetailItem {
	priceByModel := map[string][]Price{}
	for _, price := range prices {
		priceByModel[price.ModelName] = append(priceByModel[price.ModelName], price.Price)
	}
	ratioByGroup := map[string]string{}
	for _, ratio := range ratios {
		ratioByGroup[ratio.GroupName] = ratio.Ratio
	}
	items := make([]DetailItem, 0, len(rows))
	for _, row := range rows {
		item := DetailItem{Day: row.Day.Format("2006-01-02"), ModelName: row.ModelName, GroupName: row.GroupName, TierFrom: row.TierFrom, RequestCount: row.RequestCount, PromptTokens: row.PromptTokens, CompletionTokens: row.CompletionTokens, CacheTokens: row.CacheTokens, Quota: row.Quota}
		price, ok := selectPriceForTier(priceByModel[row.ModelName], row.Day, row.TierFrom)
		ratio := "1"
		item.PriceSource = "ct"
		if ok {
			if configured := ratioByGroup[row.GroupName]; configured != "" {
				ratio = configured
			}
		} else {
			snapshot, err := ParseRatioSnapshot(snapshots[item.Day])
			if err == nil {
				price, ratio, err = FallbackPrice(snapshot, row.ModelName, row.GroupName)
			}
			if err != nil {
				item.Unpriced = true
				items = append(items, item)
				continue
			}
			item.PriceSource = "newapi"
		}
		amount, err := Amount(Usage{PromptTokens: row.PromptTokens, CompletionTokens: row.CompletionTokens, CacheTokens: row.CacheTokens}, price, ratio)
		if err != nil {
			item.Unpriced = true
		} else {
			item.Amount = FormatAmount(amount, 6)
		}
		items = append(items, item)
	}
	sort.Slice(items, func(i, j int) bool {
		if items[i].Day != items[j].Day {
			return items[i].Day > items[j].Day
		}
		if items[i].ModelName != items[j].ModelName {
			return items[i].ModelName < items[j].ModelName
		}
		return items[i].GroupName < items[j].GroupName
	})
	return items
}

func selectPriceForTier(prices []Price, day time.Time, tier int64) (Price, bool) {
	var effective time.Time
	var selected Price
	ok := false
	for _, p := range prices {
		if p.TierFrom == tier && !p.EffectiveFrom.After(day) && (effective.IsZero() || p.EffectiveFrom.After(effective)) {
			effective = p.EffectiveFrom
			selected = p
			ok = true
		}
	}
	return selected, ok
}
