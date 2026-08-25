package dashboard

import (
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
	item, err := h.Store.ActiveBillingUserDailyFile(r.Context(), site, day, userID)
	if err == sql.ErrNoRows {
		writeDashboardError(w, http.StatusNotFound, "billing_file_not_found")
		return
	}
	if err != nil {
		writeDashboardError(w, http.StatusInternalServerError, "billing_file_query_failed")
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
