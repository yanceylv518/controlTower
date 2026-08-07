package billing

import (
	"database/sql"
	"math/big"
	"sort"
	"strconv"
	"strings"
	"time"
)

const (
	ReconciliationAnomaly    = "anomaly"
	ReconciliationCacheWrite = "cache_write_policy"
	ReconciliationResidual   = "residual"
)

type ReconciliationBreakdown struct {
	Anomaly          string `json:"anomaly"`
	CacheWritePolicy string `json:"cache_write_policy"`
	Residual         string `json:"residual"`
}

type ReconciliationRow struct {
	UserID         int64                   `json:"user_id"`
	Username       string                  `json:"username"`
	Day            string                  `json:"day,omitempty"`
	ModelName      string                  `json:"model_name,omitempty"`
	GroupName      string                  `json:"group_name,omitempty"`
	RequestCount   int64                   `json:"request_count"`
	AbnormalRows   int64                   `json:"abnormal_rows"`
	CTAmount       string                  `json:"ct_amount"`
	ActualAmount   string                  `json:"actual_amount"`
	DiffAmount     string                  `json:"diff_amount"`
	DiffRate       string                  `json:"diff_rate"`
	FallbackPriced bool                    `json:"fallback_priced"`
	Classification string                  `json:"classification"`
	Breakdown      ReconciliationBreakdown `json:"breakdown"`
}

type ReconciliationTotals struct {
	CTAmount     string                  `json:"ct_amount"`
	ActualAmount string                  `json:"actual_amount"`
	DiffAmount   string                  `json:"diff_amount"`
	Breakdown    ReconciliationBreakdown `json:"breakdown"`
}

type ReconciliationReport struct {
	Rows   []ReconciliationRow  `json:"rows"`
	Totals ReconciliationTotals `json:"totals"`
}

type RequestReconciliation struct {
	LogID          int64  `json:"log_id"`
	RequestID      string `json:"request_id"`
	CreatedAt      string `json:"created_at"`
	ActualAmount   string `json:"actual_amount"`
	RebuiltAmount  string `json:"rebuilt_amount"`
	CTAmount       string `json:"ct_amount"`
	DiffAmount     string `json:"diff_amount"`
	InputDiff      string `json:"input_diff"`
	OutputDiff     string `json:"output_diff"`
	CacheReadDiff  string `json:"cache_read_diff"`
	CacheWriteDiff string `json:"cache_write_diff"`
	GroupDiff      string `json:"group_diff"`
	Unexplained    bool   `json:"unexplained"`
	FallbackPriced bool   `json:"fallback_priced"`
}

type RequestReconciliationResult struct {
	Items           []RequestReconciliation `json:"items"`
	Scanned         int                     `json:"scanned"`
	Matched         int                     `json:"matched"`
	Truncated       bool                    `json:"truncated"`
	RebuildResidual string                  `json:"rebuild_residual"`
	ComponentDiffs  RequestComponentDiffs   `json:"component_diffs"`
	Uninformative   bool                    `json:"uninformative"`
}

type RequestComponentDiffs struct {
	Input      string `json:"input"`
	Output     string `json:"output"`
	CacheRead  string `json:"cache_read"`
	CacheWrite string `json:"cache_write"`
	Group      string `json:"group"`
}

type reconciliationAcc struct {
	row                                       ReconciliationRow
	ct, actual, anomaly, cacheWrite, residual *big.Rat
}

// ClassifyReconciliation returns the largest absolute explained component.
// It is intentionally pure so UI, JSON and CSV cannot diverge.
func ClassifyReconciliation(anomaly, cacheWrite, residual *big.Rat) string {
	label, largest := ReconciliationAnomaly, absRat(anomaly)
	if candidate := absRat(cacheWrite); candidate.Cmp(largest) > 0 {
		label, largest = ReconciliationCacheWrite, candidate
	}
	if candidate := absRat(residual); candidate.Cmp(largest) > 0 {
		label = ReconciliationResidual
	}
	return label
}

