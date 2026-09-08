package containerlogs

import (
	"compress/gzip"
	"context"
	cl "controltower/internal/containerlog"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
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
