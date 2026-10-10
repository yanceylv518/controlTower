package archivejob

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"math/big"
	"strconv"
	"strings"

	"controltower/internal/archivefacts"
)

// Groups retain historical pricing evidence. Amounts are sums of request-level
// charges, never a second application of the user's discount or a daily expression.
const summaryParserVersion = 3

type aggregate struct {
	Dimensions map[string]any
	Amounts    map[string]string
}

func add(m map[string]string, key, value string) error {
	n, ok := new(big.Int).SetString(value, 10)
	if !ok || n.Sign() < 0 {
		return errors.New("invalid_billing_integer")
	}
	old := new(big.Int)
	if m[key] != "" {
		if _, ok = old.SetString(m[key], 10); !ok {
			return errors.New("invalid_summary_integer")
		}
	}
	m[key] = old.Add(old, n).String()
	return nil
}
func parseAggregate(r row) (string, aggregate, error) {
	a := aggregate{Dimensions: map[string]any{}, Amounts: map[string]string{}}
	for _, key := range []string{"user_id", "token_id", "model_name", "channel_id", "channel", "group", "type", "username", "token_name"} {
		a.Dimensions[key] = r[key]
	}
	other := map[string]any{}
	if value := r.text("other"); value != "" {
		dec := json.NewDecoder(strings.NewReader(value))
		dec.UseNumber()
		if err := dec.Decode(&other); err != nil {
			return "", a, errors.New("invalid_billing_other_json")
		}
	}
	pricing := map[string]any{}
	for _, key := range []string{"model_ratio", "model_price", "completion_ratio", "cache_ratio", "cache_creation_ratio", "cache_creation_5m_ratio", "cache_creation_1h_ratio", "cache_creation_ratio_1h", "cache_creation_ratio_5m", "group_ratio", "user_group_ratio", "user_model_discount", "billing_mode", "billing_expr", "expr", "expr_string", "expr_b64", "matched_tier", "billing_source", "image_ratio", "request_rules", "tool_surcharges"} {
		if v, ok := other[key]; ok {
			pricing[key] = v
		}
	}
	a.Dimensions["pricing"] = pricing
	a.Dimensions["summary_version"] = summaryParserVersion
	addToolCallCounts(a.Amounts, other)
	values := map[string][]byte{}
	for k, v := range r {
		if v == nil {
			values[k] = nil
		} else {
			values[k] = []byte(*v)
		}
	}
	fact := archivefacts.ParseUsageSnapshot(values)
	// New structured price evidence stores decimal lexemes as strings so MySQL
	// JSON storage cannot round historical rule/tool prices through float64.
	var exactPrices map[string]json.RawMessage
	if err := json.Unmarshal(fact.PricingSnapshotJSON, &exactPrices); err != nil {
		return "", a, err
	}
	for _, key := range []string{"image_ratio", "request_rules", "tool_surcharges"} {
		if value, ok := exactPrices[key]; ok {
			pricing[key] = value
		}
	}
	var usage archivefacts.UsageContext
	if err := json.Unmarshal(fact.UsageContextJSON, &usage); err != nil {
		return "", a, err
	}
	// Only semantic metadata is a dimension, never per-request normalized counts.
	a.Dimensions["usage"] = map[string]any{"semantic": usage.UsageSemantic, "semantic_basis": usage.SemanticBasis, "billing_path": usage.UsageBillingPath, "cache_policy": usage.CachePolicy, "version": usage.UsageVersion, "issues": fact.IssueCodes}
	normalized := map[string]*int64{
		"input_tokens": fact.InputTokens, "output_tokens": fact.OutputTokens,
		"pricing_input_tokens": usage.PricingInputTokens, "context_tokens": usage.ContextTokens,
		"cache_read_tokens": fact.CacheReadTokens, "cache_write_tokens": fact.CacheWriteTokens,
		"cache_write_5m_tokens": fact.CacheWrite5mTokens, "cache_write_1h_tokens": fact.CacheWrite1hTokens,
		"image_input_tokens": fact.ImageInputTokens, "image_output_tokens": fact.ImageOutputTokens,
		"audio_input_tokens": fact.AudioInputTokens, "audio_output_tokens": fact.AudioOutputTokens,
	}
	if fact.CacheWriteTokens != nil {
		remaining := *fact.CacheWriteTokens
		for _, split := range []*int64{fact.CacheWrite5mTokens, fact.CacheWrite1hTokens} {
			if split != nil {
				remaining -= *split
			}
		}
		if remaining < 0 {
			remaining = 0
		}
		normalized["cache_write_unclassified_tokens"] = &remaining
	} else {
		normalized["cache_write_unclassified_tokens"] = nil
	}
	for k, v := range normalized {
		if v == nil {
			a.Amounts[k+"_missing"] = "1"
		} else {
			a.Amounts[k] = strconv.FormatInt(*v, 10)
		}
	}
	if len(fact.IssueCodes) > 0 {
		a.Amounts["usage_issue_rows"] = "1"
	} else {
		a.Amounts["usage_issue_rows"] = "0"
	}

	if err := add(a.Amounts, "log_rows", "1"); err != nil {
		return "", a, err
	}
	a.Amounts["anomaly_rows"] = "1"
	for _, k := range []string{"empty_output", "missing_output", "error_logs", "charged_empty_output"} {
		a.Amounts[k] = "0"
	}
	if r.text("type") == "5" {
		a.Amounts["error_logs"] = "1"
	}
	if r.text("type") == "2" {
		if r.text("completion_tokens") == "" {
			a.Amounts["missing_output"] = "1"
		}
		if r.text("completion_tokens") == "0" {
			a.Amounts["empty_output"] = "1"
			quota, _ := new(big.Int).SetString(r.text("quota"), 10)
			if quota != nil && quota.Sign() > 0 {
				a.Amounts["charged_empty_output"] = "1"
			}
		}
	}
	if r.text("type") == "2" {
		a.Amounts["requests"] = "1"
	}
	for _, key := range []string{"prompt_tokens", "completion_tokens", "quota"} {
		if value := r.text(key); value != "" {
			if err := add(a.Amounts, key, value); err != nil {
				return "", a, err
			}
		} else {
			_ = add(a.Amounts, key+"_missing", "1")
		}
	}
	for _, key := range []string{"cache_tokens", "cache_creation_tokens", "cache_creation_5m_tokens", "cache_creation_1h_tokens", "cache_creation_tokens_5m", "cache_creation_tokens_1h", "quota_before_discount", "discount_quota", "quota_after_discount"} {
		if v, ok := other[key]; ok {
			var text string
			switch n := v.(type) {
			case json.Number:
				text = n.String()
			case string:
				text = n
			default:
				return "", a, errors.New("invalid_billing_usage")
			}
			if err := add(a.Amounts, key, text); err != nil {
				return "", a, err
			}
		} else {
			_ = add(a.Amounts, key+"_missing", "1")
		}
	}
	// No fallback from user quota to upstream cost. Missing before-discount
	// evidence remains explicit so an upstream bill cannot use the user's price.
	raw, _ := json.Marshal(a.Dimensions)
	hash := sha256.Sum256(raw)
	return hex.EncodeToString(hash[:]), a, nil
}
func (e *Engine) summarize(ctx context.Context, c *sql.Conn, s *state, batch int) error {
	h := &s.History
	from, to := dateBounds(h.Date)
	rows, byteLimited, err := readArchivePage(ctx, c, table(h.Date), "SELECT /*+ MAX_EXECUTION_TIME(3000) */ * FROM "+q(table(h.Date))+" WHERE created_at>=? AND created_at<? AND (created_at>? OR (created_at=? AND id>?)) ORDER BY created_at,id LIMIT ?", from, to, h.AfterCreated, h.AfterCreated, h.AfterID, batch)
	if err != nil {
		return err
	}
	groups := map[string]aggregate{}
	for _, r := range rows {
		key, a, err := parseAggregate(r)
		if err != nil {
			return failDay(ctx, c, s, code(err))
		}
		if old, ok := groups[key]; ok {
			for k, v := range a.Amounts {
				if err = add(old.Amounts, k, v); err != nil {
					return err
				}
			}
		} else {
			groups[key] = a
		}
	}
	tx, err := c.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err = writeAggregateBatch(ctx, tx, h.Version, h.Date, groups); err != nil {
		return err
	}
	if len(rows) > 0 {
		h.AfterID, _ = rows[len(rows)-1].number("id")
		h.AfterCreated, _ = rows[len(rows)-1].number("created_at")
	}
	if !byteLimited && len(rows) < batch {
		h.Step = "seal"
	}
	s.HistoryProgress.AfterID = h.AfterID
	if err = save(ctx, tx, *s); err != nil {
		return err
	}
	return tx.Commit()
}
