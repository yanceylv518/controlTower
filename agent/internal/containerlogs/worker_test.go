package containerlogs

import (
	"compress/gzip"
	"context"
	cl "controltower/internal/containerlog"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestResultTransportCompression(t *testing.T) {
	result := cl.Result{Status: "succeeded", Lines: []string{strings.Repeat("\x01", cl.MaxResultBytes)}}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Content-Encoding") != "gzip" {
			t.Error("result not compressed")
		}
		z, e := gzip.NewReader(r.Body)
		if e != nil {
			t.Error(e)
			w.WriteHeader(400)
			return
		}
		defer z.Close()
		var got cl.Result
		if e = json.NewDecoder(z).Decode(&got); e != nil || len(got.Lines) != 1 || len(got.Lines[0]) != cl.MaxResultBytes {
			t.Error("result truncated", e)
		}
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer server.Close()
	var response map[string]bool
	if e := request(context.Background(), server.Client(), "POST", server.URL, "test-token", result, &response); e != nil || !response["ok"] {
		t.Fatal(e)
	}
}

// The reader omits empty fields; a successful reply must not inherit the
// pre-filled "reader unavailable" error, and a dead reader must still fail.
func TestExecuteQueryUsesFreshResult(t *testing.T) {
	now := time.Now().UTC()
	q := cl.Query{SourceID: strings.Repeat("a", 64), Container: "new-api", From: now.Add(-10 * time.Minute), To: now}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"status":"succeeded","lines":["a.log:1 ok"],"files_scanned":1}`))
	}))
	defer server.Close()
	// The worker addresses the reader as http://unix/...; route that host to the test server.
	client := &http.Client{Transport: &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, "tcp", server.Listener.Addr().String())
	}}}
	got := executeQuery(context.Background(), client, q)
	if got.Status != "succeeded" || got.Error != "" || len(got.Lines) != 1 {
		t.Fatalf("stale failure text leaked into a successful result: %+v", got)
	}
	server.Close()
	got = executeQuery(context.Background(), client, q)
	if got.Status != "failed" || got.Error == "" || got.Lines == nil {
		t.Fatalf("dead reader must produce a failed result: %+v", got)
	}
	if got := executeQuery(context.Background(), client, cl.Query{}); got.Status != "failed" || got.Error == "" {
		t.Fatalf("invalid query must not reach the reader: %+v", got)
	}
}
