package controlpoll

import (
	"bytes"
	"compress/gzip"
	"context"
	cp "controltower/internal/controlpoll"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"
)

type contextKey struct{}
type call struct {
	req     *http.Request
	section string
	body    json.RawMessage
	reply   chan outcome
}
type outcome struct {
	result cp.Result
	err    error
}
type transport struct {
	ctx   context.Context
	queue chan call
	base  http.RoundTripper
}

// WithTransport installs one network poll scheduler shared by independent workers.
func WithTransport(ctx context.Context) context.Context {
	t := &transport{ctx: ctx, queue: make(chan call), base: http.DefaultTransport}
	go t.run()
	return context.WithValue(ctx, contextKey{}, http.RoundTripper(t))
}
func Transport(ctx context.Context) http.RoundTripper {
	if t, ok := ctx.Value(contextKey{}).(http.RoundTripper); ok {
		return t
	}
	return http.DefaultTransport
}
func (t *transport) RoundTrip(r *http.Request) (*http.Response, error) {
	section := ""
	switch {
	case strings.HasSuffix(r.URL.Path, "/log-archive/poll"):
		section = "archive"
	case strings.HasSuffix(r.URL.Path, "/container-logs/poll"):
		section = "container_logs"
	default:
		return t.base.RoundTrip(r)
	}
	defer r.Body.Close()
	var reader io.Reader = r.Body
	if r.Header.Get("Content-Encoding") == "gzip" {
		z, e := gzip.NewReader(reader)
		if e != nil {
			return nil, e
		}
		defer z.Close()
		reader = z
	}
	b, e := io.ReadAll(io.LimitReader(reader, 8*1024*1024+1))
	if e != nil {
		return nil, e
	}
	if len(b) > 8*1024*1024 {
		return nil, errors.New("control payload too large")
	}
	c := call{r, section, b, make(chan outcome, 1)}
	select {
	case t.queue <- c:
	case <-r.Context().Done():
		return nil, r.Context().Err()
	case <-t.ctx.Done():
		return nil, t.ctx.Err()
	}
	select {
	case result := <-c.reply:
		if result.err != nil {
			return nil, result.err
		}
		return &http.Response{StatusCode: result.result.Status, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(bytes.NewReader(result.result.Body)), Request: r}, nil
	case <-r.Context().Done():
		return nil, r.Context().Err()
	case <-t.ctx.Done():
		return nil, t.ctx.Err()
	}
}
func (t *transport) run() {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	pending := map[string]call{}
	for {
		select {
		case <-t.ctx.Done():
			return
		case c := <-t.queue:
			if _, exists := pending[c.section]; exists {
				c.reply <- outcome{err: errors.New("duplicate control poll")}
			} else {
				pending[c.section] = c
			}
		case <-ticker.C:
			if len(pending) == 0 {
				continue
			}
			t.send(pending)
			pending = map[string]call{}
		}
	}
}
func (t *transport) send(pending map[string]call) {
	payload := cp.Request{}
	var first *http.Request
	for name, c := range pending {
		if c.req.Context().Err() == nil {
			payload[name] = c.body
			first = c.req
		}
	}
	if first == nil {
		return
	}
	ctx, cancel := context.WithTimeout(t.ctx, 10*time.Second)
	defer cancel()
	u := *first.URL
	u.Path = strings.TrimSuffix(strings.TrimSuffix(u.Path, "/log-archive/poll"), "/container-logs/poll") + "/control/poll"
	b, _ := json.Marshal(payload)
	var compressed bytes.Buffer
	z := gzip.NewWriter(&compressed)
	_, _ = z.Write(b)
	_ = z.Close()
	r, err := http.NewRequestWithContext(ctx, "POST", u.String(), &compressed)
	var out cp.Response
	if err == nil {
		r.Header.Set("Authorization", first.Header.Get("Authorization"))
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("Content-Encoding", "gzip")
		var res *http.Response
		res, err = t.base.RoundTrip(r)
		if err == nil {
			defer res.Body.Close()
			if res.StatusCode != 200 {
				err = errors.New("control poll rejected")
			} else {
				err = json.NewDecoder(io.LimitReader(res.Body, 4*1024*1024)).Decode(&out)
			}
		}
	}
	for name, c := range pending {
		result, ok := out[name]
		e := err
		if e == nil && (!ok || result.Status < 200 || result.Status > 599 || !json.Valid(result.Body)) {
			e = errors.New("invalid control response")
		}
		c.reply <- outcome{result, e}
	}
}
