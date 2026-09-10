package controlpoll

import (
	"bytes"
	"compress/gzip"
	"context"
	cp "controltower/internal/controlpoll"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
)

func TestSharedPollAndIndependentAcknowledgements(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.URL.Path != "/api/agent/control/poll" || r.Header.Get("Authorization") != "Bearer test" {
			t.Error("wrong endpoint or credentials")
		}
		z, err := gzip.NewReader(r.Body)
		if err != nil {
			t.Error(err)
			w.WriteHeader(400)
			return
		}
		defer z.Close()
		var p cp.Request
		if json.NewDecoder(z).Decode(&p) != nil || len(p) != 2 {
			t.Error("requests were not combined")
		}
		json.NewEncoder(w).Encode(cp.Response{"archive": {Status: 500, Body: []byte(`{"error":"failed"}`)}, "container_logs": {Status: 200, Body: []byte(`{"task":null}`)}})
	}))
	defer server.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ctx = WithTransport(ctx)
	results := make(chan int, 2)
	for _, path := range []string{"log-archive", "container-logs"} {
		go func(path string) {
			r, _ := http.NewRequestWithContext(ctx, "POST", server.URL+"/api/agent/"+path+"/poll", bytes.NewBufferString(`{}`))
			r.Header.Set("Authorization", "Bearer test")
			res, err := Transport(ctx).RoundTrip(r)
			if err != nil {
				t.Error(err)
				results <- 0
				return
			}
			defer res.Body.Close()
			io.Copy(io.Discard, res.Body)
			results <- res.StatusCode
		}(path)
	}
	a, b := <-results, <-results
	if a+b != 700 || calls.Load() != 1 {
		t.Fatalf("statuses %d %d requests %d", a, b, calls.Load())
	}
}

func TestCancelledWorkerDoesNotBlockScheduler(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ctx = WithTransport(ctx)
	worker, stop := context.WithCancel(ctx)
	stop()
	r, _ := http.NewRequestWithContext(worker, "POST", "http://unused/api/agent/log-archive/poll", bytes.NewBufferString(`{}`))
	if _, err := Transport(ctx).RoundTrip(r); err == nil {
		t.Fatal("cancelled poll accepted")
	}
}
