package billing

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"math"
	"math/big"
	"strings"
	"time"

	"github.com/expr-lang/expr"
	"github.com/expr-lang/expr/ast"
	"github.com/expr-lang/expr/vm"
)

type tieredRequestRule struct {
	Cond       string  `json:"cond"`
	Multiplier float64 `json:"multiplier"`
	Matched    bool    `json:"matched"`
}

type tieredRulePatcher struct {
	rules []tieredRequestRule
	next  int
}

func (p *tieredRulePatcher) Visit(node *ast.Node) {
	conditional, ok := (*node).(*ast.ConditionalNode)
	if !ok || !tieredUsesRequestProbe(conditional.Cond) {
		return
	}
	multiplier, multiplierOK := tieredNumber(conditional.Exp1)
	fallback, fallbackOK := tieredNumber(conditional.Exp2)
	if !multiplierOK || !fallbackOK || fallback != 1 {
		return
	}
	matched := false
	if p.next < len(p.rules) {
		rule := p.rules[p.next]
		matched = rule.Matched
		// The frozen trace is authoritative, but reject a structurally different
		// expression/log pair instead of silently applying the wrong multiplier.
		if rule.Multiplier != 0 && math.Abs(rule.Multiplier-multiplier) > 1e-9 {
			return
		}
	}
	p.next++
	value := 1.0
	if matched {
		value = multiplier
	}
	ast.Patch(node, &ast.FloatNode{Value: value})
}

func tieredNumber(node ast.Node) (float64, bool) {
	switch value := node.(type) {
	case *ast.IntegerNode:
		return float64(value.Value), true
	case *ast.FloatNode:
		return value.Value, true
	default:
		return 0, false
	}
}

func tieredUsesRequestProbe(node ast.Node) bool {
	return ast.Find(node, func(node ast.Node) bool {
		identifier, ok := node.(*ast.IdentifierNode)
		if !ok {
			return false
		}
		switch identifier.Value {
		case "param", "header", "hour", "minute", "weekday", "month", "day":
			return true
		default:
			return false
		}
	}) != nil
}

func calculateTieredExprCharge(log PagedLogRecord, quotaPerUnit string) (LogCharge, error) {
	rawExpr, err := base64.StdEncoding.DecodeString(log.ExprBase64)
	if err != nil || len(rawExpr) == 0 {
		return LogCharge{}, fmt.Errorf("invalid tiered expression")
	}
	rules := []tieredRequestRule{}
	if log.RequestRules != "" && json.Unmarshal([]byte(log.RequestRules), &rules) != nil {
		return LogCharge{}, fmt.Errorf("invalid tiered request rules")
	}
	body := string(rawExpr)
	if strings.HasPrefix(body, "v1:") {
		body = body[3:]
	}
	patcher := &tieredRulePatcher{rules: rules}
	program, err := expr.Compile(body, expr.Env(tieredCompileEnv()), expr.Patch(patcher), expr.AsFloat64())
	if err != nil {
		return LogCharge{}, fmt.Errorf("tiered expression compile: %w", err)
	}
	if patcher.next != len(rules) {
		return LogCharge{}, fmt.Errorf("tiered request rule trace mismatch")
	}
	used := tieredUsedVars(program.Node())
	params := tieredTokenParams(log, used)
	matchedTier := ""
	now := time.Unix(log.CreatedUnix, 0)
	env := tieredRuntimeEnv(params, now, &matchedTier)
	result, err := expr.Run(program, env)
	if err != nil {
		return LogCharge{}, fmt.Errorf("tiered expression run: %w", err)
	}
	cost, ok := result.(float64)
	if !ok || math.IsNaN(cost) || math.IsInf(cost, 0) || cost < 0 {
		return LogCharge{}, fmt.Errorf("invalid tiered expression result")
	}
	if log.MatchedTier != "" && matchedTier != log.MatchedTier {
		return LogCharge{}, fmt.Errorf("tiered matched tier mismatch")
	}
	group, err := requiredLogRatio(log.GroupRatio, "group_ratio")
	if err != nil {
		return LogCharge{}, err
	}
	qpu, err := decimalRat(quotaPerUnit)
	if err != nil || qpu.Sign() <= 0 {
		return LogCharge{}, fmt.Errorf("invalid QuotaPerUnit")
	}
	amount := new(big.Rat).Mul(new(big.Rat).SetFloat64(cost), group)
	amount.Quo(amount, big.NewRat(tokensPerMillion, 1))
	charge := LogCharge{Mode: "tiered_expr", MatchedTier: matchedTier, Total: FormatAmount(amount, 6)}
	fillTieredLinearBreakdown(&charge, program, params, now, matchedTier, cost, group, log)
	charge.ReconstructedQuota = new(big.Rat).Mul(new(big.Rat).Set(amount), qpu).FloatString(6)
	return charge, nil
}

