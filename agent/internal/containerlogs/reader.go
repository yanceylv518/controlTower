package containerlogs

import (
	"bufio"
	"bytes"
	"context"
	cl "controltower/internal/containerlog"
	"encoding/json"
	"io"
	"net/http"
	"regexp"
	"strings"
	"sync"
	"time"
	_ "time/tzdata"
)

const scanLimit = 8 * 1024 * 1024
const tailLines = 10000

type Reader struct {
	Containers []string
	Discover   func(context.Context) cl.Inventory
	Location   *time.Location
	busy       chan struct{}
	mu         sync.Mutex
	inventory  cl.Inventory
	refreshed  time.Time
}

func NewReader(names []string) *Reader {
	h, _ := NewReaderWithTimezone(names, "Asia/Shanghai")
	return h
}
func NewReaderWithTimezone(names []string, timezone string) (*Reader, error) {
	loc, err := time.LoadLocation(timezone)
	if err != nil {
		return nil, err
	}
	h := &Reader{Containers: names, Location: loc, busy: make(chan struct{}, 1)}
	h.Discover = func(ctx context.Context) cl.Inventory { return Discover(ctx, discoveryCommand, names, timezone) }
	return h, nil
}
func (h *Reader) Refresh(ctx context.Context, force bool) cl.Inventory {
	h.mu.Lock()
	defer h.mu.Unlock()
	if !force && time.Since(h.refreshed) < time.Minute {
		return h.inventory
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	h.inventory = h.Discover(ctx)
	h.refreshed = time.Now()
	return h.inventory
}
func (h *Reader) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	if r.Method == "GET" && r.URL.Path == "/containers" {
		_ = json.NewEncoder(w).Encode(h.Refresh(r.Context(), false))
		return
	}
	if r.Method != "POST" || r.URL.Path != "/query" {
		http.Error(w, "not found", 404)
		return
	}
	var q cl.Query
	r.Body = http.MaxBytesReader(w, r.Body, 4096)
	d := json.NewDecoder(r.Body)
	d.DisallowUnknownFields()
	if d.Decode(&q) != nil || q.Validate(time.Now().UTC()) != nil {
		http.Error(w, "invalid query", 400)
		return
	}
	select {
	case h.busy <- struct{}{}:
		defer func() { <-h.busy }()
	default:
		http.Error(w, "busy", 429)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	inv := h.Refresh(ctx, true)
	result := cl.Result{Status: "failed", Lines: []string{}, Error: "容器或日志来源已变化，请刷新目标后重新查询"}
	for _, source := range inv.Sources {
		if source.Container != q.Container {
			continue
		}
		if !source.Available {
			result.Error = source.Reason
			break
		}
		if q.SourceID == "" || q.SourceID != source.ID {
			break
		}
		result = ReadFiles(ctx, source.HostDir, q, h.Location)
		break
	}
	if inv.Error != "" && len(inv.Sources) == 0 {
		result.Error = inv.Error
	}
	_ = json.NewEncoder(w).Encode(result)
}

type cappedWriter struct {
	mu        sync.Mutex
	b         bytes.Buffer
	cancel    context.CancelFunc
	truncated bool
}

func (w *cappedWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	n := len(p)
	remaining := scanLimit - w.b.Len()
	if len(p) > remaining {
		p = p[:remaining]
		w.truncated = true
		w.cancel()
	}
	_, _ = w.b.Write(p)
	return n, nil
}

var secrets = regexp.MustCompile(`(?i)(["']?(?:authorization|api[_-]?key|access[_-]?token|refresh[_-]?token|password|passwd|secret|cookie)["']?\s*[:=]\s*)(?:"[^"\r\n]*"|'[^'\r\n]*'|[^\s,;}]+)`)
var bearer = regexp.MustCompile(`(?i)\bBearer\s+[a-zA-Z0-9._~+/=-]+`)
var apiKey = regexp.MustCompile(`\bsk-[a-zA-Z0-9_-]{8,}`)

func Redact(s string) string {
	s = bearer.ReplaceAllString(s, "Bearer [REDACTED]")
	s = secrets.ReplaceAllString(s, "${1}[REDACTED]")
	return apiKey.ReplaceAllString(s, "[REDACTED]")
}
func Filter(data []byte, q cl.Query, truncated bool) cl.Result {
	result := cl.Result{Status: "succeeded", Lines: []string{}, Truncated: truncated}
	matches := newMatcher(q)
	scan := bufio.NewScanner(bytes.NewReader(data))
	scan.Buffer(make([]byte, 4096), 256*1024)
	total, seen := 0, 0
	for scan.Scan() {
		seen++
		line := strings.TrimSuffix(scan.Text(), "\r")
		if !matches(line) {
			continue
		}
		line = Redact(line)
		if len(result.Lines) >= cl.MaxLines || total+len(line) > cl.MaxResultBytes {
			result.Truncated = true
			break
		}
		result.Lines = append(result.Lines, line)
		total += len(line)
	}
	if seen >= tailLines || scan.Err() != nil {
		result.Truncated = true
	}
	return result
}

var _ io.Writer = (*cappedWriter)(nil)

// GIN access status is the first numeric field after its timestamp, never token counts.
var ginStatus = regexp.MustCompile(`(?m)^\[GIN\]\s+\d{4}/\d{2}/\d{2}\s+-\s+\d{2}:\d{2}:\d{2}\s+\|\s*([1-5][0-9]{2})\s*\|`)

func ginCode(line, code string) bool {
	for _, match := range ginStatus.FindAllStringSubmatch(line, -1) {
		if match[1] == code {
			return true
		}
	}
	return false
}
