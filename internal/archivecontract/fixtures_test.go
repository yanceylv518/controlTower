package archivecontract_test

import (
	"bytes"
	"encoding/json"
	"math/big"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"
)

// These checks protect the reusable input corpus, not an archive parser. The
// legacy/source and archive adapters must eventually consume these same cases.
type fixtureCorpus struct {
	FixtureVersion int               `json:"fixture_version"`
	Synthetic      bool              `json:"synthetic"`
	Timezone       string            `json:"timezone"`
	ValueEncoding  string            `json:"value_encoding"`
	Columns        map[string]string `json:"columns"`
	Defaults       struct {
		SiteID             string             `json:"site_id"`
		DatasetID          string             `json:"dataset_id"`
		SourceGenerationID string             `json:"source_generation_id"`
		SourceValues       map[string]*string `json:"source_values"`
	} `json:"defaults"`
	Cases           []fixtureCase `json:"cases"`
	AmountScenarios []struct {
		ID               string   `json:"id"`
		CaseIDs          []string `json:"case_ids"`
		QuotaPerUnit     string   `json:"quota_per_unit"`
		ExpectedRational string   `json:"expected_rational"`
		DisplayScale     int      `json:"display_scale"`
		ExpectedDisplay  string   `json:"expected_display"`
		PerRowRoundedSum string   `json:"per_row_rounded_sum"`
	} `json:"amount_scenarios"`
}

type fixtureCase struct {
	ID                string             `json:"id"`
	Tags              []string           `json:"tags"`
	IdentityOverrides map[string]string  `json:"identity_overrides"`
	SourceOverrides   map[string]*string `json:"source_overrides"`
	Expect            struct {
		BusinessDay         *string           `json:"business_day"`
		LegacyProjection    map[string]string `json:"legacy_projection"`
		IssueCodes          []string          `json:"issue_codes"`
		RequiredBehavior    string            `json:"required_behavior"`
		ExactNumericLexemes map[string]string `json:"exact_numeric_lexemes"`
	} `json:"expect"`
}

func loadFixtureCorpus(t *testing.T) fixtureCorpus {
	t.Helper()
	raw, err := os.ReadFile("testdata/log_cases.json")
	if err != nil {
		t.Fatal(err)
	}
	var corpus fixtureCorpus
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	if err = d.Decode(&corpus); err != nil {
		t.Fatal(err)
	}
	if corpus.FixtureVersion != 1 || !corpus.Synthetic || corpus.Timezone != "Asia/Shanghai" || corpus.ValueEncoding != "utf8_sql_raw_bytes_or_sql_null" {
		t.Fatal("unsupported or non-synthetic fixture corpus")
	}
	return corpus
}

func (c fixtureCorpus) sourceValues(t *testing.T, id string) (fixtureCase, map[string]*string) {
	t.Helper()
	for _, item := range c.Cases {
		if item.ID != id {
			continue
		}
		values := make(map[string]*string, len(c.Defaults.SourceValues))
		for k, v := range c.Defaults.SourceValues {
			values[k] = v
		}
		for k, v := range item.SourceOverrides {
			if _, ok := c.Columns[k]; !ok {
				t.Fatalf("case %s has undeclared column %s", id, k)
			}
			values[k] = v
		}
		return item, values
	}
	t.Fatalf("missing fixture %s", id)
	return fixtureCase{}, nil
}

func fixtureValue(t *testing.T, values map[string]*string, key string) string {
	t.Helper()
	v, exists := values[key]
	if !exists || v == nil {
		t.Fatalf("expected known value for %s", key)
	}
	return *v
}

func TestFixtureEvidencePreservesNullAndNumericLexemes(t *testing.T) {
	c := loadFixtureCorpus(t)
	// These rows are deliberately similar to the legacy '{}' projection. They
	// must remain distinguishable when a new evidence encoder consumes them.
	seen := map[string]bool{}
	for _, id := range []string{"sql_null_other", "empty_other", "json_null_other", "empty_object_other", "bad_other"} {
		_, values := c.sourceValues(t, id)
		value, exists := values["other"]
		if !exists {
			t.Fatalf("%s: other is absent rather than SQL NULL", id)
		}
		encoded, err := json.Marshal(value)
		if err != nil {
			t.Fatal(err)
		}
		if seen[string(encoded)] {
			t.Fatalf("%s: NULL/text evidence variants became identical", id)
		}
		seen[string(encoded)] = true
	}
	_, nullOutput := c.sourceValues(t, "null_output")
	_, zeroOutput := c.sourceValues(t, "zero_output")
	if nullOutput["completion_tokens"] != nil || fixtureValue(t, zeroOutput, "completion_tokens") != "0" {
		t.Fatal("unknown output and known zero output lost their distinction")
	}
	item, highPrecision := c.sourceValues(t, "high_precision")
	id, err := strconv.ParseInt(fixtureValue(t, highPrecision, "id"), 10, 64)
	if err != nil || id != 9007199254740993 || int64(float64(id)) == id {
		t.Fatal("large-id fixture no longer exercises float64 precision loss")
	}
	var other map[string]json.RawMessage
	if err = json.Unmarshal([]byte(fixtureValue(t, highPrecision, "other")), &other); err != nil {
		t.Fatal(err)
	}
	for key, expected := range item.Expect.ExactNumericLexemes {
		if string(other[key]) != expected {
			t.Fatalf("numeric token %s changed: %s != %s", key, other[key], expected)
		}
	}
	_, malformed := c.sourceValues(t, "bad_other")
	if json.Valid([]byte(fixtureValue(t, malformed, "other"))) {
		t.Fatal("malformed JSON fixture accidentally became valid")
	}
}

