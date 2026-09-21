package archivefacts

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"testing"
)

var fixtureSchemaHash = sha256.Sum256([]byte("synthetic-source-schema"))
var fixtureRawHash = sha256.Sum256([]byte("synthetic-raw-row"))

type corpus struct {
	Columns  map[string]string `json:"columns"`
	Defaults struct {
		SourceValues map[string]*string `json:"source_values"`
	} `json:"defaults"`
	Cases []struct {
		ID              string             `json:"id"`
		SourceOverrides map[string]*string `json:"source_overrides"`
		Expect          struct {
			BusinessDay      *string           `json:"business_day"`
			LegacyProjection map[string]string `json:"legacy_projection"`
			IssueCodes       []string          `json:"issue_codes"`
		} `json:"expect"`
	} `json:"cases"`
}

func loadCorpus(t *testing.T) corpus {
	t.Helper()
	b, err := os.ReadFile("../archivecontract/testdata/log_cases.json")
	if err != nil {
		t.Fatal(err)
	}
	var c corpus
	if err := json.Unmarshal(b, &c); err != nil {
		t.Fatal(err)
	}
	return c
}

func sourceRow(c corpus, overrides map[string]*string) ([]Column, map[string][]byte) {
	columns := make([]Column, 0, len(c.Columns))
	for name, kind := range c.Columns {
		columns = append(columns, Column{name, kind})
	}
	values := map[string][]byte{}
	for name, text := range c.Defaults.SourceValues {
		if text == nil {
			values[name] = nil
		} else {
			values[name] = []byte(*text)
		}
	}
	for name, text := range overrides {
		if text == nil {
			values[name] = nil
		} else {
			values[name] = []byte(*text)
		}
	}
	return columns, values
}

func decodeUsage(t *testing.T, f *Fact) UsageContext {
	t.Helper()
	var usage UsageContext
	if err := json.Unmarshal(f.UsageContextJSON, &usage); err != nil {
		t.Fatal(err)
	}
	return usage
}

