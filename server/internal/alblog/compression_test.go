package alblog

import (
	"bytes"
	"compress/gzip"
	"compress/zlib"
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

func compressFixture(t *testing.T, payload []byte, format string) []byte {
	t.Helper()
	var buf bytes.Buffer
	var writer io.WriteCloser
	if format == "gzip" {
		writer = gzip.NewWriter(&buf)
	} else {
		writer = zlib.NewWriter(&buf)
	}
	if _, err := writer.Write(payload); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func TestSLSCompressionContract(t *testing.T) {
	payload := []byte(`{"meta":{"progress":"Complete"},"data":[]}`)
	for _, tc := range []struct{ name, header, encoding, format string }{
		{"http-gzip", "Content-Encoding", "gzip", "gzip"},
		{"sls-gzip-label-zlib", "X-Log-Compresstype", "gzip", "zlib"},
		{"sls-deflate", "X-Log-Compresstype", "deflate", "zlib"},
		{"sls-standard-gzip", "X-Log-Compresstype", "gzip", "gzip"},
		{"http-deflate", "Content-Encoding", "deflate", "zlib"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			wire := compressFixture(t, payload, tc.format)
			header := http.Header{}
			header.Set(tc.header, tc.encoding)
			read := func(body []byte, limit int64) ([]byte, error) {
				resp := &http.Response{Header: header, Body: io.NopCloser(bytes.NewReader(body))}
				return readResponseLimit(resp, limit)
			}
			got, err := read(wire, 1024)
			if err != nil || !bytes.Equal(got, payload) {
				t.Fatalf("decode failed: %v", err)
			}
			if _, err = read(wire[:len(wire)-2], 1024); err == nil {
				t.Fatal("accepted truncated compression")
			}
			if _, err = read(compressFixture(t, bytes.Repeat([]byte("a"), 2048), tc.format), 1024); err == nil {
				t.Fatal("accepted decompression over limit")
			}
		})
	}
	header := http.Header{}
	header.Set("X-Log-Compresstype", "lz4")
	if _, err := readResponseLimit(&http.Response{Header: header, Body: io.NopCloser(bytes.NewReader(payload))}, 1024); err == nil {
		t.Fatal("accepted unsupported encoding")
	}
}

func TestProbeAndMonitorSLSZlibResponse(t *testing.T) {
	now := time.Unix(1791504000, 0).UTC()
	calls := 0
	c := Client{SecretKey: "key", Now: func() time.Time { return now }, HTTP: &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		calls++
		if r.Header.Get("Accept-Encoding") != "gzip" {
			t.Fatal("unexpected compression negotiation")
		}
		body, _ := io.ReadAll(r.Body)
		payload := `{"meta":{"progress":"Complete"},"data":[]}`
		if strings.Contains(string(body), "sized_requests") {
			payload = `{"meta":{"progress":"Complete"},"data":[{"request_count":"12","latest_log_time":"1791503990"}]}`
		}
		header := http.Header{}
		header.Set("X-Log-Compresstype", "gzip")
		return &http.Response{StatusCode: 200, Header: header, Body: io.NopCloser(bytes.NewReader(compressFixture(t, []byte(payload), "zlib")))}, nil
	})}}
	if got := c.Probe(context.Background(), fixture()); got.Status != "success" || got.RequestCount == nil || *got.RequestCount != 12 {
		t.Fatal(got)
	}
	if got := c.Monitor(context.Background(), fixture()); got.Status != "no_data" {
		t.Fatal(got)
	}
	if calls != 2 {
		t.Fatal(calls)
	}
}
