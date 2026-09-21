package archivefacts

import (
	"bytes"
	"encoding/json"
	"io"
	"math"
	"math/big"
	"regexp"
	"strconv"
	"strings"
	"unicode/utf8"
)

// UsageContext preserves why lanes were extracted as well as missing/null/
// invalid source fields. PricingInputTokens is the historical pricing lane;
// Fact.InputTokens is the display lane after audio input has been removed.
type UsageContext struct {
	UsageSemantic      string            `json:"usage_semantic"`
	SemanticBasis      string            `json:"semantic_basis"`
	UsageBillingPath   string            `json:"usage_billing_path"`
	OtherState         string            `json:"other_state"`
	SourceFields       map[string]string `json:"source_fields"`
	OtherFields        map[string]string `json:"other_fields"`
	PricingInputTokens *int64            `json:"pricing_input_tokens,string"`
	ContextTokens      *int64            `json:"context_tokens,string"`
	CachePolicy        string            `json:"cache_policy"`
	UsageVersion       int               `json:"usage_version"`
}

var decimalSyntax = regexp.MustCompile(`^[+-]?(?:[0-9]+(?:\.[0-9]*)?|\.[0-9]+)(?:[eE][+-]?[0-9]+)?$`)

var priceKeys = []string{
	"model_price", "model_ratio", "completion_ratio", "cache_ratio", "cache_creation_ratio",
	"cache_creation_ratio_5m", "cache_creation_ratio_1h", "group_ratio", "image_ratio",
}

type usageParser struct {
	parent *parser
	values map[string]json.RawMessage
	states map[string]string
}

