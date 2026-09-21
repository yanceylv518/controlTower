package archivefacts

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"io"
	"sort"
	"strconv"
	"time"
	"unicode/utf8"
)

// Nullable values are intentional. SQL NULL, a missing token count and an
// invalid conversion must never become a normal zero in an immutable fact.
type Fact struct {
	ParserVersion          string          `json:"parser_version"`
	SchemaVersion          int             `json:"schema_version"`
	SourceLogID            int64           `json:"source_log_id,string"`
	LogDate                string          `json:"log_date"`
	CreatedUnix            int64           `json:"created_unix,string"`
	LogType                int64           `json:"log_type,string"`
	UserID                 *int64          `json:"user_id,string"`
	TokenID                *int64          `json:"token_id,string"`
	ChannelID              *int64          `json:"channel_id,string"`
	UsernameSnapshot       *string         `json:"username_snapshot"`
	TokenNameSnapshot      *string         `json:"token_name_snapshot"`
	ModelName              *string         `json:"model_name"`
	GroupName              *string         `json:"group_name"`
	RequestID              *string         `json:"request_id"`
	UpstreamRequestID      *string         `json:"upstream_request_id"`
	Quota                  *int64          `json:"quota,string"`
	SourcePromptTokens     *int64          `json:"source_prompt_tokens,string"`
	SourceCompletionTokens *int64          `json:"source_completion_tokens,string"`
	InputTokens            *int64          `json:"input_tokens,string"`
	OutputTokens           *int64          `json:"output_tokens,string"`
	CacheReadTokens        *int64          `json:"cache_read_tokens,string"`
	CacheWriteTokens       *int64          `json:"cache_write_tokens,string"`
	CacheWrite5mTokens     *int64          `json:"cache_write_5m_tokens,string"`
	CacheWrite1hTokens     *int64          `json:"cache_write_1h_tokens,string"`
	ImageInputTokens       *int64          `json:"image_input_tokens,string"`
	ImageOutputTokens      *int64          `json:"image_output_tokens,string"`
	AudioInputTokens       *int64          `json:"audio_input_tokens,string"`
	AudioOutputTokens      *int64          `json:"audio_output_tokens,string"`
	PricingSnapshotJSON    json.RawMessage `json:"pricing_snapshot"`
	UsageContextJSON       json.RawMessage `json:"usage_context"`
	EvidenceHash           [32]byte        `json:"evidence_hash"`
	RawRowHash             [32]byte        `json:"raw_row_hash"`
	FactHash               [32]byte        `json:"fact_hash"`
	ParseState             string          `json:"parse_state"`
	IssueCodes             []string        `json:"issue_codes"`
}

type Result struct {
	Evidence Evidence
	Fact     *Fact
	LogDate  string
	Issues   []string
	// Blocking flags an unscoped charged/unknown row or an unrepresentable
	// identity. Scoped fact issues remain in Fact for the later billing gate.
	Blocking bool
}

func Build(columns []Column, values map[string][]byte, schemaHash, rawRowHash [32]byte) (Result, error) {
	e, err := EncodeEvidence(columns, values, schemaHash)
	if err != nil {
		return Result{}, err
	}
	return ParseEvidence(e, rawRowHash)
}

