package dashboard

import (
	"bytes"
	"context"
	ctauth "controltower/server/internal/auth"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"time"
)

const billingExportFormatVersion = "v1"

type billingExportTask struct {
	ID        string    `json:"id"`
	Status    string    `json:"status"`
	Error     string    `json:"error,omitempty"`
	CreatedAt time.Time `json:"created_at"`
	Owner     string    `json:"-"`
}

var billingExports = struct {
	sync.Mutex
	tasks map[string]*billingExportTask
}{tasks: map[string]*billingExportTask{}}

type BillingExportJobHandler struct {
	Workbook http.Handler
	Kind     string
}

func (h BillingExportJobHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodGet {
		id := r.URL.Query().Get("id")
		billingExports.Lock()
		task := billingExports.tasks[id]
		billingExports.Unlock()
		if task == nil || task.Owner != ctauth.Actor(r) {
			writeDashboardError(w, 404, "export_not_found")
			return
		}
		if r.URL.Query().Get("download") == "1" {
			if task.Status != "complete" {
				writeDashboardError(w, 409, "export_not_ready")
				return
			}
			ext := ".xlsx"
			if h.Kind == "token" {
				ext = ".csv"
			}
			w.Header().Set("Content-Disposition", `attachment; filename="billing-export-`+id+ext+`"`)
			http.ServeFile(w, r, billingExportPath(id, h.Kind))
			return
		}
		writeDashboardJSON(w, 200, task)
		return
	}
	if r.Method != http.MethodPost {
		writeDashboardError(w, 405, "method_not_allowed")
		return
	}
	var q map[string]string
	if json.NewDecoder(r.Body).Decode(&q) != nil {
		writeDashboardError(w, 400, "invalid_request")
		return
	}
	query := "instance_id=" + urlQuery(q["instance_id"]) + "&from=" + urlQuery(q["from"]) + "&to=" + urlQuery(q["to"])
	if q["job_id"] != "" {
		query += "&job_id=" + urlQuery(q["job_id"])
	}
	if h.Kind == "channel" {
		query += "&channel_id=" + urlQuery(q["channel_id"])
	} else {
		query += "&user_id=" + urlQuery(q["user_id"])
		if h.Kind == "token" {
			query += "&token_id=" + urlQuery(q["token_id"])
		} else {
			query += "&include_requests=" + urlQuery(q["include_requests"])
		}
	}
	fingerprint := ctauth.Actor(r) + "|" + billingExportFormatVersion + "|" + h.Kind + "|" + query
	// Without a generation job there is no stable data-version boundary. Do not
	// reuse a range-only export because late logs or pricing changes can make a
	// freshly generated workbook differ from the cached file.
	if q["job_id"] == "" {
		nonce := make([]byte, 12)
		if _, err := rand.Read(nonce); err != nil {
			nonce = []byte(time.Now().Format(time.RFC3339Nano))
		}
		fingerprint += "|uncached=" + hex.EncodeToString(nonce)
	}
	sum := sha256.Sum256([]byte(fingerprint))
	id := hex.EncodeToString(sum[:12])
	path := billingExportPath(id, h.Kind)
	billingExports.Lock()
	task := billingExports.tasks[id]
	if task == nil {
		if info, err := os.Stat(path); err == nil && info.Mode().IsRegular() && info.Size() > 0 {
			task = &billingExportTask{ID: id, Status: "complete", CreatedAt: info.ModTime(), Owner: ctauth.Actor(r)}
			billingExports.tasks[id] = task
		}
	}
	if task != nil && (task.Status == "pending" || task.Status == "running" || (task.Status == "complete" && fileExists(path))) {
		billingExports.Unlock()
		writeDashboardJSON(w, 202, task)
		return
	}
	task = &billingExportTask{ID: id, Status: "pending", CreatedAt: time.Now(), Owner: ctauth.Actor(r)}
	billingExports.tasks[id] = task
	billingExports.Unlock()
	ctx := context.WithoutCancel(r.Context())
	go func() {
		startedAt := time.Now()
		setExportStatus(id, "running", "")
		req, _ := http.NewRequestWithContext(ctx, http.MethodGet, "http://local/?"+query, nil)
		out, err := os.Create(path)
		if err == nil {
			rw := &fileDownloadWriter{header: http.Header{}, file: out}
			h.Workbook.ServeHTTP(rw, req)
			err = rw.err
			if rw.status >= 400 {
				err = rw.responseError()
			}
			out.Close()
		}
		if err != nil {
			_ = os.Remove(path)
			log.Printf("billing export failed id=%s kind=%s instance=%s user=%s channel=%s elapsed=%s: %v", id, h.Kind, q["instance_id"], q["user_id"], q["channel_id"], time.Since(startedAt).Round(time.Millisecond), err)
			setExportStatus(id, "failed", err.Error())
			return
		}
		setExportStatus(id, "complete", "")
	}()
	writeDashboardJSON(w, 202, task)
}

var errExportFailed = &exportError{"export generation failed"}

type exportError struct{ s string }

func (e *exportError) Error() string { return e.s }

type fileDownloadWriter struct {
	header http.Header
	file   *os.File
	status int
	err    error
	body   bytes.Buffer
}

func (w *fileDownloadWriter) Header() http.Header { return w.header }
func (w *fileDownloadWriter) WriteHeader(s int)   { w.status = s }
func (w *fileDownloadWriter) Write(p []byte) (int, error) {
	if w.status == 0 {
		w.status = 200
	}
	if w.status >= 400 {
		return w.body.Write(p)
	}
	n, e := w.file.Write(p)
	w.err = e
	return n, e
}
func (w *fileDownloadWriter) responseError() error {
	var payload struct {
		Error string `json:"error"`
	}
	if json.Unmarshal(w.body.Bytes(), &payload) == nil && payload.Error != "" {
		return &exportError{payload.Error}
	}
	return errExportFailed
}
func billingExportPath(id, kind string) string {
	ext := ".xlsx"
	if kind == "token" {
		ext = ".csv"
	}
	return filepath.Join(os.TempDir(), "control-tower-billing-"+id+ext)
}
func fileExists(p string) bool { _, e := os.Stat(p); return e == nil }
func setExportStatus(id, status, msg string) {
	billingExports.Lock()
	defer billingExports.Unlock()
	if t := billingExports.tasks[id]; t != nil {
		t.Status = status
		t.Error = msg
	}
}
func urlQuery(v string) string {
	req, _ := http.NewRequest(http.MethodGet, "http://local", nil)
	q := req.URL.Query()
	q.Set("v", v)
	return q.Encode()[2:]
}