// BuildReconciliation compares the generated CT bill with new-api's deducted
// quota. detail controls whether rows are grouped by user or by day/model/group.
func BuildReconciliation(rows []AggregateRow, prices []PriceRecord, ratios []GroupRatio, snapshots map[string]string, anomalies []AnomalyCount, detail bool) ReconciliationReport {
	priceByModel := map[string][]Price{}
	for _, p := range prices {
		priceByModel[p.ModelName] = append(priceByModel[p.ModelName], p.Price)
	}
	ratioByGroup := map[string]string{}
	for _, r := range ratios {
		ratioByGroup[r.GroupName] = r.Ratio
	}
	values := map[string]*reconciliationAcc{}
	keyFor := func(userID int64, day, model, group string) string {
		if !detail {
			return strconv.FormatInt(userID, 10)
		}
		return strings.Join([]string{strconv.FormatInt(userID, 10), day, model, group}, "\x00")
	}
	ensure := func(userID int64, username, day, model, group string) *reconciliationAcc {
		key := keyFor(userID, day, model, group)
		a := values[key]
		if a == nil {
			a = &reconciliationAcc{row: ReconciliationRow{UserID: userID, Username: username, Day: day, ModelName: model, GroupName: group}, ct: new(big.Rat), actual: new(big.Rat), anomaly: new(big.Rat), cacheWrite: new(big.Rat), residual: new(big.Rat)}
			values[key] = a
		}
		if a.row.Username == "" {
			a.row.Username = username
		}
		return a
	}
	for _, row := range rows {
		day := row.Day.In(BusinessLocation).Format("2006-01-02")
		a := ensure(row.UserID, row.Username, day, row.ModelName, row.GroupName)
		a.row.RequestCount += row.RequestCount
		quotaPerUnit, qerr := quotaPerUnitForReport(snapshots[day])
		actual, aerr := AmountFromQuota(row.Quota, quotaPerUnit)
		if qerr != nil || aerr != nil {
			actual = new(big.Rat)
		}
		a.actual.Add(a.actual, actual)
		price, configured := selectPriceForTier(priceByModel[row.ModelName], row.Day, row.TierFrom)
		if !configured {
			// Fallback-priced rows intentionally use the same quota amount on
			// both sides. They are marked but cannot create a fake difference.
			a.ct.Add(a.ct, actual)
			a.row.FallbackPriced = true
			continue
		}
		price = priceWithSnapshotCacheWrite(price, snapshots[day], row.ModelName)
		ratio := ratioByGroup[row.GroupName]
		if ratio == "" {
			ratio = "1"
		}
		if amount, err := Amount(usageFromAggregate(row), price, ratio); err == nil {
			a.ct.Add(a.ct, amount)
		}
		a.cacheWrite.Add(a.cacheWrite, estimatedCacheWritePolicy(row, snapshots[day]))
	}
	for _, anomaly := range anomalies {
		day := anomaly.Day.In(BusinessLocation).Format("2006-01-02")
		a := ensure(anomaly.UserID, "", day, anomaly.ModelName, anomaly.GroupName)
		a.row.AbnormalRows += anomaly.Count
		if value, err := decimalRat(decimalOrZero(anomaly.Amount)); err == nil {
			a.actual.Add(a.actual, value)
			a.anomaly.Add(a.anomaly, value)
		}
	}
	report := ReconciliationReport{Rows: make([]ReconciliationRow, 0, len(values))}
	totalCT, totalActual, totalAnomaly, totalCache := new(big.Rat), new(big.Rat), new(big.Rat), new(big.Rat)
	for _, a := range values {
		diff := new(big.Rat).Sub(a.actual, a.ct)
		a.residual.Sub(new(big.Rat).Sub(diff, a.anomaly), a.cacheWrite)
		a.row.CTAmount = FormatAmount(a.ct, 6)
		a.row.ActualAmount = FormatAmount(a.actual, 6)
		a.row.DiffAmount = FormatAmount(diff, 6)
		a.row.DiffRate = "0.000000"
		if a.actual.Sign() != 0 {
			a.row.DiffRate = new(big.Rat).Quo(diff, a.actual).FloatString(6)
		}
		a.row.Breakdown = ReconciliationBreakdown{Anomaly: FormatAmount(a.anomaly, 6), CacheWritePolicy: FormatAmount(a.cacheWrite, 6), Residual: FormatAmount(a.residual, 6)}
		a.row.Classification = ClassifyReconciliation(a.anomaly, a.cacheWrite, a.residual)
		report.Rows = append(report.Rows, a.row)
		totalCT.Add(totalCT, a.ct)
		totalActual.Add(totalActual, a.actual)
		totalAnomaly.Add(totalAnomaly, a.anomaly)
		totalCache.Add(totalCache, a.cacheWrite)
	}
	sort.Slice(report.Rows, func(i, j int) bool {
		if report.Rows[i].FallbackPriced != report.Rows[j].FallbackPriced {
			return !report.Rows[i].FallbackPriced
		}
		left, _ := decimalRat(report.Rows[i].DiffAmount)
		right, _ := decimalRat(report.Rows[j].DiffAmount)
		if cmp := absRat(left).Cmp(absRat(right)); cmp != 0 {
			return cmp > 0
		}
		if report.Rows[i].UserID != report.Rows[j].UserID {
			return report.Rows[i].UserID < report.Rows[j].UserID
		}
		return report.Rows[i].Day > report.Rows[j].Day
	})
	totalDiff := new(big.Rat).Sub(totalActual, totalCT)
	totalResidual := new(big.Rat).Sub(new(big.Rat).Sub(totalDiff, totalAnomaly), totalCache)
	report.Totals = ReconciliationTotals{CTAmount: FormatAmount(totalCT, 6), ActualAmount: FormatAmount(totalActual, 6), DiffAmount: FormatAmount(totalDiff, 6), Breakdown: ReconciliationBreakdown{Anomaly: FormatAmount(totalAnomaly, 6), CacheWritePolicy: FormatAmount(totalCache, 6), Residual: FormatAmount(totalResidual, 6)}}
	return report
}

