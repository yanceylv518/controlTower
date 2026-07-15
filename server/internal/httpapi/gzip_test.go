package httpapi

import (
	"compress/gzip"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestGzipJSONNegotiationAndContent(t *testing.T) {
	payload := `{"items":[{"display_key":"主渠道"}]}`
	handler := gzipJSON(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		_, _ = w.Write([]byte(payload))
	}))

	plain := httptest.NewRecorder()
	handler.ServeHTTP(plain, httptest.NewRequest(http.MethodGet, "/api/dashboard/metrics", nil))
	if plain.Header().Get("Content-Encoding") != "" || plain.Body.String() != payload {
		t.Fatalf("plain response encoding=%q body=%q", plain.Header().Get("Content-Encoding"), plain.Body.String())
	}

	request := httptest.NewRequest(http.MethodGet, "/api/dashboard/metrics", nil)
	request.Header.Set("Accept-Encoding", "br, gzip")
	compressed := httptest.NewRecorder()
	handler.ServeHTTP(compressed, request)
	if compressed.Header().Get("Content-Encoding") != "gzip" {
		t.Fatalf("encoding=%q", compressed.Header().Get("Content-Encoding"))
	}
	reader, err := gzip.NewReader(compressed.Body)
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	decoded, err := io.ReadAll(reader)
	if err != nil {
		t.Fatal(err)
	}
	if string(decoded) != payload {
		t.Fatalf("decoded=%q", decoded)
	}
}

func TestGzipJSONLeavesNonJSONUncompressed(t *testing.T) {
	handler := gzipJSON(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		_, _ = w.Write([]byte("ok"))
	}))
	request := httptest.NewRequest(http.MethodGet, "/", nil)
	request.Header.Set("Accept-Encoding", "gzip")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Header().Get("Content-Encoding") != "" || response.Body.String() != "ok" {
		t.Fatalf("unexpected response headers=%v body=%q", response.Header(), response.Body.String())
	}
}
