package archivejob

import (
	"crypto/sha256"
	"encoding"
	"encoding/hex"
	"encoding/json"
	"strings"
	"testing"
)

func TestLargeJSONProjectionResume(t *testing.T) {
	fixtures := []string{`null`, `{}`, `{"ignored":[{"a":"b"},true,false,null,1,-1.3e+2],"cache_tokens":17,"billing_expr":{"x":[1,"a\\b",null]}}`, `{"cache_tokens":1,"cache_tokens":2,"unknown":"中😀\\\"","model_ratio":0.4}`, `{"cache_tokens":null,"billing_expr":"x\\u1234"}`}
	for _, input := range fixtures {
		for _, size := range []int{1, 2, 7, 256} {
			p := jsonProjection{}
			for offset := 0; offset < len(input); offset += size {
				end := offset + size
				if end > len(input) {
					end = len(input)
				}
				if err := p.Feed([]byte(input[offset:end])); err != nil {
					t.Fatalf("%s chunk %d: %v", input, size, err)
				}
				saved, _ := json.Marshal(p)
				p = jsonProjection{}
				if err := json.Unmarshal(saved, &p); err != nil {
					t.Fatal(err)
				}
			}
			actual, err := p.Finish()
			if err != nil {
				t.Fatal(input, err)
			}
			var original, projected map[string]json.RawMessage
			json.Unmarshal([]byte(input), &original)
			json.Unmarshal([]byte(actual), &projected)
			for key, value := range original {
				if summaryOtherKeys[key] && string(projected[key]) != string(value) {
					t.Fatalf("%s: %s != %s", key, projected[key], value)
				}
			}
		}
	}
	for _, input := range []string{`{"x":1,}`, `{"x":[1,]}`, `{"x":"bad\z"}`, `{"x":01}`, `{"x":tru}`, `{"x":1} trailing`, `[]`, `{"x":`, `{"x":"`} {
		p := jsonProjection{}
		err := p.Feed([]byte(input))
		if err == nil {
			_, err = p.Finish()
		}
		if err == nil {
			t.Fatal("accepted invalid JSON", input)
		}
	}
}
func TestLargeJSONIgnoredStringBounded(t *testing.T) {
	p := jsonProjection{}
	if err := p.Feed([]byte(`{"ignored":"`)); err != nil {
		t.Fatal(err)
	}
	chunk := []byte(strings.Repeat("x", largeChunkBytes))
	for i := 0; i < (189629491+largeChunkBytes-1)/largeChunkBytes; i++ {
		if err := p.Feed(chunk); err != nil {
			t.Fatal(err)
		}
	}
	if err := p.Feed([]byte(`","cache_tokens":"22"}`)); err != nil {
		t.Fatal(err)
	}
	out, err := p.Finish()
	if err != nil || out != `{"cache_tokens":"22"}` {
		t.Fatal(out, err)
	}
	raw, _ := json.Marshal(p)
	if len(raw) > 1024 {
		t.Fatal("projection grew with ignored payload", len(raw))
	}
}
func TestLargeHashMatchesExisting(t *testing.T) {
	v := "中文\x00" + strings.Repeat("x", largeChunkBytes+10)
	empty := ""
	r := row{"a": nil, "b": &v, "c": &empty}
	h := sha256.New()
	for _, col := range []largeColumn{{"a", 0, true}, {"b", int64(len(v)), false}, {"c", 0, false}} {
		hashFieldHeader(h, col)
		if col.Name == "b" {
			h.Write([]byte(v[:largeChunkBytes]))
			state, _ := h.(encoding.BinaryMarshaler).MarshalBinary()
			var err error
			h, err = largeHash(state)
			if err != nil {
				t.Fatal(err)
			}
			h.Write([]byte(v[largeChunkBytes:]))
		}
	}
	got := hex.EncodeToString(h.Sum(nil))
	if got != rowHash(r) || chainDigest("previous", got) != chain("previous", r) {
		t.Fatal("hash format changed")
	}
}

func TestLargeJSONHugeIgnoredKeyAndNumber(t *testing.T) {
	input := `{"` + strings.Repeat("k", 12000) + `":1,"ignored":` + strings.Repeat("1", 12000) + `,"cache_tokens":17}`
	p := jsonProjection{}
	for i := 0; i < len(input); i += 73 {
		end := i + 73
		if end > len(input) {
			end = len(input)
		}
		if err := p.Feed([]byte(input[i:end])); err != nil {
			t.Fatal(err)
		}
	}
	out, err := p.Finish()
	if err != nil || out != `{"cache_tokens":17}` {
		t.Fatal(out, err)
	}
}
