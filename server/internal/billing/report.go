package billing

import (
	"encoding/json"
	"fmt"
	"math/big"
	"sort"
	"strconv"
	"strings"
	"time"
)

const defaultQuotaPerUnit = "500000"

type AggregateRow struct {
	InstanceID                                                       string
	UserID                                                           int64
	Username, ModelName, GroupName                                   string
	TierFrom                                                         int64
	Day                                                              time.Time
	RequestCount, PromptTokens, CompletionTokens, CacheTokens, Quota int64
	CacheWriteTokens, CacheWrite5mTokens, CacheWrite1hTokens         int64
	Amount                                                           string
}

type AnomalyCount struct {
	UserID, ChannelID, TokenID      int64
	Day                             time.Time
	TokenName, ModelName, GroupName string
	Count                           int64
	Amount                          string
}

type UserSummary struct {
	UserID           int64    `json:"user_id"`
	Username         string   `json:"username"`
	RequestCount     int64    `json:"request_count"`
	PromptTokens     int64    `json:"prompt_tokens"`
	CompletionTokens int64    `json:"completion_tokens"`
	CacheTokens      int64    `json:"cache_tokens"`
	CacheWriteTokens int64    `json:"cache_write_tokens"`
	AbnormalRows     int64    `json:"abnormal_rows"`
	AbnormalAmount   string   `json:"abnormal_amount"`
	Quota            int64    `json:"quota"`
	Amount           string   `json:"amount"`
	Balance          int64    `json:"balance"`
	UnpricedModels   []string `json:"unpriced_models"`
	PriceSources     []string `json:"price_sources"`
}

type DetailItem struct {
	Day                string `json:"day"`
	ModelName          string `json:"model_name"`
	GroupName          string `json:"group_name"`
	TierFrom           int64  `json:"tier_from"`
	RequestCount       int64  `json:"request_count"`
	PromptTokens       int64  `json:"prompt_tokens"`
	CompletionTokens   int64  `json:"completion_tokens"`
	CacheTokens        int64  `json:"cache_tokens"`
	CacheWriteTokens   int64  `json:"cache_write_tokens"`
	CacheWrite5mTokens int64  `json:"cache_write_5m_tokens"`
	CacheWrite1hTokens int64  `json:"cache_write_1h_tokens"`
	AbnormalRows       int64  `json:"abnormal_rows"`
	AbnormalAmount     string `json:"abnormal_amount"`
	Quota              int64  `json:"quota"`
	Amount             string `json:"amount"`
	InputPrice         string `json:"input_price"`
	OutputPrice        string `json:"output_price"`
	CachePrice         string `json:"cache_price"`
	CacheWritePrice    string `json:"cache_write_price"`
	PriceSource        string `json:"price_source"`
	Unpriced           bool   `json:"unpriced"`
}

type InvoiceItem struct {
	ModelName        string `json:"model_name"`
	TierFrom         int64  `json:"tier_from"`
	RequestCount     int64  `json:"request_count"`
	PromptTokens     int64  `json:"prompt_tokens"`
	CompletionTokens int64  `json:"completion_tokens"`
	CacheTokens      int64  `json:"cache_tokens"`
	CacheWriteTokens int64  `json:"cache_write_tokens"`
	InputPrice       string `json:"input_price"`
	OutputPrice      string `json:"output_price"`
	CachePrice       string `json:"cache_price"`
	CacheWritePrice  string `json:"cache_write_price"`
	InputAmount      string `json:"input_amount"`
	OutputAmount     string `json:"output_amount"`
	CacheAmount      string `json:"cache_amount"`
	CacheWriteAmount string `json:"cache_write_amount"`
	Amount           string `json:"amount"`
	Discount         string `json:"discount"`
	DiscountedAmount string `json:"discounted_amount"`
	PriceSource      string `json:"price_source"`
	Unpriced         bool   `json:"unpriced"`
}

