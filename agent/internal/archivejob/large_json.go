package archivejob

import (
	"encoding/json"
	"errors"
	"regexp"
)

const maxLargeSummaryBytes = 1 << 20

var jsonNumber = regexp.MustCompile(`^-?(0|[1-9][0-9]*)(\.[0-9]+)?([eE][+-]?[0-9]+)?$`)
var summaryColumns = map[string]bool{"user_id": true, "token_id": true, "model_name": true, "channel_id": true, "channel": true, "group": true, "type": true, "username": true, "token_name": true, "prompt_tokens": true, "completion_tokens": true, "quota": true, "other": true}
var summaryOtherKeys = map[string]bool{}

func init() {
	for _, k := range []string{"model_ratio", "model_price", "completion_ratio", "cache_ratio", "cache_creation_ratio", "cache_creation_5m_ratio", "cache_creation_1h_ratio", "cache_creation_ratio_1h", "cache_creation_ratio_5m", "group_ratio", "user_group_ratio", "user_model_discount", "billing_mode", "billing_expr", "expr", "expr_string", "expr_b64", "matched_tier", "billing_source", "cache_tokens", "cache_creation_tokens", "cache_creation_5m_tokens", "cache_creation_1h_tokens", "cache_creation_tokens_5m", "cache_creation_tokens_1h", "quota_before_discount", "discount_quota", "quota_after_discount"} {
		summaryOtherKeys[k] = true
	}
	for _, k := range []string{"cached_tokens", "cache_read_input_tokens", "prompt_cache_hit_tokens", "cache_write_tokens", "cached_creation_tokens", "claude_cache_creation_5_m_tokens", "claude_cache_creation_1_h_tokens", "usage_semantic", "claude", "usage_billing_path", "admin_info", "image_input", "image_tokens", "image_output", "image_output_tokens", "audio_input", "audio_input_token_count", "audio_output", "image_ratio", "request_rules", "tool_surcharges"} {
		summaryOtherKeys[k] = true
	}
}

type jsonFrame struct {
	Kind  byte
	Phase int
}

// Serializable lexer/parser: a multi-hundred-MiB ignored JSON string never
// becomes a Go string. Grammar, escapes and selected values span chunk boundaries.
// Object phases: key/end, colon, value, comma/end, required key. Array: value/end,
// comma/end, required value. Completed values are validated with encoding/json.
type jsonProjection struct {
	KeyOverflow                        bool
	Stack                              []jsonFrame
	Started, Done, InString, KeyString bool
	Escape, Unicode                    int
	Token, Key, CaptureKey             string
	Capture                            []byte
	Fields                             map[string]json.RawMessage
}