func estimatedCacheWritePolicy(row AggregateRow, raw string) *big.Rat {
	result := new(big.Rat)
	if row.CacheWriteTokens <= 0 || strings.TrimSpace(raw) == "" {
		return result
	}
	snapshot, err := ParseRatioSnapshot(raw)
	if err != nil {
		return result
	}
	mr, ok := snapshot.ModelRatio[row.ModelName]
	if !ok {
		return result
	}
	modelRatio, err := decimalRat(mr)
	if err != nil {
		return result
	}
	qpu, err := decimalRat(snapshot.QuotaPerUnit)
	if err != nil || qpu.Sign() == 0 {
		return result
	}
	input := new(big.Rat).Quo(new(big.Rat).Mul(modelRatio, big.NewRat(tokensPerMillion, 1)), qpu)
	writeRatio := snapshot.CreateCacheRatio[row.ModelName]
	if writeRatio == "" {
		writeRatio = "1.25"
	}
	wr, err := decimalRat(writeRatio)
	if err != nil {
		return result
	}
	group := snapshot.GroupRatio[row.GroupName]
	if group == "" {
		group = "1"
	}
	gr, err := decimalRat(group)
	if err != nil {
		return result
	}
	price5m := new(big.Rat).Mul(input, wr)
	price1h := new(big.Rat).Mul(input, big.NewRat(2, 1))
	remaining := row.CacheWriteTokens - row.CacheWrite5mTokens - row.CacheWrite1hTokens
	if remaining < 0 {
		remaining = 0
	}
	result.Add(result, tokenCost(row.CacheWrite5mTokens+remaining, price5m))
	result.Add(result, tokenCost(row.CacheWrite1hTokens, price1h))
	return result.Mul(result, gr)
}

func absRat(value *big.Rat) *big.Rat {
	if value == nil {
		return new(big.Rat)
	}
	return new(big.Rat).Abs(value)
}