func TestSyntheticCorpusThroughEvidenceAndFactParser(t *testing.T) {
	c := loadCorpus(t)
	if len(c.Cases) != 30 {
		t.Fatalf("fixture cases changed: %d", len(c.Cases))
	}
	for _, item := range c.Cases {
		t.Run(item.ID, func(t *testing.T) {
			columns, values := sourceRow(c, item.SourceOverrides)
			result, err := Build(columns, values, fixtureSchemaHash, fixtureRawHash)
			if err != nil {
				t.Fatal(err)
			}
			wantDate := ""
			if item.Expect.BusinessDay != nil {
				wantDate = *item.Expect.BusinessDay
			}
			if result.LogDate != wantDate {
				t.Fatalf("date %q != %q", result.LogDate, wantDate)
			}
			for _, code := range item.Expect.IssueCodes {
				if !slices.Contains(result.Issues, code) {
					t.Fatalf("missing issue %s in %v", code, result.Issues)
				}
			}
			decodedColumns, decodedValues, err := DecodeEvidence(result.Evidence)
			if err != nil {
				t.Fatal(err)
			}
			if len(decodedColumns) != len(columns) || !reflect.DeepEqual(values, decodedValues) {
				t.Fatal("evidence lost source bytes or NULL")
			}
			reparsed, err := ParseEvidence(result.Evidence, fixtureRawHash)
			if err != nil || !reflect.DeepEqual(result, reparsed) {
				t.Fatal("reparse from immutable evidence changed result", err)
			}
			if string(values["type"]) != "2" || wantDate == "" {
				if result.Fact != nil || !result.Blocking {
					t.Fatal("unscoped source row became a usable fact")
				}
				return
			}
			f := result.Fact
			if f == nil {
				t.Fatal("a dated type-2 row was dropped")
			}
			if f.FactHash == ([32]byte{}) || f.FactHash != FactDigest(*f) {
				t.Fatal("bad fact hash")
			}
			if strconv.FormatInt(f.SourceLogID, 10) != string(values["id"]) {
				t.Fatal("source id lost precision")
			}
			if values["quota"] == nil {
				if f.Quota != nil {
					t.Fatal("NULL quota became zero")
				}
			} else if f.Quota == nil || strconv.FormatInt(*f.Quota, 10) != string(values["quota"]) {
				t.Fatal("quota changed")
			}
			if values["completion_tokens"] == nil && (f.SourceCompletionTokens != nil || f.OutputTokens != nil) {
				t.Fatal("NULL output became normal zero")
			}
			u := decodeUsage(t, f)
			projection := map[string]string{
				"prompt_tokens":         strconv.FormatInt(value(u.PricingInputTokens), 10),
				"completion_tokens":     strconv.FormatInt(value(f.SourceCompletionTokens), 10),
				"context_tokens":        strconv.FormatInt(value(u.ContextTokens), 10),
				"cache_tokens":          strconv.FormatInt(value(f.CacheReadTokens), 10),
				"cache_write_tokens":    strconv.FormatInt(value(f.CacheWriteTokens), 10),
				"cache_write_5m_tokens": strconv.FormatInt(value(f.CacheWrite5mTokens), 10),
				"cache_write_1h_tokens": strconv.FormatInt(value(f.CacheWrite1hTokens), 10),
				"image_input_tokens":    strconv.FormatInt(value(f.ImageInputTokens), 10),
				"image_output_tokens":   strconv.FormatInt(value(f.ImageOutputTokens), 10),
				"audio_input_tokens":    strconv.FormatInt(value(f.AudioInputTokens), 10),
				"audio_output_tokens":   strconv.FormatInt(value(f.AudioOutputTokens), 10),
				"usage_semantic":        u.UsageSemantic,
			}
			var prices map[string]json.RawMessage
			if err := json.Unmarshal(f.PricingSnapshotJSON, &prices); err != nil {
				t.Fatal(err)
			}
			for key, raw := range prices {
				var text string
				if json.Unmarshal(raw, &text) == nil {
					projection[key] = text
				}
			}
			for key, want := range item.Expect.LegacyProjection {
				if got := projection[key]; got != want {
					t.Errorf("legacy %s = %s, want %s", key, got, want)
				}
			}
			if item.ID == "high_precision" {
				if f.SourceLogID != 9007199254740993 || value(f.CacheReadTokens) != 9007199254740993 || projection["model_ratio"] != "0.12345678901234567890123456789" {
					t.Fatal("numeric precision was lost")
				}
			}
			if item.ID == "audio_usage" && (value(f.InputTokens) != 80 || value(f.OutputTokens) != 20) {
				t.Fatal("display lanes changed pricing/source tokens")
			}
			if item.ID == "image_explicit" && (value(f.OutputTokens) != 18 || value(f.SourceCompletionTokens) != 25) {
				t.Fatal("image output damaged original completion")
			}
		})
	}
}