func TestFixtureKeysetAndGenerationIdentity(t *testing.T) {
	c := loadFixtureCorpus(t)
	_, first := c.sourceValues(t, "ordinary_token")
	_, second := c.sourceValues(t, "same_second_second_id")
	if fixtureValue(t, first, "created_at") != fixtureValue(t, second, "created_at") || fixtureValue(t, first, "request_id") != fixtureValue(t, second, "request_id") {
		t.Fatal("keyset fixtures must share timestamp and request ID")
	}
	firstID, _ := strconv.ParseInt(fixtureValue(t, first, "id"), 10, 64)
	secondID, _ := strconv.ParseInt(fixtureValue(t, second, "id"), 10, 64)
	if firstID >= secondID {
		t.Fatal("second same-timestamp row must follow the first ID")
	}
	gen, reused := c.sourceValues(t, "next_generation_reused_id")
	if fixtureValue(t, reused, "id") != fixtureValue(t, first, "id") || gen.IdentityOverrides["dataset_id"] == c.Defaults.DatasetID || gen.IdentityOverrides["source_generation_id"] == c.Defaults.SourceGenerationID {
		t.Fatal("generation fixture must reuse the raw ID under a new dataset and generation")
	}
	identities, caseIDs, tags := map[string]bool{}, map[string]bool{}, map[string]bool{}
	for _, row := range c.Cases {
		if row.ID == "" || caseIDs[row.ID] {
			t.Fatalf("ambiguous fixture case ID %q", row.ID)
		}
		caseIDs[row.ID] = true
		for _, tag := range row.Tags {
			tags[tag] = true
		}
		_, values := c.sourceValues(t, row.ID)
		dataset, generation := c.Defaults.DatasetID, c.Defaults.SourceGenerationID
		if value := row.IdentityOverrides["dataset_id"]; value != "" {
			dataset = value
		}
		if value := row.IdentityOverrides["source_generation_id"]; value != "" {
			generation = value
		}
		identity := strings.Join([]string{dataset, generation, fixtureValue(t, values, "id")}, "/")
		if identities[identity] {
			t.Fatalf("fixture %s unexpectedly overwrites identity %s", row.ID, identity)
		}
		identities[identity] = true
	}
	for _, needed := range []string{"ordinary", "expression", "cache_5m_1h", "image", "audio", "per_request", "zero_price", "zero_output", "bad_other", "null", "same_time_multiple_id", "duplicate_request_id", "high_precision", "small_amount", "crossmonth", "generation"} {
		if !tags[needed] {
			t.Errorf("required regression scenario %q removed", needed)
		}
	}
}

func TestFixtureBeijingHalfOpenMonth(t *testing.T) {
	c := loadFixtureCorpus(t)
	zone := time.FixedZone("Asia/Shanghai", 8*60*60)
	from := time.Date(2026, 9, 1, 0, 0, 0, 0, zone).Unix()
	to := time.Date(2026, 10, 1, 0, 0, 0, 0, zone).Unix()
	for _, tc := range []struct {
		id       string
		included bool
	}{
		{"cross_month_before", false},
		{"cross_month_at", true},
		{"month_exclusive_end", false},
	} {
		row, values := c.sourceValues(t, tc.id)
		stamp, err := strconv.ParseInt(fixtureValue(t, values, "created_at"), 10, 64)
		if err != nil {
			t.Fatal(err)
		}
		day := time.Unix(stamp, 0).In(zone).Format(time.DateOnly)
		if row.Expect.BusinessDay == nil || day != *row.Expect.BusinessDay || (stamp >= from && stamp < to) != tc.included {
			t.Fatalf("%s: Beijing business day or [from,to) boundary changed", tc.id)
		}
	}
	row, undated := c.sourceValues(t, "undated_charge")
	if undated["created_at"] != nil || row.Expect.BusinessDay != nil {
		t.Fatal("undated charge acquired an invented date")
	}
}

func TestFixtureAmountsRequireRoundingAfterSum(t *testing.T) {
	c := loadFixtureCorpus(t)
	if len(c.AmountScenarios) == 0 {
		t.Fatal("missing exact amount scenario")
	}
	for _, scenario := range c.AmountScenarios {
		unit, ok := new(big.Rat).SetString(scenario.QuotaPerUnit)
		if !ok || unit.Sign() <= 0 {
			t.Fatalf("%s: invalid quota unit", scenario.ID)
		}
		total, individuallyRounded := new(big.Rat), new(big.Rat)
		for _, id := range scenario.CaseIDs {
			_, values := c.sourceValues(t, id)
			quota, valid := new(big.Int).SetString(fixtureValue(t, values, "quota"), 10)
			if !valid {
				t.Fatalf("%s: invalid integer quota", id)
			}
			amount := new(big.Rat).Quo(new(big.Rat).SetInt(quota), unit)
			total.Add(total, amount)
			rounded, _ := new(big.Rat).SetString(amount.FloatString(scenario.DisplayScale))
			individuallyRounded.Add(individuallyRounded, rounded)
		}
		if total.RatString() != scenario.ExpectedRational || total.FloatString(scenario.DisplayScale) != scenario.ExpectedDisplay || individuallyRounded.FloatString(scenario.DisplayScale) != scenario.PerRowRoundedSum || individuallyRounded.Cmp(total) == 0 {
			t.Fatalf("%s no longer demonstrates loss from rounding individual requests", scenario.ID)
		}
	}
}