func jsonFailure() error { return errors.New("invalid_billing_other_json") }
func (p *jsonProjection) appendCapture(b byte) error {
	if p.CaptureKey != "" {
		if len(p.Capture) >= maxLargeSummaryBytes {
			return errors.New("archive_large_pricing_evidence_limit")
		}
		p.Capture = append(p.Capture, b)
	}
	return nil
}
func (p *jsonProjection) finishValue() error {
	if len(p.Stack) == 0 {
		p.Done = true
		return nil
	}
	f := &p.Stack[len(p.Stack)-1]
	if f.Kind == '{' {
		if f.Phase != 2 {
			return jsonFailure()
		}
		f.Phase = 3
	} else {
		if f.Phase != 0 && f.Phase != 2 {
			return jsonFailure()
		}
		f.Phase = 1
	}
	if len(p.Stack) == 1 && p.CaptureKey != "" {
		if !json.Valid([]byte(p.Capture)) {
			return jsonFailure()
		}
		if p.Fields == nil {
			p.Fields = map[string]json.RawMessage{}
		}
		p.Fields[p.CaptureKey] = json.RawMessage(p.Capture)
		p.CaptureKey = ""
		p.Capture = nil
		size := 0
		for _, raw := range p.Fields {
			size += len(raw)
		}
		if size > maxLargeSummaryBytes {
			return errors.New("archive_large_pricing_evidence_limit")
		}
	}
	return nil
}
func (p *jsonProjection) wantsValue() bool {
	if len(p.Stack) == 0 {
		return !p.Started
	}
	f := p.Stack[len(p.Stack)-1]
	return f.Kind == '{' && f.Phase == 2 || f.Kind == '[' && (f.Phase == 0 || f.Phase == 2)
}
func (p *jsonProjection) scalar(token string, isString bool) error {
	if p.KeyString {
		if p.KeyOverflow {
			p.Key = ""
			p.KeyOverflow = false
		} else if err := json.Unmarshal([]byte(token), &p.Key); err != nil {
			return jsonFailure()
		}
		p.Stack[len(p.Stack)-1].Phase = 1
		p.KeyString = false
		return nil
	}
	if !p.wantsValue() {
		return jsonFailure()
	}
	if !isString && token != "true" && token != "false" && token != "null" && !jsonNumber.MatchString(token) {
		return jsonFailure()
	}
	if len(p.Stack) == 0 && token != "null" {
		return jsonFailure()
	}
	p.Started = true
	return p.finishValue()
}
func jsonSpace(b byte) bool { return b == ' ' || b == '\n' || b == '\r' || b == '\t' }
func (p *jsonProjection) Feed(data []byte) error {
	for _, b := range data {
		// A scalar token ends before its delimiter; do not capture the delimiter as
		// part of a selected scalar (container delimiters are captured separately).
		if !p.InString && p.Token != "" && (jsonSpace(b) || b == ',' || b == ']' || b == '}') {
			token := p.Token
			p.Token = ""
			if err := p.scalar(token, false); err != nil {
				return err
			}
		}
		if p.Done {
			if !jsonSpace(b) {
				return jsonFailure()
			}
			continue
		}
		if !p.InString && p.Token == "" && !jsonSpace(b) && len(p.Stack) == 1 && p.Stack[0].Kind == '{' && p.Stack[0].Phase == 2 && summaryOtherKeys[p.Key] && p.CaptureKey == "" {
			p.CaptureKey = p.Key
		}
		if err := p.appendCapture(b); err != nil {
			return err
		}
		if p.InString {
			if p.KeyString {
				if len(p.Token) > 4096 {
					p.KeyOverflow = true
				} else {
					p.Token += string([]byte{b})
				}
			}
			if p.Unicode > 0 {
				if !((b >= '0' && b <= '9') || (b >= 'a' && b <= 'f') || (b >= 'A' && b <= 'F')) {
					return jsonFailure()
				}
				p.Unicode--
				continue
			}
			if p.Escape == 1 {
				p.Escape = 0
				switch b {
				case 'u':
					p.Unicode = 4
				case '"', '\\', '/', 'b', 'f', 'n', 'r', 't':
				default:
					return jsonFailure()
				}
				continue
			}
			if b == '\\' {
				p.Escape = 1
				continue
			}
			if b < 32 {
				return jsonFailure()
			}
			if b == '"' {
				p.InString = false
				token := p.Token
				p.Token = ""
				if !p.KeyString {
					token = ""
				}
				if err := p.scalar(token, true); err != nil {
					return err
				}
			}
			continue
		}
		if p.Token != "" {
			n := len(p.Token)
			if b >= '0' && b <= '9' && n >= 2 && p.Token[n-1] >= '0' && p.Token[n-1] <= '9' && p.Token[n-2] >= '0' && p.Token[n-2] <= '9' {
				continue
			}
			if len(p.Token) > 4096 {
				return jsonFailure()
			}
			p.Token += string([]byte{b})
			continue
		}
		if jsonSpace(b) {
			continue
		}
		switch b {
		case '"':
			isKey := false
			if len(p.Stack) > 0 {
				f := p.Stack[len(p.Stack)-1]
				isKey = f.Kind == '{' && (f.Phase == 0 || f.Phase == 4)
			}
			if !isKey && !p.wantsValue() {
				return jsonFailure()
			}
			p.KeyString = isKey
			p.InString = true
			if isKey {
				p.Token = "\""
			}
		case '{', '[':
			if !p.wantsValue() {
				return jsonFailure()
			}
			if len(p.Stack) == 0 && b != '{' {
				return jsonFailure()
			}
			p.Started = true
			if len(p.Stack) >= 10000 {
				return jsonFailure()
			}
			p.Stack = append(p.Stack, jsonFrame{Kind: b})
		case '}', ']':
			if len(p.Stack) == 0 {
				return jsonFailure()
			}
			f := p.Stack[len(p.Stack)-1]
			if b == '}' && (f.Kind != '{' || (f.Phase != 0 && f.Phase != 3)) || b == ']' && (f.Kind != '[' || (f.Phase != 0 && f.Phase != 1)) {
				return jsonFailure()
			}
			p.Stack = p.Stack[:len(p.Stack)-1]
			if err := p.finishValue(); err != nil {
				return err
			}
		case ':':
			if len(p.Stack) == 0 {
				return jsonFailure()
			}
			f := &p.Stack[len(p.Stack)-1]
			if f.Kind != '{' || f.Phase != 1 {
				return jsonFailure()
			}
			f.Phase = 2
		case ',':
			if len(p.Stack) == 0 {
				return jsonFailure()
			}
			f := &p.Stack[len(p.Stack)-1]
			if f.Kind == '{' {
				if f.Phase != 3 {
					return jsonFailure()
				}
				f.Phase = 4
			} else {
				if f.Phase != 1 {
					return jsonFailure()
				}
				f.Phase = 2
			}
		default:
			if !p.wantsValue() {
				return jsonFailure()
			}
			p.Token = string([]byte{b})
		}
	}
	return nil
}
func (p *jsonProjection) Finish() (string, error) {
	if !p.InString && p.Token != "" {
		t := p.Token
		p.Token = ""
		if err := p.scalar(t, false); err != nil {
			return "", err
		}
	}
	if !p.Done || p.InString {
		return "", jsonFailure()
	}
	if p.Fields == nil {
		return "{}", nil
	}
	b, err := json.Marshal(p.Fields)
	return string(b), err
}
