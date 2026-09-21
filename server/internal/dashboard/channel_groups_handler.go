package dashboard

import (
	"context"
	"net/http"
	"time"
)

// TuningGroupDirectory 提供站点级分组候选；直连站点由 New API 返回，
// Agent 站点由 Control Tower 的完整渠道快照兜底。
type TuningGroupDirectory interface {
	ListGroups(context.Context, string) ([]string, error)
}

// HandleTuningGroups 返回渠道分组编辑器使用的完整候选集合，不执行写操作。
func (h Handler) HandleTuningGroups(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeDashboardError(w, http.StatusMethodNotAllowed, "method_not_allowed")
		return
	}
	site := tuningSiteID(r)
	if site == "" {
		writeDashboardError(w, http.StatusBadRequest, "site_id_required")
		return
	}
	store, ok := h.tuningStore.(TuningGroupDirectory)
	if !ok {
		writeDashboardError(w, http.StatusNotImplemented, "group_directory_not_supported")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	groups, err := store.ListGroups(ctx, site)
	if err != nil {
		writeDashboardError(w, http.StatusBadGateway, "group_query_failed")
		return
	}
	writeDashboardJSON(w, http.StatusOK, map[string]any{"items": groups})
}
