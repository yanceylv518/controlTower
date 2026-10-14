package httpapi

import (
	"encoding/json"
	"net/http"

	"controltower/server/internal/auth"
)

// Keep legacy history readable, but never accept work for a retired executor.
func retiredArchiveTask(w http.ResponseWriter, r *http.Request) {
	u, ok := auth.CurrentUser(r)
	if !ok || !auth.HasPermission(u, "archive.manage") {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusGone)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": "legacy_archive_executor_retired", "replacement": "/api/dashboard/log-archive-jobs"})
}