func (p *parser) parseUsage(f *Fact) {
	u := UsageContext{UsageSemantic: "openai", SemanticBasis: "legacy_default", CachePolicy: "source_aliases_v1", UsageVersion: 2, SourceFields: p.states, OtherFields: map[string]string{}}
	values := map[string]json.RawMessage{}
	raw, known := p.source("other")
	usable := false
	switch {
	case !known:
		u.OtherState = p.states["other"]
	case !utf8.Valid(raw):
		u.OtherState = "invalid_encoding"
		p.issue("other_invalid_encoding")
	case len(bytes.TrimSpace(raw)) == 0:
		u.OtherState = "empty"
	case bytes.Equal(bytes.TrimSpace(raw), []byte("null")):
		u.OtherState = "json_null"
	case !json.Valid(raw) || bytes.TrimSpace(raw)[0] != '{':
		u.OtherState = "invalid_json"
		p.issue("other_invalid_json")
	case duplicateJSONKey(raw):
		u.OtherState = "duplicate_key"
		p.issue("other_duplicate_key")
	default:
		if json.Unmarshal(raw, &values) == nil {
			u.OtherState, usable = "object", true
		}
	}
	v := usageParser{p, values, u.OtherFields}
	prices := map[string]json.RawMessage{}
	for _, key := range priceKeys {
		if value, ok := v.decimal(key); ok {
			prices[key], _ = json.Marshal(value) // Decimal strings, never float64.
		}
	}
	for _, key := range []string{"billing_mode", "expr_b64", "matched_tier"} {
		if value, ok := v.text(key); ok {
			prices[key], _ = json.Marshal(value)
		}
	}
	for _, key := range []string{"request_rules", "tool_surcharges"} {
		if value, ok := v.raw(key); ok {
			// These are versioned structured rule inputs. Other root keys are
			// never copied into the fact; complete other remains only evidence.
			if bytes.TrimSpace(value)[0] != '[' && bytes.TrimSpace(value)[0] != '{' {
				v.invalid(key, "pricing_value_invalid")
			} else if projected, ok := exactStructuredPricing(value); ok {
				prices[key] = projected
			} else {
				v.invalid(key, "pricing_value_invalid")
			}
		}
	}
	f.PricingSnapshotJSON, _ = json.Marshal(prices)
	f.CacheReadTokens = v.first("cache_tokens", "cached_tokens", "cache_read_input_tokens", "prompt_cache_hit_tokens")
	f.CacheWrite5mTokens = v.first("cache_creation_tokens_5m", "claude_cache_creation_5_m_tokens")
	f.CacheWrite1hTokens = v.first("cache_creation_tokens_1h", "claude_cache_creation_1_h_tokens")
	f.CacheWriteTokens = v.maximum("cache_creation_tokens", "cache_write_tokens", "cached_creation_tokens")
	if f.CacheWrite5mTokens != nil || f.CacheWrite1hTokens != nil {
		if split, ok := safeSum(value(f.CacheWrite5mTokens), value(f.CacheWrite1hTokens)); ok {
			if f.CacheWriteTokens == nil || split > *f.CacheWriteTokens {
				f.CacheWriteTokens = ptr(split)
			}
		} else {
			p.issue("usage_overflow")
			f.CacheWriteTokens = nil
		}
	}
	if semantic, ok := v.text("usage_semantic"); ok && strings.EqualFold(semantic, "anthropic") {
		u.UsageSemantic, u.SemanticBasis = "anthropic", "explicit_marker"
	}
	if claude, ok := v.raw("claude"); ok {
		var flag bool
		if json.Unmarshal(claude, &flag) == nil {
			if flag {
				u.UsageSemantic, u.SemanticBasis = "anthropic", "explicit_marker"
			}
		} else {
			v.invalid("claude", "usage_value_invalid")
		}
	}
	if path, ok := v.text("usage_billing_path"); ok {
		u.UsageBillingPath = strings.ToLower(strings.TrimSpace(path))
	} else if admin, ok := v.raw("admin_info"); ok {
		var fields map[string]json.RawMessage
		if json.Unmarshal(admin, &fields) != nil || fields == nil {
			v.invalid("admin_info", "usage_value_invalid")
		} else {
			childStates := map[string]string{}
			child := usageParser{p, fields, childStates}
			if path, ok := child.text("usage_billing_path"); ok {
				u.UsageBillingPath = strings.ToLower(strings.TrimSpace(path))
			}
			for key, state := range childStates {
				u.OtherFields["admin_info."+key] = state
			}
		}
	}
	f.ImageInputTokens = v.first("image_input", "image_tokens")
	f.ImageOutputTokens = v.first("image_output_tokens")
	if value(f.ImageInputTokens) == 0 && value(f.ImageOutputTokens) == 0 && u.UsageBillingPath != "" {
		f.ImageInputTokens = v.first("image_output")
	}
	f.AudioInputTokens = v.first("audio_input", "audio_input_token_count")
	f.AudioOutputTokens = v.first("audio_output")
	if f.TokenNameSnapshot != nil && strings.TrimSpace(*f.TokenNameSnapshot) == "模型测试" {
		f.CacheReadTokens, f.CacheWriteTokens, f.CacheWrite5mTokens, f.CacheWrite1hTokens = ptr(0), ptr(0), ptr(0), ptr(0)
		u.CachePolicy = "channel_model_test"
	}
	cacheTotal, cacheOK := safeSum(value(f.CacheReadTokens), value(f.CacheWriteTokens))
	if !cacheOK {
		p.issue("usage_overflow")
	}
	// Preserve the established fallback only for old text-only records that
	// contain no modern price snapshot. Explicit markers always win.
	if usable && cacheOK && u.UsageSemantic != "anthropic" && !hasModernSnapshot(prices) && value(f.ImageInputTokens) == 0 && value(f.ImageOutputTokens) == 0 && value(f.AudioInputTokens) == 0 && value(f.AudioOutputTokens) == 0 && f.SourcePromptTokens != nil && cacheTotal > *f.SourcePromptTokens {
		u.UsageSemantic, u.SemanticBasis = "anthropic", "legacy_cache_exceeds_prompt"
	}
	// Malformed values remain issues; they are never treated as valid zero
	// channels when producing derived usage. Valid absent aliases retain NULL
	// while the legacy normalization branch assumes no reported extra channel.
	usageValid := usable && cacheOK && !p.issues["usage_value_invalid"] && !p.issues["usage_overflow"] && !p.issues["token_value_invalid"]
	if usageValid && f.SourcePromptTokens != nil {
		pricingInput := *f.SourcePromptTokens
		contextTokens := *f.SourcePromptTokens
		if u.UsageSemantic == "anthropic" {
			var ok bool
			contextTokens, ok = safeSum(contextTokens, cacheTotal)
			if !ok {
				p.issue("usage_overflow")
				usageValid = false
			}
		} else {
			pricingInput = subtract(pricingInput, cacheTotal)
		}
		if usageValid {
			pricingInput = subtract(pricingInput, value(f.ImageInputTokens))
			u.PricingInputTokens, u.ContextTokens = ptr(pricingInput), ptr(contextTokens)
			f.InputTokens = ptr(subtract(pricingInput, value(f.AudioInputTokens)))
		}
	}
	if usageValid && f.SourceCompletionTokens != nil {
		f.OutputTokens = ptr(subtract(subtract(*f.SourceCompletionTokens, value(f.ImageOutputTokens)), value(f.AudioOutputTokens)))
	}
	f.UsageContextJSON, _ = json.Marshal(u)
}

func hasModernSnapshot(prices map[string]json.RawMessage) bool {
	for _, raw := range prices {
		if !bytes.Equal(raw, []byte(`""`)) {
			return true
		}
	}
	return false
}

