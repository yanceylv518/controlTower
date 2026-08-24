package dashboard

import (
	"bytes"
	"context"
	"database/sql"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"controltower/server/internal/billing"
)

type BillingFileStore interface {
	ActiveBillingUserDailyFile(context.Context, string, time.Time, int64) (billing.UserDailyFile, error)
}

type BillingFileDetailStore interface {
	ListBillingRequestDetails(context.Context, string, time.Time, int64) ([]billing.RequestDetail, error)
	BillingActiveDays(context.Context, string, int64, time.Time, time.Time) (map[string]string, error)
}

type BillingFileHandler struct {
	Store BillingFileStore
	Root  string
}

func (h BillingFileHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	site := strings.TrimSpace(r.URL.Query().Get("instance_id"))
	userID, err := strconv.ParseInt(r.URL.Query().Get("user_id"), 10, 64)
	day, dayErr := time.ParseInLocation("2006-01-02", r.URL.Query().Get("day"), billing.BusinessLocation)
	if err != nil || dayErr != nil || site == "" || userID <= 0 {
		writeDashboardError(w, http.StatusBadRequest, "invalid_query")
		return
	}
	if !billingSiteAllowed(r, site, userID) {
		writeDashboardError(w, http.StatusForbidden, "forbidden")
		return
	}
	modelName := strings.TrimSpace(r.URL.Query().Get("model_name"))
	item, err := h.Store.ActiveBillingUserDailyFile(r.Context(), site, day, userID)
	if err == sql.ErrNoRows && modelName != "" {
		if detailStore, ok := h.Store.(BillingFileDetailStore); ok {
			active, activeErr := detailStore.BillingActiveDays(r.Context(), site, userID, day, day.AddDate(0, 0, 1))
			if activeErr != nil {
				writeDashboardError(w, http.StatusInternalServerError, "billing_active_version_query_failed")
				return
			}
			if jobID := active[day.Format("2006-01-02")]; jobID != "" {
				item = billing.UserDailyFile{JobID: jobID, InstanceID: site, BillDay: day, UserID: userID}
				err = nil
			}
		}
	}
	if err == sql.ErrNoRows {
		writeDashboardError(w, http.StatusNotFound, "billing_file_not_found")
		return
	}
	if err != nil {
		writeDashboardError(w, http.StatusInternalServerError, "billing_file_query_failed")
		return
	}
	if modelName != "" {
		detailStore, ok := h.Store.(BillingFileDetailStore)
		if !ok {
			writeDashboardError(w, http.StatusNotImplemented, "billing_details_unavailable")
			return
		}
		rows, queryErr := detailStore.ListBillingRequestDetails(r.Context(), item.JobID, day, userID)
		if queryErr != nil {
			writeDashboardError(w, http.StatusInternalServerError, "billing_details_query_failed")
			return
		}
		filtered := make([]billing.RequestDetail, 0, len(rows))
		for _, row := range rows {
			if row.ModelName == modelName {
				filtered = append(filtered, row)
			}
		}
		if len(filtered) == 0 {
			writeDashboardError(w, http.StatusNotFound, "billing_details_not_found")
			return
		}
		var output bytes.Buffer
		job := billing.Job{ID: item.JobID, InstanceID: site}
		if writeErr := billing.WriteUserDailyWorkbook(&output, job, item, filtered); writeErr != nil {
			writeDashboardError(w, http.StatusInternalServerError, "billing_file_write_failed")
			return
		}
		w.Header().Set("Content-Type", "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet")
		w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="billing-user-%d-%s-model.xlsx"`, userID, day.Format("2006-01-02")))
		w.Header().Set("X-Content-Type-Options", "nosniff")
		_, _ = w.Write(output.Bytes())
		return
	}
	root := h.Root
	if root == "" {
		root = billing.DefaultBillingFileRoot
	}
	root, err = filepath.Abs(root)
	if err != nil {
		writeDashboardError(w, http.StatusInternalServerError, "billing_file_unavailable")
		return
	}
	path := filepath.Join(root, filepath.FromSlash(item.RelativePath))
	relative, err := filepath.Rel(root, path)
	if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) || filepath.IsAbs(relative) {
		writeDashboardError(w, http.StatusInternalServerError, "billing_file_path_invalid")
		return
	}
	file, err := os.Open(path)
	if os.IsNotExist(err) {
		writeDashboardError(w, http.StatusNotFound, "billing_file_missing")
		return
	}
	if err != nil {
		writeDashboardError(w, http.StatusInternalServerError, "billing_file_unavailable")
		return
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() {
		writeDashboardError(w, http.StatusInternalServerError, "billing_file_unavailable")
		return
	}
	w.Header().Set("Content-Type", "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="billing-user-%d-%s.xlsx"`, userID, day.Format("2006-01-02")))
	w.Header().Set("X-Content-Type-Options", "nosniff")
	http.ServeContent(w, r, info.Name(), info.ModTime(), file)
}
