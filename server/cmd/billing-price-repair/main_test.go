package main

import (
	"archive/zip"
	"bytes"
	"controltower/server/internal/billing"
	"encoding/json"
	"io"
	"reflect"
	"testing"
)

func TestWritePricesPreservesLegacyFields(t *testing.T) {
	var source bytes.Buffer
	z := zip.NewWriter(&source)
	w, _ := z.Create("part-00001.jsonl")
	original := `{"request_id":"a","amount":"0.123456789012","token_id":"9007199254740993","custom":1234567890123456789}`
	io.WriteString(w, original+"\n")
	w, _ = z.Create("manifest.json")
	io.WriteString(w, `{"version":1,"total":1}`)
	z.Close()
	var out bytes.Buffer
	if err := writePrices(&out, source.Bytes(), []billing.SettlementDetailRow{{UnitPrice: "输入 6；输出 24"}}); err != nil {
		t.Fatal(err)
	}
	repaired, err := zip.NewReader(bytes.NewReader(out.Bytes()), int64(out.Len()))
	if err != nil {
		t.Fatal(err)
	}
	f, err := repaired.Open("part-00001.jsonl")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	var before, after map[string]json.RawMessage
	if err = json.Unmarshal([]byte(original), &before); err != nil {
		t.Fatal(err)
	}
	if err = json.NewDecoder(f).Decode(&after); err != nil {
		t.Fatal(err)
	}
	var label string
	if err = json.Unmarshal(after["unit_price"], &label); err != nil || label != "输入 6；输出 24" {
		t.Fatal(label, err)
	}
	delete(after, "unit_price")
	if !reflect.DeepEqual(before, after) {
		t.Fatal("non-price metadata changed")
	}
}
