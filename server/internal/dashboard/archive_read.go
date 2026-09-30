package dashboard

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"

	"controltower/server/internal/archivereader"
	"controltower/server/internal/auth"
)

type ArchiveJobReader interface {
	ReadJob(context.Context, archivereader.JobQuery) (archivereader.JobPage, error)
}

type ArchiveReadHandler struct {
	Reader      ArchiveJobReader
	OptionNames func(string, map[string]map[string]bool) map[string]map[string]string
}

func (h ArchiveReadHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	u, ok := auth.CurrentUser(r)
	if !ok || !auth.HasPermission(u, "archive.manage") {
		writeDashboardError(w, 403, "forbidden")
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	v := r.URL.Query()
	q := archivereader.JobQuery{Site: v.Get("site_id"), Kind: r.PathValue("kind"), Date: v.Get("date"), Limit: 100, Version: v.Get("version"), AfterHash: v.Get("after_hash"), UserID: v.Get("user_id"), Model: v.Get("model"), ChannelID: v.Get("channel_id"), Category: v.Get("category"), Dimension: v.Get("dimension"), From: v.Get("from"), Through: v.Get("through")}
	var err error
	if v.Has("limit") {
		q.Limit, err = strconv.Atoi(v.Get("limit"))
		if err != nil {
			writeDashboardError(w, 400, "archive_invalid_query")
			return
		}
	}
	for key, target := range map[string]*int64{"after_time": &q.AfterTime, "after_id": &q.AfterID} {
		if v.Has(key) {
			*target, err = strconv.ParseInt(v.Get(key), 10, 64)
			if err != nil {
				writeDashboardError(w, 400, "archive_invalid_query")
				return
			}
		}
	}
	if q.Validate() != nil {
		writeDashboardError(w, 400, "archive_invalid_query")
		return
	}
	if h.Reader == nil {
		writeDashboardError(w, 503, "archive_readonly_unavailable")
		return
	}
	page, err := h.Reader.ReadJob(r.Context(), q)
	if err != nil {
		status, code := 503, archivereader.ReadErrorCode(err)
		switch {
		case errors.Is(err, archivereader.ErrQuery):
			status, code = 400, "archive_invalid_query"
		case errors.Is(err, archivereader.ErrIdentity):
			status, code = 409, "archive_identity_or_schema_mismatch"
		case errors.Is(err, archivereader.ErrVersion):
			status, code = 409, "archive_sealed_version_unavailable"
		case errors.Is(err, archivereader.ErrPermissions):
			code = "archive_readonly_permissions_required"
		case errors.Is(err, archivereader.ErrPageSize):
			code = "archive_read_row_too_large"
		case errors.Is(err, archivereader.ErrChannelColumn):
			code = "archive_channel_column_missing"
		case errors.Is(err, archivereader.ErrIndex):
			code = "archive_read_time_index_required"
		}
		writeDashboardError(w, status, code)
		return
	}
	if q.Kind == "overview" && h.OptionNames != nil {
		for _, item := range page.Items {
			if options, ok := item["options"].(map[string]map[string]bool); ok {
				item["option_names"] = h.OptionNames(q.Site, options)
			}
		}
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(page)
}
