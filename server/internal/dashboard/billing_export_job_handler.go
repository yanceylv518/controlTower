package dashboard

import (
	"context"
	ctauth "controltower/server/internal/auth"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"time"
)

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
			w.Header().Set("Content-Disposition", `attachment; filename="billing-export-`+id+`.xlsx"`)
			http.ServeFile(w, r, billingExportPath(id))
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
	if h.Kind == "channel" {
		query += "&channel_id=" + urlQuery(q["channel_id"])
	} else {
		query += "&user_id=" + urlQuery(q["user_id"])
	}
	sum := sha256.Sum256([]byte(ctauth.Actor(r) + "|" + h.Kind + "|" + query))
	id := hex.EncodeToString(sum[:12])
	billingExports.Lock()
	task := billingExports.tasks[id]
	if task != nil && (task.Status == "pending" || task.Status == "running" || (task.Status == "complete" && fileExists(billingExportPath(id)))) {
		billingExports.Unlock()
		writeDashboardJSON(w, 202, task)
		return
	}
	task = &billingExportTask{ID: id, Status: "pending", CreatedAt: time.Now(), Owner: ctauth.Actor(r)}
	billingExports.tasks[id] = task
	billingExports.Unlock()
	ctx := context.WithoutCancel(r.Context())
	go func() {
		setExportStatus(id, "running", "")
		req, _ := http.NewRequestWithContext(ctx, http.MethodGet, "http://local/?"+query, nil)
		out, err := os.Create(billingExportPath(id))
		if err == nil {
			rw := &fileDownloadWriter{header: http.Header{}, file: out}
			h.Workbook.ServeHTTP(rw, req)
			err = rw.err
			if rw.status >= 400 {
				err = errExportFailed
			}
			out.Close()
		}
		if err != nil {
			_ = os.Remove(billingExportPath(id))
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
}

func (w *fileDownloadWriter) Header() http.Header { return w.header }
func (w *fileDownloadWriter) WriteHeader(s int)   { w.status = s }
func (w *fileDownloadWriter) Write(p []byte) (int, error) {
	if w.status == 0 {
		w.status = 200
	}
	n, e := w.file.Write(p)
	w.err = e
	return n, e
}
func billingExportPath(id string) string {
	return filepath.Join(os.TempDir(), "control-tower-billing-"+id+".xlsx")
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