type InvoiceTotal struct {
	Amount           string `json:"amount"`
	Discount         string `json:"discount"`
	DiscountedAmount string `json:"discounted_amount"`
}
type SummaryTotal struct {
	Users            int    `json:"users"`
	RequestCount     int64  `json:"request_count"`
	PromptTokens     int64  `json:"prompt_tokens"`
	CompletionTokens int64  `json:"completion_tokens"`
	CacheTokens      int64  `json:"cache_tokens"`
	CacheWriteTokens int64  `json:"cache_write_tokens"`
	AbnormalRows     int64  `json:"abnormal_rows"`
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
		total.CacheWriteTokens += item.CacheWriteTokens
		total.Quota += item.Quota
		if value, err := decimalRat(item.Amount); err == nil {
			amount.Add(amount, value)
		}
		total.AbnormalRows += item.AbnormalRows
	}
	total.Amount = FormatAmount(amount, 6)
	return total
}

type RatioSnapshot struct {
	ModelRatio, CompletionRatio, CacheRatio, CreateCacheRatio, GroupRatio map[string]string
	QuotaPerUnit                                                          string
	Currency                                                              CurrencyDisplay
}

type CurrencyDisplay struct {
	Type         string `json:"type"`
	Symbol       string `json:"symbol"`
	ExchangeRate string `json:"exchange_rate"`
}

