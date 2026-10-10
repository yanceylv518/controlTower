package archivejob

import (
	"encoding/json"
	"reflect"
	"testing"
)

func TestSummaryNormalizedUsage(t *testing.T) {
	tests := []struct {
		name, other string
		want        map[string]string
	}{
		{"image_overlap", `{"model_ratio":10,"cache_tokens":90,"image_input":30,"image_ratio":1}`, map[string]string{"input_tokens": "0", "image_input_tokens": "30", "cache_read_tokens": "90", "pricing_input_tokens": "0"}},
		{"multimedia", `{"model_ratio":10,"cache_tokens":20,"cache_creation_tokens":10,"cache_creation_tokens_5m":4,"cache_creation_tokens_1h":3,"image_input":5,"image_output_tokens":2,"audio_input":7,"audio_output":3}`, map[string]string{"input_tokens": "58", "output_tokens": "45", "cache_write_unclassified_tokens": "3", "cache_write_5m_tokens": "4", "cache_write_1h_tokens": "3", "audio_input_tokens": "7", "audio_output_tokens": "3"}},
		{"anthropic", `{"usage_semantic":"anthropic","cache_tokens":90,"cache_creation_tokens":20,"image_input":10}`, map[string]string{"input_tokens": "90", "context_tokens": "210"}},
		{"billing_path", `{"model_ratio":10,"admin_info":{"usage_billing_path":"upstream"},"image_output":30}`, map[string]string{"input_tokens": "70", "image_input_tokens": "30"}},
		{"legacy_image_diagnostic", `{"model_ratio":10,"image_output":30}`, map[string]string{"input_tokens": "100", "image_input_tokens_missing": "1"}},
		{"aliases", `{"cache_read_input_tokens":10,"cached_creation_tokens":6,"claude_cache_creation_5_m_tokens":3,"claude_cache_creation_1_h_tokens":2,"image_tokens":4,"audio_input_token_count":5}`, map[string]string{"input_tokens": "75", "cache_read_tokens": "10", "cache_write_tokens": "6", "cache_write_unclassified_tokens": "1"}},
		{"zero", `{"cache_tokens":0,"image_input":0}`, map[string]string{"image_input_tokens": "0", "cache_read_tokens": "0", "audio_input_tokens_missing": "1"}},
		{"invalid", `{"image_input":"bad"}`, map[string]string{"input_tokens_missing": "1", "image_input_tokens_missing": "1", "usage_issue_rows": "1"}},
		{"tools", `{"tool_surcharges":[{"name":"search","count":2,"price":3},{"name":"fetch","count":1,"price":2}],"request_rules":{"rule":"historical"}}`, map[string]string{"tool_calls": "3"}},
		{"invalid_tools", `{"tool_surcharges":[{"count":-1}]}`, map[string]string{"tool_calls_missing": "1", "tool_calls_invalid": "1"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := testRow(map[string]string{"prompt_tokens": "100", "completion_tokens": "50", "quota": "12345", "other": tt.other})
			_, a, err := parseAggregate(r)
			if err != nil {
				t.Fatal(err)
			}
			for k, v := range tt.want {
				if a.Amounts[k] != v {
					t.Fatalf("%s=%s want %s", k, a.Amounts[k], v)
				}
			}
			if a.Amounts["quota"] != "12345" || a.Amounts["prompt_tokens"] != "100" {
				t.Fatal("source quota/usage changed")
			}
			if a.Dimensions["summary_version"] != summaryParserVersion {
				t.Fatal("missing version")
			}
			// The bounded streaming projection must preserve exactly the same statistics.
			proj := jsonProjection{}
			for i := 0; i < len(tt.other); i++ {
				if err = proj.Feed([]byte(tt.other[i : i+1])); err != nil {
					t.Fatal(err)
				}
			}
			projected, err := proj.Finish()
			if err != nil {
				t.Fatal(err)
			}
			r["other"] = &projected
			_, b, err := parseAggregate(r)
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(a, b) {
				ar, _ := json.Marshal(a)
				br, _ := json.Marshal(b)
				t.Fatalf("stream mismatch %s / %s", ar, br)
			}
		})
	}
}

func TestSummaryNormalizationBeforeAggregation(t *testing.T) {
	// sum(max(prompt-cache-image,0)) differs from max(sum(prompt-cache-image),0).
	totals := map[string]string{}
	for _, other := range []string{`{"model_ratio":10,"cache_tokens":90,"image_input":30}`, `{"model_ratio":10,"cache_tokens":0,"image_input":0}`} {
		_, a, err := parseAggregate(testRow(map[string]string{"prompt_tokens": "100", "completion_tokens": "0", "other": other}))
		if err != nil {
			t.Fatal(err)
		}
		if err = add(totals, "input_tokens", a.Amounts["input_tokens"]); err != nil {
			t.Fatal(err)
		}
	}
	if totals["input_tokens"] != "100" {
		t.Fatal(totals)
	}
}