func TestEvidenceWhitelistNullFramingAndBinding(t *testing.T) {
	columns, values := sourceRow(loadCorpus(t), nil)
	columns = append(columns, Column{"content", "LONGTEXT"}, Column{"ip", "VARCHAR(64)"})
	values["content"], values["ip"] = []byte("excluded request body"), []byte("excluded address")
	first, err := EncodeEvidence(columns, values, fixtureSchemaHash)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(first.Payload, values["content"]) || bytes.Contains(first.Payload, values["ip"]) {
		t.Fatal("body leaked into evidence")
	}
	values["content"] = []byte("another body")
	second, err := EncodeEvidence(columns, values, fixtureSchemaHash)
	if err != nil || first.Hash != second.Hash {
		t.Fatal("excluded columns affected evidence", err)
	}
	seen := map[[32]byte]bool{}
	for _, other := range [][]byte{nil, {}, []byte("null"), []byte("{}"), {0xff, 0x00, 0xfe}} {
		values["other"] = other
		e, err := EncodeEvidence(columns, values, fixtureSchemaHash)
		if err != nil || seen[e.Hash] {
			t.Fatal("NULL/empty/JSON/binary values aliased", err)
		}
		seen[e.Hash] = true
		_, actual, err := DecodeEvidence(e)
		if err != nil || !reflect.DeepEqual(other, actual["other"]) {
			t.Fatal("byte round trip", err)
		}
	}
	values["other"] = []byte("immutable")
	e, err := EncodeEvidence(columns, values, fixtureSchemaHash)
	if err != nil {
		t.Fatal(err)
	}
	values["other"][0] = 'X'
	if bytes.Contains(e.Payload, []byte("Xmmutable")) {
		t.Fatal("evidence aliases mutable input")
	}
	for _, change := range []func(*Evidence){
		func(v *Evidence) { v.CodecVersion++ },
		func(v *Evidence) { v.SourceSchemaHash[0] ^= 1 },
		func(v *Evidence) { v.Payload = append(v.Payload, 0) },
		func(v *Evidence) { v.Hash[0] ^= 1 },
	} {
		copy := e
		change(&copy)
		if _, _, err := DecodeEvidence(copy); err == nil {
			t.Fatal("changed evidence accepted")
		}
	}
	delete(values, "other")
	if _, err := EncodeEvidence(columns, values, fixtureSchemaHash); err == nil {
		t.Fatal("missing SELECT value silently became SQL NULL")
	}
	columns = slices.DeleteFunc(columns, func(c Column) bool { return c.Name == "other" })
	missing, err := EncodeEvidence(columns, values, fixtureSchemaHash)
	if err != nil || seen[missing.Hash] {
		t.Fatal("missing column aliases NULL", err)
	}
	if _, err := EncodeEvidence(append(columns, columns[0]), values, fixtureSchemaHash); err == nil {
		t.Fatal("duplicate schema accepted")
	}
}

func TestFactsRetainInvalidValuesAndExactNullableStates(t *testing.T) {
	cases := []struct {
		name, other          string
		wantState, wantIssue string
	}{
		{"absent", `{}`, "missing", ""},
		{"json_null", `{"cache_tokens":null}`, "json_null", ""},
		{"zero", `{"cache_tokens":0}`, "value", ""},
		{"fraction", `{"cache_tokens":1.5}`, "invalid", "usage_value_invalid"},
		{"negative", `{"cache_tokens":-1}`, "invalid", "usage_value_invalid"},
		{"overflow", `{"cache_tokens":9223372036854775808}`, "invalid", "usage_value_invalid"},
		{"split_overflow", `{"cache_creation_tokens_5m":9223372036854775807,"cache_creation_tokens_1h":1}`, "missing", "usage_overflow"},
		{"context_overflow", `{"usage_semantic":"anthropic","cache_tokens":9223372036854775807}`, "value", "usage_overflow"},
		{"bad_price", `{"model_price":"NaN"}`, "missing", "pricing_value_invalid"},
		{"duplicate", `{"cache_tokens":1,"cache_tokens":2}`, "missing", "other_duplicate_key"},
		{"nested_duplicate", `{"request_rules":[{"price":1,"price":2}]}`, "missing", "other_duplicate_key"},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			columns, values := sourceRow(loadCorpus(t), nil)
			values["other"] = []byte(tt.other)
			r, err := Build(columns, values, fixtureSchemaHash, fixtureRawHash)
			if err != nil || r.Fact == nil {
				t.Fatal("conversion error dropped type2 fact", err)
			}
			u := decodeUsage(t, r.Fact)
			if u.OtherFields["cache_tokens"] != tt.wantState {
				t.Fatal(u.OtherFields)
			}
			if tt.wantIssue != "" && !slices.Contains(r.Issues, tt.wantIssue) {
				t.Fatal(r.Issues)
			}
			if tt.name == "zero" && (r.Fact.CacheReadTokens == nil || *r.Fact.CacheReadTokens != 0) {
				t.Fatal("known zero became unknown")
			}
			if (tt.name == "absent" || tt.name == "json_null" || tt.wantState == "invalid") && r.Fact.CacheReadTokens != nil {
				t.Fatal("unknown/invalid cache became zero")
			}
			if strings.Contains(tt.wantIssue, "usage_") && r.Fact.InputTokens != nil {
				t.Fatal("bad numeric usage produced a normal derived input")
			}
		})
	}
	columns, values := sourceRow(loadCorpus(t), nil)
	values["username"] = []byte{0xff}
	values["model_name"] = []byte(strings.Repeat("字", 256))
	values["user_id"] = []byte("9223372036854775808")
	values["quota"] = []byte(strconv.FormatInt(math.MinInt64, 10))
	values["prompt_tokens"] = nil
	r, err := Build(columns, values, fixtureSchemaHash, fixtureRawHash)
	if err != nil || r.Fact == nil {
		t.Fatal(err)
	}
	if r.Fact.UsernameSnapshot != nil || r.Fact.ModelName != nil || r.Fact.UserID != nil || r.Fact.SourcePromptTokens != nil || r.Fact.InputTokens != nil || *r.Fact.Quota != math.MinInt64 {
		t.Fatal("invalid source conversion was truncated or replaced")
	}
	for _, code := range []string{"text_encoding_invalid", "text_length_exceeded", "integer_invalid", "subject_unknown", "input_token_missing"} {
		if !slices.Contains(r.Issues, code) {
			t.Fatal("missing", code, r.Issues)
		}
	}
	_, decoded, err := DecodeEvidence(r.Evidence)
	if err != nil || !reflect.DeepEqual(decoded, values) {
		t.Fatal("conversion failure lost evidence", err)
	}
}

