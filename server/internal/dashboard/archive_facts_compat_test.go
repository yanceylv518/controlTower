package dashboard

import (
	facts "controltower/internal/archivefacts"
	"crypto/sha256"
	"encoding/json"
	"os"
	"strconv"
	"testing"
)

// Compare the actual existing production parser, rather than only repeating
// fixture expectations in the new parser. NULL/invalid data and float precision
// fixes intentionally have stronger semantics in immutable archive facts.
func TestArchiveFactsLegacyProductionCompatibility(t *testing.T) {
	var c struct {
		Columns  map[string]string `json:"columns"`
		Defaults struct {
			SourceValues map[string]*string `json:"source_values"`
		} `json:"defaults"`
		Cases []struct {
			ID        string             `json:"id"`
			Overrides map[string]*string `json:"source_overrides"`
		} `json:"cases"`
	}
	raw, err := os.ReadFile("../../../internal/archivecontract/testdata/log_cases.json")
	if err != nil || json.Unmarshal(raw, &c) != nil {
		t.Fatal("archive compatibility fixtures unavailable")
	}
	columns := []facts.Column{}
	for name, kind := range c.Columns {
		columns = append(columns, facts.Column{Name: name, Type: kind})
	}
	schema := sha256.Sum256([]byte("synthetic-source-schema"))
	rowHash := sha256.Sum256([]byte("synthetic-raw-row"))
	compared := 0
	for _, item := range c.Cases {
		values := map[string][]byte{}
		for k, v := range c.Defaults.SourceValues {
			if v != nil {
				values[k] = []byte(*v)
			} else {
				values[k] = nil
			}
		}
		for k, v := range item.Overrides {
			if v != nil {
				values[k] = []byte(*v)
			} else {
				values[k] = nil
			}
		}
		if item.ID == "high_precision" || values["prompt_tokens"] == nil || values["completion_tokens"] == nil || string(values["type"]) != "2" || values["created_at"] == nil {
			continue
		}
		var other map[string]any
		if json.Unmarshal(values["other"], &other) != nil || other == nil {
			continue
		}
		compared++
		t.Run(item.ID, func(t *testing.T) {
			result, err := facts.Build(columns, values, schema, rowHash)
			if err != nil || result.Fact == nil {
				t.Fatal("parse fact", err)
			}
			f := result.Fact
			prompt, _ := strconv.ParseInt(string(values["prompt_tokens"]), 10, 64)
			old := normalizeChannelTestBillingUsage(string(values["token_name"]), resolveBillingCacheSemantic(parseBillingCacheUsage(string(values["other"])), prompt))
			input, context := normalizedBillingPromptTokens(prompt, old)
			var usage facts.UsageContext
			if json.Unmarshal(f.UsageContextJSON, &usage) != nil {
				t.Fatal("usage metadata")
			}
			if usage.PricingInputTokens == nil || usage.ContextTokens == nil || *usage.PricingInputTokens != input || *usage.ContextTokens != context || usage.UsageSemantic != old.Semantic {
				t.Fatalf("legacy input semantics drift: %+v vs %+v", usage, old)
			}
			for _, pair := range []struct {
				got  *int64
				want int64
			}{{f.CacheReadTokens, old.Read}, {f.CacheWriteTokens, old.Write}, {f.CacheWrite5mTokens, old.Write5m}, {f.CacheWrite1hTokens, old.Write1h}, {f.ImageInputTokens, old.ImageInput}, {f.ImageOutputTokens, old.ImageOutput}, {f.AudioInputTokens, old.AudioInput}, {f.AudioOutputTokens, old.AudioOutput}} {
				if pair.got != nil && *pair.got != pair.want {
					t.Fatal("legacy cache or media lane drift")
				}
			}
		})
	}
	if compared < 20 {
		t.Fatalf("too few production compatibility cases: %d", compared)
	}
}