func fillTieredLinearBreakdown(charge *LogCharge, program anyProgram, params tieredParams, now time.Time, matchedTier string, totalCost float64, group *big.Rat, log PagedLogRecord) {
	type lane struct {
		tokens float64
		zero   func(*tieredParams)
		price  *string
		amount *string
	}
	lanes := []lane{
		{params.P, func(p *tieredParams) { p.P = 0 }, &charge.InputPrice, &charge.InputAmount},
		{params.C, func(p *tieredParams) { p.C = 0 }, &charge.OutputPrice, &charge.OutputAmount},
		{params.CR, func(p *tieredParams) { p.CR = 0 }, &charge.CacheReadPrice, &charge.CacheReadAmount},
	}
	if log.CacheWrite5mTokens > 0 {
		lanes = append(lanes, lane{params.CC, func(p *tieredParams) { p.CC = 0 }, &charge.CacheWrite5mPrice, &charge.CacheWrite5mAmount})
	} else {
		lanes = append(lanes, lane{params.CC, func(p *tieredParams) { p.CC = 0 }, &charge.CacheWritePrice, &charge.CacheWriteAmount})
	}
	lanes = append(lanes, lane{params.CC1h, func(p *tieredParams) { p.CC1h = 0 }, &charge.CacheWrite1hPrice, &charge.CacheWrite1hAmount})
	type calculated struct {
		lane         lane
		contribution float64
	}
	parts := []calculated{}
	sum := 0.0
	for _, item := range lanes {
		if item.tokens <= 0 {
			continue
		}
		without := params
		item.zero(&without)
		tier := ""
		out, err := expr.Run(program, tieredRuntimeEnv(without, now, &tier))
		if err != nil || tier != matchedTier {
			return
		}
		base, ok := out.(float64)
		if !ok {
			return
		}
		contribution := totalCost - base
		if contribution < -1e-9 {
			return
		}
		sum += contribution
		parts = append(parts, calculated{item, contribution})
	}
	// Only expose unit prices when independent lane contributions explain the
	// complete expression. This is exact for ordinary linear provider prices.
	if math.Abs(sum-totalCost) > math.Max(1e-7, math.Abs(totalCost)*1e-9) {
		return
	}
	for _, part := range parts {
		unit := new(big.Rat).Mul(new(big.Rat).SetFloat64(part.contribution/part.lane.tokens), group)
		*part.lane.price = unit.FloatString(6)
		amount := new(big.Rat).Mul(new(big.Rat).SetFloat64(part.contribution), group)
		amount.Quo(amount, big.NewRat(tokensPerMillion, 1))
		*part.lane.amount = FormatAmount(amount, 6)
	}
}

// anyProgram keeps the helper signature compact while preserving the concrete
// expression VM program accepted by expr.Run.
type anyProgram = *vm.Program

type tieredParams struct{ P, C, Len, CR, CC, CC1h, Img, ImgO, AI, AO float64 }

func tieredTokenParams(log PagedLogRecord, used map[string]bool) tieredParams {
	rawPrompt := nullableInt64(log.SourcePromptTokens)
	if !log.SourcePromptTokens.Valid {
		rawPrompt = nullableInt64(log.PromptTokens)
	}
	p := tieredParams{P: float64(rawPrompt), C: float64(nullableInt64(log.CompletionTokens)), CR: float64(log.CacheTokens), CC: float64(log.CacheWriteTokens - log.CacheWrite1hTokens), CC1h: float64(log.CacheWrite1hTokens), Img: float64(log.ImageInputTokens), ImgO: float64(log.ImageOutputTokens), AI: float64(log.AudioInputTokens), AO: float64(log.AudioOutputTokens)}
	if p.CC < 0 {
		p.CC = 0
	}
	p.Len = p.P
	if log.UsageSemantic == "anthropic" {
		p.Len += p.CR + p.CC + p.CC1h
	} else {
		if used["cr"] {
			p.P -= p.CR
		}
		if used["cc"] {
			p.P -= p.CC
		}
		if used["cc1h"] {
			p.P -= p.CC1h
		}
		if used["img"] {
			p.P -= p.Img
		}
		if used["ai"] {
			p.P -= p.AI
		}
		if used["img_o"] {
			p.C -= p.ImgO
		}
		if used["ao"] {
			p.C -= p.AO
		}
	}
	if p.P < 0 {
		p.P = 0
	}
	if p.C < 0 {
		p.C = 0
	}
	return p
}

func tieredCompileEnv() map[string]any {
	return tieredRuntimeEnv(tieredParams{}, time.Unix(0, 0), new(string))
}

func tieredRuntimeEnv(p tieredParams, now time.Time, matchedTier *string) map[string]any {
	inZone := func(zone string) time.Time {
		if strings.TrimSpace(zone) == "" {
			return now.UTC()
		}
		loc, err := time.LoadLocation(zone)
		if err != nil {
			return now.UTC()
		}
		return now.In(loc)
	}
	return map[string]any{
		"p": p.P, "c": p.C, "len": p.Len, "cr": p.CR, "cc": p.CC, "cc1h": p.CC1h,
		"img": p.Img, "img_o": p.ImgO, "ai": p.AI, "ao": p.AO,
		"tier":   func(name string, value float64) float64 { *matchedTier = name; return value },
		"header": func(string) string { return "" }, "param": func(string) any { return nil },
		"has": func(source any, substr string) bool {
			return source != nil && substr != "" && strings.Contains(fmt.Sprint(source), substr)
		},
		"hour": func(zone string) int { return inZone(zone).Hour() }, "minute": func(zone string) int { return inZone(zone).Minute() },
		"weekday": func(zone string) int { return int(inZone(zone).Weekday()) }, "month": func(zone string) int { return int(inZone(zone).Month()) },
		"day": func(zone string) int { return inZone(zone).Day() }, "max": math.Max, "min": math.Min, "abs": math.Abs, "ceil": math.Ceil, "floor": math.Floor,
	}
}

func tieredUsedVars(node ast.Node) map[string]bool {
	used := map[string]bool{}
	ast.Find(node, func(node ast.Node) bool {
		if identifier, ok := node.(*ast.IdentifierNode); ok {
			used[identifier.Value] = true
		}
		return false
	})
	return used
}
