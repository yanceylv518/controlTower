package agentgateway

import (
	"bytes"
	"context"
	cp "controltower/internal/controlpoll"
	"io"
	"net/http"
)

// Set only after authenticating the outer request, never from client headers.
type controlIdentityKey struct{}

type controlWriter struct {
	header http.Header
	status int
	bytes.Buffer
}

func (w *controlWriter) Header() http.Header { return w.header }
func (w *controlWriter) WriteHeader(status int) {
	if w.status == 0 {
		w.status = status
	}
}
func (w *controlWriter) Write(b []byte) (int, error) {
	if w.status == 0 {
		w.status = 200
	}
	return w.Buffer.Write(b)
}

// Control reuses each section's validation and transaction semantics. Legacy
// endpoints remain available during rolling upgrades.
func (h Handler) Control(sections map[string]http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		instance, ok := h.authenticate(r)
		if !ok || instance == "" {
			writeError(w, 401, "instance_token_required")
			return
		}
		var p cp.Request
		if decodeJSON(w, r, &p) != nil || len(p) == 0 || len(p) > 2 {
			writeError(w, 400, "invalid_control_poll")
			return
		}
		for name := range p {
			if name != "archive" && name != "container_logs" {
				writeError(w, 400, "unknown_control_section")
				return
			}
		}
		out := cp.Response{}
		for _, name := range []string{"archive", "container_logs"} {
			body, present := p[name]
			if !present {
				continue
			}
			handler := sections[name]
			if handler == nil {
				out[name] = cp.Result{Status: 503, Body: []byte(`{"error":"control_unavailable"}`)}
				continue
			}
			sub := r.Clone(context.WithValue(r.Context(), controlIdentityKey{}, instance))
			sub.Header = r.Header.Clone()
			sub.Header.Del("Content-Encoding")
			sub.Body = io.NopCloser(bytes.NewReader(body))
			sub.ContentLength = int64(len(body))
			reply := &controlWriter{header: http.Header{}}
			handler(reply, sub)
			out[name] = cp.Result{Status: reply.status, Body: reply.Bytes()}
		}
		writeJSON(w, 200, out)
	}
}