// ParseEvidence reuses immutable bytes and never consults current users, tokens,
// channel names, prices or currency settings. Parser changes require a new
// ParserVersion and a new day version; this function does not edit old facts.
func ParseEvidence(e Evidence, rawRowHash [32]byte) (Result, error) {
	_, values, err := DecodeEvidence(e)
	if err != nil || rawRowHash == ([32]byte{}) {
		return Result{}, ErrEvidence
	}
	p := parser{values: values, states: map[string]string{}, issues: map[string]bool{}}
	r := Result{Evidence: e}
	id := p.integer("id")
	created := p.integer("created_at")
	kind := p.integer("type")
	quota := p.integer("quota")
	if id == nil || *id <= 0 {
		p.issue("log_id_invalid")
		r.Blocking = true
	}
	if created != nil && *created > 0 {
		d := time.Unix(*created, 0).In(time.FixedZone("Asia/Shanghai", 8*3600))
		if d.Year() >= 1970 && d.Year() <= 9999 {
			r.LogDate = d.Format("2006-01-02")
		}
	}
	if r.LogDate == "" {
		p.issue("date_unknown")
		// Missing quota is not proof of an uncharged row.
		r.Blocking = quota == nil || *quota != 0 || r.Blocking
	}
	if kind == nil || *kind < 1 || *kind > 6 {
		p.issue("log_type_unknown")
		if quota == nil || *quota != 0 {
			p.issue("unsupported_charge_type")
			r.Blocking = true
		}
	}
	if kind == nil || *kind != 2 || r.LogDate == "" || id == nil || *id <= 0 {
		r.Issues = p.sortedIssues()
		return r, nil
	}
	f := &Fact{
		ParserVersion: ParserVersion, SchemaVersion: FactSchemaVersion,
		SourceLogID: *id, CreatedUnix: *created, LogDate: r.LogDate, LogType: *kind,
		Quota: quota, UserID: p.integer("user_id"), TokenID: p.integer("token_id"), ChannelID: p.integer("channel_id"),
		UsernameSnapshot: p.text("username"), TokenNameSnapshot: p.text("token_name"), ModelName: p.text("model_name"),
		GroupName: p.text("group"), RequestID: p.text("request_id"), UpstreamRequestID: p.text("upstream_request_id"),
		SourcePromptTokens: p.integer("prompt_tokens"), SourceCompletionTokens: p.integer("completion_tokens"),
		EvidenceHash: e.Hash, RawRowHash: rawRowHash,
	}
	if f.UserID == nil || *f.UserID <= 0 {
		p.issue("subject_unknown")
	}
	if quota == nil {
		p.issue("quota_missing")
	}
	if f.SourcePromptTokens == nil {
		p.issue("input_token_missing")
	}
	if f.SourceCompletionTokens == nil {
		p.issue("output_token_missing")
	}
	if (f.SourcePromptTokens != nil && *f.SourcePromptTokens < 0) || (f.SourceCompletionTokens != nil && *f.SourceCompletionTokens < 0) {
		p.issue("token_value_invalid")
	}
	p.parseUsage(f)
	f.IssueCodes = p.sortedIssues()
	f.ParseState = "complete"
	if len(f.IssueCodes) != 0 {
		f.ParseState = "issues"
	}
	f.FactHash = FactDigest(*f)
	r.Fact, r.Issues = f, append([]string{}, f.IssueCodes...)
	return r, nil
}

// FactDigest binds normalized fields and their original evidence/raw hashes.
// Day-version identity is bound separately by the enclosing manifest. Hash
// fields, issue ordering, parser and schema versions are deterministic.
func FactDigest(f Fact) [32]byte {
	f.FactHash = [32]byte{}
	var err error
	// MySQL JSON may reorder object keys. Normalize structured values without
	// converting any JSON number to float64 before computing the digest.
	f.PricingSnapshotJSON, err = canonicalJSON(f.PricingSnapshotJSON)
	if err != nil {
		return [32]byte{}
	}
	f.UsageContextJSON, err = canonicalJSON(f.UsageContextJSON)
	if err != nil {
		return [32]byte{}
	}
	encoded, err := json.Marshal(f)
	if err != nil {
		return [32]byte{}
	}
	return sha256.Sum256(append([]byte("CT-BILLING-FACT\x00"), encoded...))
}

func canonicalJSON(raw json.RawMessage) (json.RawMessage, error) {
	if len(raw) == 0 {
		return json.RawMessage("null"), nil
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	d.UseNumber()
	var value any
	if err := d.Decode(&value); err != nil {
		return nil, err
	}
	if _, err := d.Token(); err != io.EOF {
		return nil, ErrEvidence
	}
	return json.Marshal(value)
}

type parser struct {
	values map[string][]byte
	states map[string]string
	issues map[string]bool
}

func (p *parser) issue(code string) { p.issues[code] = true }
func (p *parser) sortedIssues() []string {
	issues := make([]string, 0, len(p.issues))
	for code := range p.issues {
		issues = append(issues, code)
	}
	sort.Strings(issues)
	return issues
}

func (p *parser) source(name string) ([]byte, bool) {
	v, exists := p.values[name]
	if !exists {
		p.states[name] = "missing"
		return nil, false
	}
	if v == nil {
		p.states[name] = "sql_null"
		return nil, false
	}
	p.states[name] = "value"
	return v, true
}

func (p *parser) integer(name string) *int64 {
	v, known := p.source(name)
	if !known {
		return nil
	}
	n, err := strconv.ParseInt(string(v), 10, 64)
	if err != nil {
		p.states[name] = "invalid"
		p.issue("integer_invalid")
		return nil
	}
	return &n
}

func (p *parser) text(name string) *string {
	v, known := p.source(name)
	if !known {
		return nil
	}
	if !utf8.Valid(v) {
		p.states[name] = "invalid"
		p.issue("text_encoding_invalid")
		return nil
	}
	if utf8.RuneCount(v) > 255 {
		p.states[name] = "invalid"
		p.issue("text_length_exceeded")
		return nil
	}
	s := string(v)
	return &s
}