func ParseRatioSnapshot(raw string) (RatioSnapshot, error) {
	var outer map[string]json.RawMessage
	if err := json.Unmarshal([]byte(raw), &outer); err != nil {
		return RatioSnapshot{}, err
	}
	result := RatioSnapshot{ModelRatio: map[string]string{}, CompletionRatio: map[string]string{}, CacheRatio: map[string]string{}, CreateCacheRatio: map[string]string{}, GroupRatio: map[string]string{}}
	for key, target := range map[string]*map[string]string{"ModelRatio": &result.ModelRatio, "CompletionRatio": &result.CompletionRatio, "CacheRatio": &result.CacheRatio, "CreateCacheRatio": &result.CreateCacheRatio, "GroupRatio": &result.GroupRatio} {
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
	result.Currency = parseCurrencyDisplay(outer)
	return result, nil
}

func parseCurrencyDisplay(outer map[string]json.RawMessage) CurrencyDisplay {
	result := CurrencyDisplay{Type: "USD", Symbol: "$", ExchangeRate: "1"}
	// new-api's config manager persists GeneralSetting as one dotted option
	// key per field with bare values (config.SaveToDB: name+"."+key), NOT as
	// a single JSON blob. The dotted keys are the authoritative source; the
	// blob branch below is kept only as a compatibility fallback.
	if displayType := rawJSONString(outer["general_setting.quota_display_type"]); displayType != "" {
		result.Type = strings.ToUpper(strings.TrimSpace(displayType))
		switch result.Type {
		case "CNY":
			result.Symbol = "¥"
			result.ExchangeRate = rawJSONNumber(outer["USDExchangeRate"], "7.3")
		case "CUSTOM":
			result.Symbol = strings.TrimSpace(rawJSONString(outer["general_setting.custom_currency_symbol"]))
			if result.Symbol == "" {
				result.Symbol = "¤"
			}
			result.ExchangeRate = rawJSONNumber(outer["general_setting.custom_currency_exchange_rate"], "1")
		case "TOKENS":
			result.Symbol = ""
			result.ExchangeRate = "1"
		default:
			result.Type, result.Symbol, result.ExchangeRate = "USD", "$", "1"
		}
		return result
	}
	var encoded string
	if raw := outer["general_setting"]; len(raw) > 0 && json.Unmarshal(raw, &encoded) == nil {
		var setting struct {
			QuotaDisplayType           string      `json:"quota_display_type"`
			CustomCurrencySymbol       string      `json:"custom_currency_symbol"`
			CustomCurrencyExchangeRate json.Number `json:"custom_currency_exchange_rate"`
		}
		if json.Unmarshal([]byte(encoded), &setting) == nil {
			result.Type = strings.ToUpper(strings.TrimSpace(setting.QuotaDisplayType))
			switch result.Type {
			case "CNY":
				result.Symbol = "¥"
				result.ExchangeRate = rawJSONNumber(outer["USDExchangeRate"], "1")
			case "CUSTOM":
				result.Symbol = strings.TrimSpace(setting.CustomCurrencySymbol)
				if result.Symbol == "" {
					result.Symbol = "¤"
				}
				result.ExchangeRate = setting.CustomCurrencyExchangeRate.String()
				if result.ExchangeRate == "" {
					result.ExchangeRate = "1"
				}
			case "TOKENS":
				result.Symbol = ""
				result.ExchangeRate = "1"
			default:
				result.Type, result.Symbol, result.ExchangeRate = "USD", "$", "1"
			}
			return result
		}
	}
	// Older new-api versions only expose this compatibility flag. Currency
	// display meant USD there; disabled means raw quota/tokens.
	if raw := outer["DisplayInCurrencyEnabled"]; len(raw) > 0 {
		var value string
		if json.Unmarshal(raw, &value) == nil && strings.EqualFold(strings.TrimSpace(value), "false") {
			return CurrencyDisplay{Type: "TOKENS", Symbol: "", ExchangeRate: "1"}
		}
	}
	return result
}

func rawJSONString(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var value string
	if json.Unmarshal(raw, &value) == nil {
		return value
	}
	return ""
}

func rawJSONNumber(raw json.RawMessage, fallback string) string {
	var value string
	if json.Unmarshal(raw, &value) == nil && strings.TrimSpace(value) != "" {
		return value
	}
	var number json.Number
	if json.Unmarshal(raw, &number) == nil && number.String() != "" {
		return number.String()
	}
	return fallback
}

// CurrencyDisplayForSnapshots returns the site display currency captured from
// new-api. The newest day wins because settings can change between bill runs.
func CurrencyDisplayForSnapshots(snapshots map[string]string) CurrencyDisplay {
	keys := make([]string, 0, len(snapshots))
	for key := range snapshots {
		keys = append(keys, key)
	}
	sort.Sort(sort.Reverse(sort.StringSlice(keys)))
	for _, key := range keys {
		if snapshot, err := ParseRatioSnapshot(snapshots[key]); err == nil {
			return snapshot.Currency
		}
	}
	return CurrencyDisplay{}
}

func quotaPerUnitForReport(raw string) (string, error) {
	if strings.TrimSpace(raw) == "" {
		return defaultQuotaPerUnit, nil
	}
	snapshot, err := ParseRatioSnapshot(raw)
	if err != nil {
		if err.Error() == "QuotaPerUnit missing" {
			return defaultQuotaPerUnit, nil
		}
		return "", err
	}
	return snapshot.QuotaPerUnit, nil
}

// AmountFromQuota converts new-api's final deducted quota into the configured
// currency amount. The quota already contains model, completion, cache and
// group multipliers, so reconstructing those multipliers would be both less
// accurate and unable to handle new-api's built-in defaults.
func AmountFromQuota(quota int64, quotaPerUnit string) (*big.Rat, error) {
	unit, err := decimalRat(quotaPerUnit)
	if err != nil || unit.Sign() == 0 {
		return nil, fmt.Errorf("invalid QuotaPerUnit")
	}
	if quota < 0 {
		quota = 0
	}
	return new(big.Rat).Quo(new(big.Rat).SetInt64(quota), unit), nil
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
		completion = defaultCompletionRatio(model)
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
	cacheWrite := snapshot.CreateCacheRatio[model]
	if cacheWrite == "" {
		// new-api's GetCreateCacheRatio falls back to 1.25 when a model has
		// no explicit CreateCacheRatio entry. Runtime billing ignores the
		// accompanying "configured" boolean and charges this effective rate.
		cacheWrite = "1.25"
	}
	cwr, e := decimalRat(cacheWrite)
	if e != nil {
		return Price{}, "", e
	}
	groupRatio := snapshot.GroupRatio[group]
	if groupRatio == "" {
		groupRatio = "1"
	}
	return Price{Input: input.FloatString(12), Output: new(big.Rat).Mul(input, cr).FloatString(12), Cache: new(big.Rat).Mul(input, car).FloatString(12), CacheWrite: new(big.Rat).Mul(input, cwr).FloatString(12)}, groupRatio, nil
}

// defaultCompletionRatio mirrors new-api's built-in completion multipliers.
// Those values are resolved in new-api code and therefore are absent from the
// options.CompletionRatio JSON returned by a read-only database connection.
func defaultCompletionRatio(model string) string {
	name := strings.ToLower(strings.TrimSpace(model))
	switch {
	case strings.HasPrefix(name, "gpt-4o-mini-tts"):
		return "20"
	case strings.HasPrefix(name, "gpt-4o"):
		return "4"
	case strings.HasPrefix(name, "gpt-5.4-nano"):
		return "6.25"
	case strings.HasPrefix(name, "gpt-5.4"), strings.HasPrefix(name, "gpt-5.5"):
		return "6"
	case strings.HasPrefix(name, "gpt-5") && !strings.Contains(name, "."):
		return "8"
	case strings.Contains(name, "claude-3"), strings.Contains(name, "claude-sonnet-4"), strings.Contains(name, "claude-opus-4"), strings.Contains(name, "claude-haiku-4"):
		return "5"
	case strings.HasPrefix(name, "o1"), strings.HasPrefix(name, "o3"):
		return "4"
	case strings.HasPrefix(name, "gpt-4.5-preview"):
		return "2"
	case strings.HasPrefix(name, "gpt-4-turbo"):
		return "3"
	case strings.HasPrefix(name, "gpt-4"):
		return "2"
	default:
		return "1"
	}
}

// priceWithSnapshotCacheWrite keeps CT's stored price schedule aligned with
// the new-api configuration frozen for the generated bill. Historical CT
// rows may contain the old zero write price; when the snapshot has no
// explicit CreateCacheRatio, new-api still charges input price * 1.25.
func priceWithSnapshotCacheWrite(price Price, raw, model string) Price {
	if strings.TrimSpace(raw) == "" {
		return price
	}
	snapshot, err := ParseRatioSnapshot(raw)
	if err != nil {
		return price
	}
	if _, configured := snapshot.CreateCacheRatio[model]; !configured {
		input, inputErr := decimalRat(price.Input)
		if inputErr == nil {
			price.CacheWrite = new(big.Rat).Mul(input, big.NewRat(5, 4)).FloatString(12)
		}
	}
	return price
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
		a.value.CacheWriteTokens += row.CacheWriteTokens
		a.value.Quota += row.Quota
		if strings.TrimSpace(row.Amount) != "" {
			amount, amountErr := decimalRat(row.Amount)
			if amountErr != nil {
				a.unpriced[row.ModelName] = true
				continue
			}
			a.amount.Add(a.amount, amount)
			a.sources["request_log"] = true
			continue
		}
		price, ok := selectPriceForTier(priceByModel[row.ModelName], row.Day, row.TierFrom)
		ratio, source := "1", "ct"
		if ok {
			price = priceWithSnapshotCacheWrite(price, snapshots[row.Day.Format("2006-01-02")], row.ModelName)
			if configured := ratioByGroup[row.GroupName]; configured != "" {
				ratio = configured
			}
		} else {
			quotaPerUnit, e := quotaPerUnitForReport(snapshots[row.Day.Format("2006-01-02")])
			if e != nil {
				a.unpriced[row.ModelName] = true
				continue
			}
			amount, e := AmountFromQuota(row.Quota, quotaPerUnit)
			if e != nil {
				a.unpriced[row.ModelName] = true
				continue
			}
			a.amount.Add(a.amount, amount)
			a.sources["newapi"] = true
			continue
		}
		amount, e := Amount(usageFromAggregate(row), price, ratio)
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
		total.CacheWriteTokens += a.value.CacheWriteTokens
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
		item := DetailItem{Day: row.Day.Format("2006-01-02"), ModelName: row.ModelName, GroupName: row.GroupName, TierFrom: row.TierFrom, RequestCount: row.RequestCount, PromptTokens: row.PromptTokens, CompletionTokens: row.CompletionTokens, CacheTokens: row.CacheTokens, CacheWriteTokens: row.CacheWriteTokens, CacheWrite5mTokens: row.CacheWrite5mTokens, CacheWrite1hTokens: row.CacheWrite1hTokens, Quota: row.Quota}
		if strings.TrimSpace(row.Amount) != "" {
			item.Amount = row.Amount
			item.PriceSource = "request_log"
			items = append(items, item)
			continue
		}
		price, ok := selectPriceForTier(priceByModel[row.ModelName], row.Day, row.TierFrom)
		ratio := "1"
		item.PriceSource = "ct"
		if ok {
			price = priceWithSnapshotCacheWrite(price, snapshots[item.Day], row.ModelName)
			if configured := ratioByGroup[row.GroupName]; configured != "" {
				ratio = configured
			}
		} else {
			if snapshot, snapshotErr := ParseRatioSnapshot(snapshots[item.Day]); snapshotErr == nil {
				if fallback, fallbackRatio, fallbackErr := FallbackPrice(snapshot, row.ModelName, row.GroupName); fallbackErr == nil {
					price, ratio = fallback, fallbackRatio
				}
			}
			quotaPerUnit, err := quotaPerUnitForReport(snapshots[item.Day])
			if err != nil {
				item.Unpriced = true
				items = append(items, item)
				continue
			}
			item.PriceSource = "newapi"
			item.InputPrice, _ = multipliedDecimal(price.Input, ratio)
			item.OutputPrice, _ = multipliedDecimal(price.Output, ratio)
			item.CachePrice, _ = multipliedDecimal(price.Cache, ratio)
			item.CacheWritePrice, _ = multipliedDecimal(decimalOrZero(price.CacheWrite), ratio)
			amount, err := AmountFromQuota(row.Quota, quotaPerUnit)
			if err != nil {
				item.Unpriced = true
			} else {
				item.Amount = FormatAmount(amount, 6)
			}
			items = append(items, item)
			continue
		}
		item.InputPrice, _ = multipliedDecimal(price.Input, ratio)
		item.OutputPrice, _ = multipliedDecimal(price.Output, ratio)
		item.CachePrice, _ = multipliedDecimal(price.Cache, ratio)
		item.CacheWritePrice, _ = multipliedDecimal(decimalOrZero(price.CacheWrite), ratio)
		amount, err := Amount(usageFromAggregate(row), price, ratio)
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

// BuildInvoice produces monthly model totals suitable for a customer invoice.
// Rows with different tiers or effective unit prices remain separate so a
// price change can never be hidden inside an apparently single-price row.
func BuildInvoice(rows []AggregateRow, prices []PriceRecord, ratios []GroupRatio, snapshots map[string]string, discount string) ([]InvoiceItem, InvoiceTotal, error) {
	discountRat, err := decimalRat(discount)
	if err != nil || discountRat.Cmp(big.NewRat(1, 1)) > 0 {
		return nil, InvoiceTotal{}, fmt.Errorf("invalid discount")
	}
	priceByModel := map[string][]Price{}
	for _, price := range prices {
		priceByModel[price.ModelName] = append(priceByModel[price.ModelName], price.Price)
	}
	ratioByGroup := map[string]string{}
	for _, ratio := range ratios {
		ratioByGroup[ratio.GroupName] = ratio.Ratio
	}
	type invoiceAcc struct {
		item                                    InvoiceItem
		input, output, cache, cacheWrite, total *big.Rat
	}
	itemsByKey := map[string]*invoiceAcc{}
	for _, row := range rows {
		if strings.TrimSpace(row.Amount) != "" {
			key := strings.Join([]string{row.ModelName, "request_log"}, "\x00")
			a := itemsByKey[key]
			if a == nil {
				a = &invoiceAcc{item: InvoiceItem{ModelName: row.ModelName, Discount: discount, PriceSource: "request_log"}, input: new(big.Rat), output: new(big.Rat), cache: new(big.Rat), cacheWrite: new(big.Rat), total: new(big.Rat)}
				itemsByKey[key] = a
			}
			a.item.RequestCount += row.RequestCount
			a.item.PromptTokens += row.PromptTokens
			a.item.CompletionTokens += row.CompletionTokens
			a.item.CacheTokens += row.CacheTokens
			a.item.CacheWriteTokens += row.CacheWriteTokens
			amount, amountErr := decimalRat(row.Amount)
			if amountErr != nil {
				return nil, InvoiceTotal{}, amountErr
			}
			a.total.Add(a.total, amount)
			continue
		}
		price, ok := selectPriceForTier(priceByModel[row.ModelName], row.Day, row.TierFrom)
		ratio, source := "1", "ct"
		unpriced := false
		if ok {
			price = priceWithSnapshotCacheWrite(price, snapshots[row.Day.Format("2006-01-02")], row.ModelName)
			if configured := ratioByGroup[row.GroupName]; configured != "" {
				ratio = configured
			}
		} else {
			snapshot, parseErr := ParseRatioSnapshot(snapshots[row.Day.Format("2006-01-02")])
			if parseErr == nil {
				price, ratio, parseErr = FallbackPrice(snapshot, row.ModelName, row.GroupName)
			}
			if parseErr != nil {
				unpriced = true
				price = Price{Input: "0", Output: "0", Cache: "0", CacheWrite: "0"}
				ratio = "1"
			}
			source = "newapi"
		}
		inputUnit, unitErr := multipliedDecimal(price.Input, ratio)
		if unitErr != nil {
			return nil, InvoiceTotal{}, unitErr
		}
		outputUnit, unitErr := multipliedDecimal(price.Output, ratio)
		if unitErr != nil {
			return nil, InvoiceTotal{}, unitErr
		}
		cacheUnit, unitErr := multipliedDecimal(price.Cache, ratio)
		if unitErr != nil {
			return nil, InvoiceTotal{}, unitErr
		}
		cacheWriteUnit, unitErr := multipliedDecimal(decimalOrZero(price.CacheWrite), ratio)
		if unitErr != nil {
			return nil, InvoiceTotal{}, unitErr
		}
		key := strings.Join([]string{row.ModelName, strconv.FormatInt(row.TierFrom, 10), inputUnit, outputUnit, cacheUnit, cacheWriteUnit, source, strconv.FormatBool(unpriced)}, "\x00")
		a := itemsByKey[key]
		if a == nil {
			a = &invoiceAcc{item: InvoiceItem{ModelName: row.ModelName, TierFrom: row.TierFrom, InputPrice: inputUnit, OutputPrice: outputUnit, CachePrice: cacheUnit, CacheWritePrice: cacheWriteUnit, Discount: discount, PriceSource: source, Unpriced: unpriced}, input: new(big.Rat), output: new(big.Rat), cache: new(big.Rat), cacheWrite: new(big.Rat), total: new(big.Rat)}
			itemsByKey[key] = a
		}
		a.item.RequestCount += row.RequestCount
		a.item.PromptTokens += row.PromptTokens
		a.item.CompletionTokens += row.CompletionTokens
		a.item.CacheTokens += row.CacheTokens
		a.item.CacheWriteTokens += row.CacheWriteTokens
		if unpriced {
			quotaPerUnit, quotaErr := quotaPerUnitForReport(snapshots[row.Day.Format("2006-01-02")])
			if quotaErr != nil {
				continue
			}
			quotaAmount, quotaErr := AmountFromQuota(row.Quota, quotaPerUnit)
			if quotaErr == nil {
				a.total.Add(a.total, quotaAmount)
			}
			continue
		}
		inputPrice, _ := decimalRat(inputUnit)
		outputPrice, _ := decimalRat(outputUnit)
		cachePrice, _ := decimalRat(cacheUnit)
		cacheWritePrice, _ := decimalRat(cacheWriteUnit)
		a.input.Add(a.input, tokenCost(row.PromptTokens, inputPrice))
		a.output.Add(a.output, tokenCost(row.CompletionTokens, outputPrice))
		a.cache.Add(a.cache, tokenCost(row.CacheTokens, cachePrice))
		remainingWrite := row.CacheWriteTokens - row.CacheWrite5mTokens - row.CacheWrite1hTokens
		if remainingWrite < 0 {
			remainingWrite = 0
		}
		a.cacheWrite.Add(a.cacheWrite, tokenCost(row.CacheWrite5mTokens+remainingWrite, cacheWritePrice))
		a.cacheWrite.Add(a.cacheWrite, tokenCost(row.CacheWrite1hTokens, new(big.Rat).Mul(cacheWritePrice, big.NewRat(8, 5))))
	}
	out := make([]InvoiceItem, 0, len(itemsByKey))
	grand := new(big.Rat)
	for _, a := range itemsByKey {
		if !a.item.Unpriced {
			a.total.Add(a.total, a.input)
			a.total.Add(a.total, a.output)
			a.total.Add(a.total, a.cache)
			a.total.Add(a.total, a.cacheWrite)
		}
		a.item.InputAmount = FormatAmount(a.input, 6)
		a.item.OutputAmount = FormatAmount(a.output, 6)
		a.item.CacheAmount = FormatAmount(a.cache, 6)
		a.item.CacheWriteAmount = FormatAmount(a.cacheWrite, 6)
		a.item.Amount = FormatAmount(a.total, 6)
		a.item.DiscountedAmount = FormatAmount(new(big.Rat).Mul(a.total, discountRat), 6)
		grand.Add(grand, a.total)
		out = append(out, a.item)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].ModelName != out[j].ModelName {
			return out[i].ModelName < out[j].ModelName
		}
		if out[i].TierFrom != out[j].TierFrom {
			return out[i].TierFrom < out[j].TierFrom
		}
		return out[i].InputPrice < out[j].InputPrice
	})
	return out, InvoiceTotal{Amount: FormatAmount(grand, 6), Discount: discount, DiscountedAmount: FormatAmount(new(big.Rat).Mul(grand, discountRat), 6)}, nil
}

func usageFromAggregate(row AggregateRow) Usage {
	return Usage{PromptTokens: row.PromptTokens, CompletionTokens: row.CompletionTokens, CacheTokens: row.CacheTokens, CacheWriteTokens: row.CacheWriteTokens, CacheWrite5mTokens: row.CacheWrite5mTokens, CacheWrite1hTokens: row.CacheWrite1hTokens}
}

func multipliedDecimal(value, ratio string) (string, error) {
	left, err := decimalRat(value)
	if err != nil {
		return "", err
	}
	right, err := decimalRat(ratio)
	if err != nil {
		return "", err
	}
	return new(big.Rat).Mul(left, right).FloatString(6), nil
}

func selectPriceForTier(prices []Price, occurredAt time.Time, _ int64) (Price, bool) {
	// Older immutable bill rows may retain a non-zero tier_from. Reading them
	// must still follow the current billing policy: CT tiers are not applied.
	return SelectPrice(prices, occurredAt, 0)
}
