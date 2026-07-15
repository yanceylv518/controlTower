package httpapi

import (
	"bytes"
	"compress/gzip"
	"io"
	"net/http"
	"strings"
	"sync"
)

var gzipWriterPool = sync.Pool{New: func() any { return gzip.NewWriter(io.Discard) }}

type bufferedResponse struct {
	header http.Header
	status int
	body   bytes.Buffer
}

func (w *bufferedResponse) Header() http.Header { return w.header }
func (w *bufferedResponse) WriteHeader(status int) {
	if w.status == 0 {
		w.status = status
	}
}
func (w *bufferedResponse) Write(data []byte) (int, error) {
	if w.status == 0 {
		w.status = http.StatusOK
	}
	return w.body.Write(data)
}

func gzipJSON(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !acceptsGzip(r.Header.Get("Accept-Encoding")) {
			next.ServeHTTP(w, r)
			return
		}
		buffered := &bufferedResponse{header: make(http.Header)}
		next.ServeHTTP(buffered, r)
		copyHeaders(w.Header(), buffered.header)
		w.Header().Add("Vary", "Accept-Encoding")
		status := buffered.status
		if status == 0 {
			status = http.StatusOK
		}
		if !strings.HasPrefix(buffered.header.Get("Content-Type"), "application/json") || buffered.body.Len() == 0 {
			w.WriteHeader(status)
			_, _ = w.Write(buffered.body.Bytes())
			return
		}
		w.Header().Set("Content-Encoding", "gzip")
		w.Header().Del("Content-Length")
		w.WriteHeader(status)
		writer := gzipWriterPool.Get().(*gzip.Writer)
		writer.Reset(w)
		_, _ = writer.Write(buffered.body.Bytes())
		_ = writer.Close()
		writer.Reset(io.Discard)
		gzipWriterPool.Put(writer)
	})
}

func acceptsGzip(value string) bool {
	for _, item := range strings.Split(value, ",") {
		parts := strings.Split(item, ";")
		if strings.TrimSpace(parts[0]) != "gzip" {
			continue
		}
		enabled := true
		for _, parameter := range parts[1:] {
			if strings.TrimSpace(parameter) == "q=0" {
				enabled = false
			}
		}
		if enabled {
			return true
		}
	}
	return false
}

func copyHeaders(target, source http.Header) {
	for key, values := range source {
		for _, value := range values {
			target.Add(key, value)
		}
	}
}