// ReconcileRequests performs the bounded L3 arithmetic after the caller has
// scanned the fixed user/day scope. Only matching model rows are analyzed.
func ReconcileRequests(logs []PagedLogRecord, model string, day time.Time, prices []PriceRecord, ratios []GroupRatio, snapshotRaw string) RequestReconciliationResult {
	result := RequestReconciliationResult{Items: []RequestReconciliation{}}
	priceByModel := map[string][]Price{}
	for _, p := range prices {
		priceByModel[p.ModelName] = append(priceByModel[p.ModelName], p.Price)
	}
	groupByName := map[string]string{}
	for _, ratio := range ratios {
		groupByName[ratio.GroupName] = ratio.Ratio
	}
	snapshot, snapshotErr := ParseRatioSnapshot(snapshotRaw)
	residual := new(big.Rat)
	inputTotal, outputTotal := new(big.Rat), new(big.Rat)
	cacheReadTotal, cacheWriteTotal, groupTotal := new(big.Rat), new(big.Rat), new(big.Rat)
	for _, log := range logs {
		if log.ModelName != model {
			continue
		}
		result.Matched++
		actual, _ := AmountFromQuota(log.Quota, quotaPerUnitOrDefault(snapshotRaw))
		row := RequestReconciliation{LogID: log.ID, RequestID: log.RequestID, CreatedAt: FormatBusinessTime(log.CreatedUnix), ActualAmount: FormatAmount(actual, 6)}
		ctPrice, configured := SelectPrice(priceByModel[model], day, RequestContextTokens(log))
		if !configured {
			row.FallbackPriced, result.Uninformative = true, true
			row.CTAmount, row.RebuiltAmount, row.DiffAmount = row.ActualAmount, row.ActualAmount, "0.000000"
			result.Items = append(result.Items, row)
			continue
		}
		ctPrice = priceWithSnapshotCacheWrite(ctPrice, snapshotRaw, model)
		ctGroup := groupByName[log.GroupName]
		if ctGroup == "" {
			ctGroup = "1"
		}
		ctCharge := PriceRequest(log, ctPrice, ctGroup)
		row.CTAmount = ctCharge.Total
		if snapshotErr != nil || log.ModelRatio == "" || log.GroupRatio == "" || (nullablePositive(log.CompletionTokens) && log.CompletionRatio == "") || (log.CacheTokens > 0 && log.CacheRatio == "") || (log.CacheWriteTokens > 0 && log.CacheCreationRatio == "") {
			row.Unexplained = true
			row.RebuiltAmount = ""
			row.DiffAmount = subtractDecimal(row.ActualAmount, row.CTAmount)
			result.Items = append(result.Items, row)
			continue
		}
		baseRatio, e := decimalRat(log.ModelRatio)
		qpu, qe := decimalRat(snapshot.QuotaPerUnit)
		if e != nil || qe != nil || qpu.Sign() == 0 {
			row.Unexplained = true
			row.DiffAmount = subtractDecimal(row.ActualAmount, row.CTAmount)
			result.Items = append(result.Items, row)
			continue
		}
		base := new(big.Rat).Quo(new(big.Rat).Mul(baseRatio, big.NewRat(tokensPerMillion, 1)), qpu)
		newPrice := Price{Input: base.FloatString(12), Output: base.FloatString(12), Cache: base.FloatString(12), CacheWrite: "0"}
		if log.CompletionRatio != "" {
			newPrice.Output = multiplyRatString(base, log.CompletionRatio)
		}
		if log.CacheRatio != "" {
			newPrice.Cache = multiplyRatString(base, log.CacheRatio)
		}
		if log.CacheCreationRatio != "" {
			newPrice.CacheWrite = multiplyRatString(base, log.CacheCreationRatio)
		}
		newCharge := PriceRequest(log, newPrice, log.GroupRatio)
		row.RebuiltAmount = newCharge.Total
		row.DiffAmount = subtractDecimal(row.ActualAmount, row.CTAmount)
		newBase, ctBase := PriceRequest(log, newPrice, "1"), PriceRequest(log, ctPrice, "1")
		row.InputDiff = subtractDecimal(newBase.InputAmount, ctBase.InputAmount)
		row.OutputDiff = subtractDecimal(newBase.OutputAmount, ctBase.OutputAmount)
		row.CacheReadDiff = subtractDecimal(newBase.CacheAmount, ctBase.CacheAmount)
		row.CacheWriteDiff = subtractDecimal(newBase.CacheWriteAmount, ctBase.CacheWriteAmount)
		newGroup := subtractDecimal(newCharge.Total, newBase.Total)
		ctGroupAmount := subtractDecimal(ctCharge.Total, ctBase.Total)
		row.GroupDiff = subtractDecimal(newGroup, ctGroupAmount)
		addDecimal(inputTotal, row.InputDiff)
		addDecimal(outputTotal, row.OutputDiff)
		addDecimal(cacheReadTotal, row.CacheReadDiff)
		addDecimal(cacheWriteTotal, row.CacheWriteDiff)
		addDecimal(groupTotal, row.GroupDiff)
		if rr, err := decimalRat(subtractDecimal(row.ActualAmount, row.RebuiltAmount)); err == nil {
			residual.Add(residual, absRat(rr))
		}
		result.Items = append(result.Items, row)
	}
	sort.Slice(result.Items, func(i, j int) bool {
		left, _ := decimalRat(decimalOrZero(result.Items[i].DiffAmount))
		right, _ := decimalRat(decimalOrZero(result.Items[j].DiffAmount))
		return absRat(left).Cmp(absRat(right)) > 0
	})
	if len(result.Items) > 20 {
		result.Items = result.Items[:20]
	}
	result.RebuildResidual = FormatAmount(residual, 6)
	result.ComponentDiffs = RequestComponentDiffs{
		Input:      FormatAmount(inputTotal, 6),
		Output:     FormatAmount(outputTotal, 6),
		CacheRead:  FormatAmount(cacheReadTotal, 6),
		CacheWrite: FormatAmount(cacheWriteTotal, 6),
		Group:      FormatAmount(groupTotal, 6),
	}
	return result
}

func addDecimal(total *big.Rat, value string) {
	if parsed, err := decimalRat(decimalOrZero(value)); err == nil {
		total.Add(total, parsed)
	}
}

func quotaPerUnitOrDefault(raw string) string {
	value, err := quotaPerUnitForReport(raw)
	if err != nil {
		return defaultQuotaPerUnit
	}
	return value
}

func nullablePositive(value sql.NullInt64) bool { return value.Valid && value.Int64 > 0 }

func multiplyRatString(base *big.Rat, multiplier string) string {
	value, err := decimalRat(multiplier)
	if err != nil {
		return "0"
	}
	return new(big.Rat).Mul(base, value).FloatString(12)
}

func subtractDecimal(left, right string) string {
	l, lerr := decimalRat(decimalOrZero(left))
	r, rerr := decimalRat(decimalOrZero(right))
	if lerr != nil || rerr != nil {
		return "0.000000"
	}
	return new(big.Rat).Sub(l, r).FloatString(6)
}
