package archivejob

import (
	"bytes"
	"encoding/json"
	"math/rand"
	"testing"
)

func FuzzLargeJSONProjection(f *testing.F) {
	for _, seed := range []string{`{}`, `{"cache_tokens":1}`, `{"ignored":[{},[],true,null,"a\\u0022"],"expr":{"x":1}}`, `null`} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, input string) {
		if len(input) > 8192 {
			return
		}
		var obj map[string]json.RawMessage
		referenceErr := json.Unmarshal([]byte(input), &obj)
		p := jsonProjection{}
		var err error
		for i := 0; i < len(input) && err == nil; i += 3 {
			end := i + 3
			if end > len(input) {
				end = len(input)
			}
			err = p.Feed([]byte(input[i:end]))
		}
		var output string
		if err == nil {
			output, err = p.Finish()
		}
		if referenceErr != nil {
			if err == nil {
				t.Fatalf("accepted invalid object %q", input)
			}
			return
		}
		if err != nil {
			t.Fatalf("rejected valid %q: %v", input, err)
		}
		var actual map[string]json.RawMessage
		if json.Unmarshal([]byte(output), &actual) != nil {
			t.Fatal(output)
		}
		for k, v := range obj {
			if summaryOtherKeys[k] {
				var a, b bytes.Buffer
				json.Compact(&a, v)
				json.Compact(&b, actual[k])
				if !bytes.Equal(a.Bytes(), b.Bytes()) {
					t.Fatalf("field %s changed", k)
				}
			}
		}
	})
}
func TestJSONProjectionGenerated(t *testing.T) {
	random := rand.New(rand.NewSource(7))
	for i := 0; i < 100; i++ {
		value := map[string]any{"ignored": []any{nil, true, random.Intn(1000), map[string]any{"cache_tokens": "ignore", "q": "中\n\"\\"}}, "cache_tokens": random.Intn(1000), "billing_expr": map[string]any{"a": []any{1, "中", false}}}
		input, _ := json.Marshal(value)
		p := jsonProjection{}
		for offset := 0; offset < len(input); {
			n := random.Intn(9) + 1
			if offset+n > len(input) {
				n = len(input) - offset
			}
			if err := p.Feed(input[offset : offset+n]); err != nil {
				t.Fatal(err)
			}
			offset += n
		}
		if _, err := p.Finish(); err != nil {
			t.Fatal(err)
		}
	}
}
