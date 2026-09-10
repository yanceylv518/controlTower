package agentgateway

import (
	ac "controltower/internal/archivecontrol"
	"net/http"
)

func (h Handler) LogArchive(store ac.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		instance, ok := h.authenticate(r)
		if !ok || instance == "" {
			writeError(w, 401, "instance_token_required")
			return
		}
		var p ac.Status
		if decodeJSON(w, r, &p) != nil || !p.Validate() {
			writeError(w, 400, "invalid_archive_status")
			return
		}
		out, err := store.PollLogArchive(r.Context(), instance, p)
		if err != nil {
			writeError(w, 500, "archive_poll_failed")
			return
		}
		writeJSON(w, 200, out)
	}
}