// JSON database columns are not a lossless store for arbitrary decimal JSON
// numbers. The immutable evidence retains each original subvalue; the versioned
// pricing projection uses exact decimal strings even inside rules and tools.
func exactStructuredPricing(raw []byte) (json.RawMessage, bool) {
	d := json.NewDecoder(bytes.NewReader(raw))
	d.UseNumber()
	var root any
	if d.Decode(&root) != nil {
		return nil, false
	}
	var project func(any) (any, bool)
	project = func(value any) (any, bool) {
		switch typed := value.(type) {
		case json.Number:
			text, ok := exactDecimal([]byte(typed.String()))
			return text, ok
		case []any:
			for i, item := range typed {
				projected, ok := project(item)
				if !ok {
					return nil, false
				}
				typed[i] = projected
			}
		case map[string]any:
			for key, item := range typed {
				projected, ok := project(item)
				if !ok {
					return nil, false
				}
				typed[key] = projected
			}
		}
		return value, true
	}
	projected, ok := project(root)
	if !ok {
		return nil, false
	}
	encoded, err := json.Marshal(projected)
	return encoded, err == nil
}

func ptr(v int64) *int64 { return &v }
func value(v *int64) int64 {
	if v == nil {
		return 0
	}
	return *v
}
func subtract(a, b int64) int64 {
	if b >= a {
		return 0
	}
	return a - b
}
func safeSum(a, b int64) (int64, bool) {
	if a < 0 || b < 0 || a > math.MaxInt64-b {
		return 0, false
	}
	return a + b, true
}

func (u *usageParser) raw(key string) (json.RawMessage, bool) {
	v, exists := u.values[key]
	if !exists {
		u.states[key] = "missing"
		return nil, false
	}
	if bytes.Equal(bytes.TrimSpace(v), []byte("null")) {
		u.states[key] = "json_null"
		return nil, false
	}
	if u.states[key] != "invalid" {
		u.states[key] = "value"
	}
	return v, true
}
func (u *usageParser) invalid(key, issue string) {
	u.states[key] = "invalid"
	u.parent.issue(issue)
}
func (u *usageParser) text(key string) (string, bool) {
	raw, ok := u.raw(key)
	if !ok {
		return "", false
	}
	var s string
	if json.Unmarshal(raw, &s) != nil {
		u.invalid(key, "pricing_value_invalid")
		return "", false
	}
	return s, true
}

func (u *usageParser) decimal(key string) (string, bool) {
	raw, ok := u.raw(key)
	if !ok {
		return "", false
	}
	s, ok := exactDecimal(raw)
	if !ok {
		u.invalid(key, "pricing_value_invalid")
	}
	return s, ok
}

// Keep finite decimal lexemes exactly. Bounds prevent enormous exponents from
// consuming unbounded memory; no precision is rounded or truncated.
func exactDecimal(raw []byte) (string, bool) {
	s := string(raw)
	if len(raw) > 0 && raw[0] == '"' {
		if json.Unmarshal(raw, &s) != nil {
			return "", false
		}
	}
	if len(s) == 0 || len(s) > 4096 || !decimalSyntax.MatchString(s) {
		return "", false
	}
	if at := strings.IndexAny(s, "eE"); at >= 0 {
		exponent, err := strconv.ParseInt(s[at+1:], 10, 32)
		if err != nil || exponent < -4096 || exponent > 4096 {
			return "", false
		}
	}
	return s, true
}

func (u *usageParser) integer(key string) *int64 {
	raw, ok := u.raw(key)
	if !ok {
		return nil
	}
	s, ok := exactDecimal(raw)
	if !ok {
		u.invalid(key, "usage_value_invalid")
		return nil
	}
	n, ok := new(big.Rat).SetString(s)
	if !ok || !n.IsInt() || !n.Num().IsInt64() || n.Sign() < 0 {
		u.invalid(key, "usage_value_invalid")
		return nil
	}
	return ptr(n.Num().Int64())
}

func (u *usageParser) first(keys ...string) *int64 {
	var selected *int64
	for _, key := range keys {
		n := u.integer(key)
		if n != nil && (selected == nil || *selected == 0) {
			selected = n
		}
	}
	return selected
}
func (u *usageParser) maximum(keys ...string) *int64 {
	var selected *int64
	for _, key := range keys {
		n := u.integer(key)
		if n != nil && (selected == nil || *n > *selected) {
			selected = n
		}
	}
	return selected
}

func duplicateJSONKey(raw []byte) bool {
	d := json.NewDecoder(bytes.NewReader(raw))
	d.UseNumber()
	var walk func(int) bool
	walk = func(depth int) bool {
		if depth > 100 {
			return true
		}
		token, err := d.Token()
		if err != nil {
			return true
		}
		delim, ok := token.(json.Delim)
		if !ok {
			return false
		}
		seen := map[string]bool{}
		for d.More() {
			if delim == '{' {
				key, err := d.Token()
				if err != nil {
					return true
				}
				name, ok := key.(string)
				if !ok || seen[name] {
					return true
				}
				seen[name] = true
			}
			if walk(depth + 1) {
				return true
			}
		}
		_, err = d.Token()
		return err != nil
	}
	if walk(0) {
		return true
	}
	_, err := d.Token()
	return err != io.EOF
}
