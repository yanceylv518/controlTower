package containerlogs

import (
	"context"
	cl "controltower/internal/containerlog"
	"encoding/json"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestFilterExactCombinedAndRedacted(t *testing.T) {
	q := cl.Query{RequestID: "req-123", ErrorCode: "429"}
	data := []byte("req-1234 code=429 wrong request\nreq-123 code=4290 wrong code\nreq-123 code=200 mismatch\nreq-123 {\"code\":429,\"api_key\":\"sk-abcdefghijk\",\"password\":\"private\"}\nreq-123 count=429 not an error field\n")
	got := Filter(data, q, false)
	if len(got.Lines) != 1 {
		t.Fatalf("expected exact AND match: %#v", got)
	}
	if strings.Contains(got.Lines[0], "private") || strings.Contains(got.Lines[0], "sk-abcdefghijk") {
		t.Fatal("secret returned")
	}
	if !strings.Contains(got.Lines[0], "[REDACTED]") {
		t.Fatal("missing redaction")
	}
}
func TestReaderSourceIdentityAndNoCommands(t *testing.T) {
	now := time.Now().UTC()
	id := strings.Repeat("a", 64)
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "app.log"), []byte(now.Add(-time.Second).Format(time.RFC3339)+" test log\n"), 0600); err != nil {
		t.Fatal(err)
	}
	q := cl.Query{SourceID: id, Container: "new-api", From: now.Add(-time.Minute), To: now}
	h := NewReader(nil)
	h.Discover = func(context.Context) cl.Inventory {
		return cl.Inventory{Sources: []cl.Source{{ID: id, Container: "new-api", HostDir: dir, Available: true}}}
	}
	query := func(q cl.Query) (int, cl.Result) {
		b, _ := json.Marshal(q)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest("POST", "/query", strings.NewReader(string(b))))
		var result cl.Result
		_ = json.Unmarshal(w.Body.Bytes(), &result)
		return w.Code, result
	}
	if status, result := query(q); status != 200 || len(result.Lines) != 1 {
		t.Fatal(status, result)
	}
	id = strings.Repeat("b", 64)
	if _, result := query(q); result.Status != "failed" || len(result.Lines) != 0 {
		t.Fatal("recreated container accepted", result)
	}
	q.Container = "other"
	if _, result := query(q); result.Status != "failed" {
		t.Fatal("unlisted source accepted")
	}
	q.Container = "new-api;id"
	if status, _ := query(q); status != 400 {
		t.Fatal("shell syntax accepted")
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("POST", "/containers/new-api/exec", nil))
	if w.Code != 404 {
		t.Fatal("exec endpoint exposed")
	}
}
func TestFilterLimitsAndLongLines(t *testing.T) {
	got := Filter([]byte(strings.Repeat("line\n", cl.MaxLines+1)), cl.Query{}, false)
	if !got.Truncated || len(got.Lines) != cl.MaxLines {
		t.Fatal("line cap not enforced")
	}
	got = Filter([]byte(strings.Repeat("x", 300*1024)), cl.Query{}, false)
	if !got.Truncated {
		t.Fatal("oversized line silently dropped")
	}
}

func TestKeywordIsLiteralAndCombined(t *testing.T) {
	q := cl.Query{Keyword: "model [test].*", RequestID: "req-1", ErrorCode: "500"}
	got := Filter([]byte("req-1 code=500 model [test].* failed\nreq-1 code=500 model testX failed\nreq-2 code=500 model [test].* failed\n"), q, false)
	if len(got.Lines) != 1 {
		t.Fatalf("keyword did not match literally with other filters: %#v", got)
	}
}