func TestLegacySemanticBranchesAndHashes(t *testing.T) {
	for _, tt := range []struct {
		other, semantic, basis string
		prompt, context        int64
	}{
		{`{"cache_tokens":150}`, "anthropic", "legacy_cache_exceeds_prompt", 100, 250},
		{`{"cache_tokens":150,"model_ratio":1}`, "openai", "legacy_default", 0, 100},
		{`{"cache_tokens":150,"claude":true,"model_ratio":1}`, "anthropic", "explicit_marker", 100, 250},
		{`{"cache_tokens":"30","cached_tokens":40,"cache_write_tokens":8,"cache_creation_tokens":7,"cache_creation_tokens_5m":4,"cache_creation_tokens_1h":6}`, "openai", "legacy_default", 60, 100},
	} {
		t.Run(fmt.Sprint(tt.prompt, tt.context, tt.basis), func(t *testing.T) {
			columns, values := sourceRow(loadCorpus(t), nil)
			values["other"] = []byte(tt.other)
			r, err := Build(columns, values, fixtureSchemaHash, fixtureRawHash)
			if err != nil {
				t.Fatal(err)
			}
			u := decodeUsage(t, r.Fact)
			if u.UsageSemantic != tt.semantic || u.SemanticBasis != tt.basis || value(u.PricingInputTokens) != tt.prompt || value(u.ContextTokens) != tt.context {
				t.Fatalf("usage %+v", u)
			}
			changed := *r.Fact
			changed.Quota = ptr(1)
			if FactDigest(changed) == r.Fact.FactHash {
				t.Fatal("quota not bound into fact hash")
			}
			changed = *r.Fact
			changed.ParserVersion = "new-parser"
			if FactDigest(changed) == r.Fact.FactHash {
				t.Fatal("parser identity not bound into hash")
			}
			changed = *r.Fact
			changed.RawRowHash[0] ^= 1
			if FactDigest(changed) == r.Fact.FactHash {
				t.Fatal("raw row not bound into hash")
			}
		})
	}
}

