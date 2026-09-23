package archivejob

import "testing"

func testRow(values map[string]string) row {
	r := row{}
	for k, v := range values {
		value := v
		r[k] = &value
	}
	return r
}
func TestHistoricalDiscountGroupsAndExactAmounts(t *testing.T) {
	r := testRow(map[string]string{"id": "1", "type": "2", "user_id": "7", "channel": "199", "model_name": "m", "quota": "53250", "prompt_tokens": "9007199254740993", "completion_tokens": "10", "other": `{"model_ratio":10,"user_model_discount":0.56,"quota_before_discount":95090,"discount_quota":41840,"quota_after_discount":53250}`})
	key, a, err := parseAggregate(r)
	if err != nil {
		t.Fatal(err)
	}
	if a.Amounts["quota"] != "53250" || a.Amounts["quota_before_discount"] != "95090" || a.Amounts["prompt_tokens"] != "9007199254740993" {
		t.Fatal(a.Amounts)
	}
	// JSON property order does not change a pricing group, a second discount does.
	reordered := `{"quota_after_discount":53250,"discount_quota":41840,"quota_before_discount":95090,"user_model_discount":0.56,"model_ratio":10}`
	r["other"] = &reordered
	same, _, err := parseAggregate(r)
	if err != nil || same != key {
		t.Fatal("unstable grouping", err)
	}
	second := `{"model_ratio":10,"user_model_discount":0.8}`
	r["other"] = &second
	different, b, err := parseAggregate(r)
	if err != nil || different == key {
		t.Fatal("discounts merged", err)
	}
	if b.Amounts["quota_before_discount"] != "" || b.Amounts["quota_before_discount_missing"] != "1" {
		t.Fatal("upstream silently used user quota")
	}
	channel := "200"
	r["channel"] = &channel
	otherKey, _, _ := parseAggregate(r)
	if otherKey == different {
		t.Fatal("channels merged")
	}
}
func TestRawHashIsBinarySafeAndOrderIndependent(t *testing.T) {
	a := testRow(map[string]string{"id": "1", "other": string([]byte{0xff})})
	b := testRow(map[string]string{"other": string([]byte{0xfe}), "id": "1"})
	if rowHash(a) == rowHash(b) {
		t.Fatal("invalid UTF8 collapsed")
	}
	b["other"] = a["other"]
	if rowHash(a) != rowHash(b) {
		t.Fatal("column order affected hash")
	}
	a["other"] = nil
	empty := ""
	b["other"] = &empty
	if rowHash(a) == rowHash(b) {
		t.Fatal("NULL collapsed into empty string")
	}
}
func TestCacheDetailsAreSeparateAndMissingIsNotZero(t *testing.T) {
	r := testRow(map[string]string{"other": `{"cache_creation_tokens":30,"cache_creation_tokens_5m":10,"cache_creation_tokens_1h":20,"cache_tokens":0}`})
	_, a, err := parseAggregate(r)
	if err != nil {
		t.Fatal(err)
	}
	if a.Amounts["cache_creation_tokens"] != "30" || a.Amounts["cache_creation_tokens_5m"] != "10" || a.Amounts["cache_creation_tokens_1h"] != "20" || a.Amounts["cache_tokens"] != "0" {
		t.Fatal(a.Amounts)
	}
	if a.Amounts["cache_tokens_missing"] != "" || a.Amounts["quota_missing"] != "1" {
		t.Fatal("missing/zero collapsed")
	}
}
