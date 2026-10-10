package agentgateway

import (
	es "controltower/internal/errorstats"
	"net/http"
	"time"
)

func (h Handler) HandleErrorStatistics(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, 405, "method_not_allowed")
		return
	}
	instance, ok := h.authenticate(r)
	if !ok {
		writeError(w, 401, "unauthorized")
		return
	}
	var b es.Batch
	if err := decodeJSON(w, r, &b); err != nil {
		writeDecodeError(w, err)
		return
	}
	if instance != "" && instance != b.InstanceID {
		writeError(w, 403, "instance_mismatch")
		return
	}
	if b.Validate(time.Now().UTC()) != nil {
		writeError(w, 400, "invalid_statistics")
		return
	}
	sink, ok := h.sink.(es.Sink)
	if !ok {
		writeError(w, 503, "statistics_unavailable")
		return
	}
	if err := sink.SaveErrorStatistics(r.Context(), b); err != nil {
		writeError(w, 500, "save_failed")
		return
	}
	writeAccepted(w)
}
