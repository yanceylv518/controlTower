package dashboard

import (
	"context"
	"controltower/server/internal/billing"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

type detailTask struct {
	Key       string    `json:"key"`
	JobID     string    `json:"-"`
	Kind      string    `json:"kind"`
	Status    string    `json:"status"`
	Processed int64     `json:"processed"`
	Total     int64     `json:"total"`
	Matched   int64     `json:"matched"`
	Error     string    `json:"error,omitempty"`
	Updated   time.Time `json:"-"`
	Path      string    `json:"-"`
}
type BillingDetailsHandler struct {
	Store       BillingStatementResultStore
	Root        string
	mu          sync.Mutex
	tasks       map[string]*detailTask
	running     int
	lastCleanup time.Time
}

func (h *BillingDetailsHandler) task(key string) (detailTask, bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	t, ok := h.tasks[key]
	if !ok {
		return detailTask{}, false
	}
	return *t, true
}
func (h *BillingDetailsHandler) start(key, jobID, kind, path string, total int64, work func(context.Context, func(int64)) (int64, error)) (detailTask, bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.tasks == nil {
		h.tasks = map[string]*detailTask{}
	}
	if t, ok := h.tasks[key]; ok && t.Status != "failed" {
		if t.Status == "running" {
			return *t, true
		}
		if _, err := os.Stat(t.Path); err == nil {
			return *t, true
		}
		delete(h.tasks, key)
	}
	if h.running >= 2 {
		return detailTask{}, false
	}
	for k, t := range h.tasks {
		if t.Status != "running" && time.Since(t.Updated) > time.Hour {
			delete(h.tasks, k)
		}
	}
	t := &detailTask{Key: key, JobID: jobID, Kind: kind, Status: "running", Total: total, Updated: time.Now(), Path: path}
	h.tasks[key] = t
	h.running++
	// Expiring exported copies never removes the canonical issued bill or its detail archive.
	if time.Since(h.lastCleanup) > time.Hour {
		h.lastCleanup = time.Now()
		root := h.Root
		if root == "" {
			root = billing.DefaultBillingFileRoot
		}
		go filepath.WalkDir(filepath.Join(root, "detail-exports"), func(path string, d os.DirEntry, err error) error {
			if err == nil && !d.IsDir() {
				if info, e := d.Info(); e == nil && time.Since(info.ModTime()) > 24*time.Hour {
					_ = os.Remove(path)
				}
			}
			return nil
		})
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
		defer cancel()
		matched, err := work(ctx, func(n int64) { h.mu.Lock(); t.Processed = n; t.Updated = time.Now(); h.mu.Unlock() })
		h.mu.Lock()
		defer h.mu.Unlock()
		h.running--
		t.Updated = time.Now()
		t.Matched = matched
		if err != nil {
			log.Printf("billing details task job=%s kind=%s failed: %v", jobID, kind, err)
			t.Status = "failed"
			t.Error = "文件处理失败，请重试"
		} else {
			t.Status = "complete"
			t.Processed = t.Total
		}
	}()
	return *t, true
}
func detailKey(values ...string) string {
	sum := sha256.Sum256([]byte(strings.Join(values, "\n")))
	return hex.EncodeToString(sum[:])
}
func (h *BillingDetailsHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	id := r.URL.Query().Get("id")
	if !requireBillingJobPermission(w, r, h.Store, id) {
		return
	}
	job, err := h.Store.BillingJob(r.Context(), id)
	if err != nil || job.Status != "complete" || job.UsageVersion < 3 || job.JobType != "user_statement" || job.BillPeriod != "daily" {
		writeDashboardError(w, 404, "billing_statement_not_found")
		return
	}
	if r.Method != "GET" && r.Method != "POST" {
		writeDashboardError(w, 405, "method_not_allowed")
		return
	}
	files, err := h.Store.ListBillingStatementUserFiles(r.Context(), job.ID)
	if err != nil {
		writeDashboardError(w, 500, "billing_statement_files_failed")
		return
	}
	root := h.Root
	if root == "" {
		root = billing.DefaultBillingFileRoot
	}
	root, err = filepath.Abs(root)
	if err != nil {
		writeDashboardError(w, 500, "billing_file_unavailable")
		return
	}
	var path string
	for _, file := range files {
		if file.UserID == job.UserID && file.BillDay.Format("2006-01-02") == job.From.In(billing.BusinessLocation).Format("2006-01-02") {
			path = filepath.Join(root, filepath.FromSlash(file.RelativePath))
			break
		}
	}
	relative, e := filepath.Rel(root, path)
	if path == "" || e != nil || relative == "." || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		writeDashboardError(w, 404, "billing_file_not_found")
		return
	}
	action := r.URL.Query().Get("action")
	if action == "status" || action == "download" {
		key := r.URL.Query().Get("key")
		if len(key) != 64 {
			writeDashboardError(w, 400, "invalid_query")
			return
		}
		if _, e := hex.DecodeString(key); e != nil {
			writeDashboardError(w, 400, "invalid_query")
			return
		}
		task, ok := h.task(key)
		if !ok {
			writeDashboardError(w, 404, "billing_export_expired")
			return
		}
		if task.JobID != job.ID {
			writeDashboardError(w, 404, "billing_export_not_found")
			return
		}
		if action == "status" {
			writeDashboardJSON(w, 200, task)
			return
		}
		if task.Kind != "export" || task.Status != "complete" {
			writeDashboardError(w, 409, "billing_export_not_ready")
			return
		}
		name := job.From.In(billing.BusinessLocation).Format("2006-01-02") + "-日账单明细.zip"
		w.Header().Set("Content-Type", "application/zip")
		w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="billing-details.zip"; filename*=UTF-8''%s`, url.PathEscape(name)))
		w.Header().Set("X-Content-Type-Options", "nosniff")
		http.ServeFile(w, r, task.Path)
		return
	}
	archive := path + ".details.zip"
	if _, err = os.Stat(archive); os.IsNotExist(err) {
		if r.Method != "GET" {
			writeDashboardError(w, 409, "billing_details_preparing")
			return
		}
		key := detailKey(job.ID, "prepare")
		if t, ok := h.task(key); ok && t.Status == "failed" && r.URL.Query().Get("retry") != "1" {
			writeDashboardJSON(w, 200, map[string]any{"preparing": true, "task": t})
			return
		}
		display, _, e := billing.SettlementDisplay(job.MoneySnapshot)
		if e != nil {
			writeDashboardError(w, 500, "billing_currency_unavailable")
			return
		}
		var total int64
		rows, e := h.Store.QueryBillingStatementAggregates(r.Context(), job.ID)
		if e != nil {
			writeDashboardError(w, 500, "billing_statement_query_failed")
			return
		}
		for _, v := range rows {
			total += v.RequestCount
		}
		task, ok := h.start(key, job.ID, "prepare", archive, total, func(ctx context.Context, progress func(int64)) (int64, error) {
			tmp, e := os.CreateTemp(filepath.Dir(path), ".detail-prepare-*.zip")
			if e != nil {
				return 0, e
			}
			defer os.Remove(tmp.Name())
			defer tmp.Close()
			e = billing.ConvertSettlementDetails(ctx, tmp, path, billing.SettlementCurrencyLabel(display), progress)
			if e != nil {
				return 0, e
			}
			if e = tmp.Sync(); e != nil {
				return 0, e
			}
			if e = tmp.Close(); e != nil {
				return 0, e
			}
			return total, os.Rename(tmp.Name(), archive)
		})
		if !ok {
			writeDashboardError(w, 429, "billing_export_busy")
			return
		}
		writeDashboardJSON(w, 202, map[string]any{"preparing": true, "task": task})
		return
	} else if err != nil {
		writeDashboardError(w, 500, "billing_file_unavailable")
		return
	}
	reader, err := billing.OpenSettlementDetails(archive)
	if err != nil {
		writeDashboardError(w, 500, "billing_details_unavailable")
		return
	}
	defer reader.Close()
	filter := billing.SettlementDetailFilter{From: r.URL.Query().Get("from"), To: r.URL.Query().Get("to"), Model: r.URL.Query().Get("model"), Token: r.URL.Query().Get("token")}
	if r.Method == "POST" {
		if action != "export" {
			writeDashboardError(w, 400, "invalid_query")
			return
		}
		if err = json.NewDecoder(http.MaxBytesReader(w, r.Body, 16*1024)).Decode(&filter); err != nil {
			writeDashboardError(w, 400, "invalid_query")
			return
		}
	}
	if !validDetailFilter(filter, job) {
		writeDashboardError(w, 400, "invalid_detail_filter")
		return
	}
	if r.Method == "POST" {
		raw, _ := json.Marshal(filter)
		key := detailKey(job.ID, "export-defaults-v5", string(raw))
		dir := filepath.Join(root, "detail-exports", job.ID)
		if err = os.MkdirAll(dir, 0o755); err != nil {
			writeDashboardError(w, 500, "billing_export_unavailable")
			return
		}
		target := filepath.Join(dir, key+".zip")
		task, ok := h.start(key, job.ID, "export", target, reader.Manifest.Total, func(ctx context.Context, progress func(int64)) (int64, error) {
			source, e := billing.OpenSettlementDetails(archive)
			if e != nil {
				return 0, e
			}
			defer source.Close()
			tmp, e := os.CreateTemp(dir, ".export-*.zip")
			if e != nil {
				return 0, e
			}
			defer os.Remove(tmp.Name())
			defer tmp.Close()
			n, e := source.Export(ctx, tmp, job, filter, progress)
			if e != nil {
				return 0, e
			}
			if e = tmp.Sync(); e != nil {
				return 0, e
			}
			if e = tmp.Close(); e != nil {
				return 0, e
			}
			return n, os.Rename(tmp.Name(), target)
		})
		if !ok {
			writeDashboardError(w, 429, "billing_export_busy")
			return
		}
		writeDashboardJSON(w, 202, task)
		return
	}
	if action != "" && action != "page" {
		writeDashboardError(w, 400, "invalid_query")
		return
	}
	cursor := int64(0)
	if raw := r.URL.Query().Get("cursor"); raw != "" {
		cursor, err = strconv.ParseInt(raw, 10, 64)
		if err != nil || cursor < 0 || cursor > reader.Manifest.Total {
			writeDashboardError(w, 400, "invalid_cursor")
			return
		}
	}
	rows, next, err := reader.Page(r.Context(), filter, cursor, 100)
	if err != nil {
		writeDashboardError(w, 500, "billing_detail_query_failed")
		return
	}
	writeDashboardJSON(w, 200, map[string]any{"items": rows, "next_cursor": next, "total": reader.Manifest.Total, "currency": reader.Manifest.Currency, "models": reader.Manifest.Models, "tokens": reader.Manifest.Tokens})
}
func validDetailFilter(f billing.SettlementDetailFilter, job billing.Job) bool {
	if len(f.Model) > 512 || len(f.Token) > 512 {
		return false
	}
	var from, to time.Time
	for i, raw := range []string{f.From, f.To} {
		if raw == "" {
			continue
		}
		v, err := time.ParseInLocation("2006-01-02 15:04:05", raw, billing.BusinessLocation)
		if err != nil || v.Before(job.From) || v.After(job.To) {
			return false
		}
		if i == 0 {
			from = v
		} else {
			to = v
		}
	}
	return from.IsZero() || to.IsZero() || from.Before(to)
}