func TestFactHashSurvivesJSONStorageOrderAndNestedDecimalsStayExact(t *testing.T) {
	columns, values := sourceRow(loadCorpus(t), nil)
	values["other"] = []byte(`{"request_rules":[{"ratio":0.12345678901234567890123456789,"limit":9007199254740993}],"tool_surcharges":[{"price":0.0000000000000000000001,"count":1}]}`)
	r, err := Build(columns, values, fixtureSchemaHash, fixtureRawHash)
	if err != nil || r.Fact == nil {
		t.Fatal(err)
	}
	if !bytes.Contains(r.Fact.PricingSnapshotJSON, []byte(`"ratio":"0.12345678901234567890123456789"`)) || !bytes.Contains(r.Fact.PricingSnapshotJSON, []byte(`"limit":"9007199254740993"`)) || !bytes.Contains(r.Fact.PricingSnapshotJSON, []byte(`"price":"0.0000000000000000000001"`)) {
		t.Fatal("nested pricing numbers can lose precision in a JSON database column", string(r.Fact.PricingSnapshotJSON))
	}
	changed := *r.Fact
	changed.PricingSnapshotJSON = json.RawMessage(`{ "tool_surcharges": [ { "count":"1", "price":"0.0000000000000000000001" } ], "request_rules": [ { "ratio":"0.12345678901234567890123456789", "limit":"9007199254740993" } ] }`)
	if FactDigest(changed) != r.Fact.FactHash {
		t.Fatal("JSON key order or whitespace changed fact hash")
	}
	changed.UsageContextJSON = json.RawMessage(`{"broken"`)
	if FactDigest(changed) != ([32]byte{}) {
		t.Fatal("malformed fact JSON produced a usable digest")
	}
	changed = *r.Fact
	changed.UsageContextJSON = json.RawMessage(`{} {}`)
	if FactDigest(changed) != ([32]byte{}) {
		t.Fatal("trailing JSON accepted")
	}
}

func TestEvidenceRejectsWellHashedMalformedFrames(t *testing.T) {
	columns, values := sourceRow(loadCorpus(t), nil)
	e, err := EncodeEvidence(columns, values, fixtureSchemaHash)
	if err != nil {
		t.Fatal(err)
	}
	for _, payload := range [][]byte{
		append(append([]byte{}, e.Payload...), 0),
		e.Payload[:len(e.Payload)-1],
		bytes.Replace(e.Payload, []byte("created_at"), []byte("contentxxx"), 1),
		append([]byte("CT-BILLING-EVIDENCE\x00"), bytes.Repeat([]byte{0xff}, 8)...),
	} {
		changed := e
		changed.Payload = payload
		changed.Hash = evidenceHash(changed)
		if _, _, err := DecodeEvidence(changed); err == nil {
			t.Fatal("malformed frames accepted with valid content hash")
		}
	}
}

func TestUnrepresentableDatesAndUnknownChargesNeverBecomeDatedFacts(t *testing.T) {
	for _, timestamp := range []string{"0", "-1", "9223372036854775807", "not-an-integer"} {
		columns, values := sourceRow(loadCorpus(t), nil)
		values["created_at"] = []byte(timestamp)
		r, err := Build(columns, values, fixtureSchemaHash, fixtureRawHash)
		if err != nil || r.Fact != nil || r.LogDate != "" || !r.Blocking || !slices.Contains(r.Issues, "date_unknown") {
			t.Fatalf("unrepresentable timestamp %s: %+v / %v", timestamp, r, err)
		}
	}
	columns, values := sourceRow(loadCorpus(t), nil)
	values["type"], values["quota"] = nil, nil
	r, err := Build(columns, values, fixtureSchemaHash, fixtureRawHash)
	if err != nil || r.Fact != nil || !r.Blocking || !slices.Contains(r.Issues, "unsupported_charge_type") {
		t.Fatal("unknown quota was considered uncharged", err)
	}
}
