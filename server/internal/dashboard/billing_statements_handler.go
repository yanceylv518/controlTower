package dashboard

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	ctauth "controltower/server/internal/auth"
	"controltower/server/internal/billing"
)

type BillingStatementsStore interface {
	CreateBillingStatementJob(context.Context, billing.Job, []billing.JobStep, string) error
	BillingStatementUpstream(context.Context, string, int64) (billing.Upstream, error)
}

type BillingStatementsHandler struct{ Store BillingStatementsStore }

func (h BillingStatementsHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeDashboardError(w, 405, "method_not_allowed")
		return
	}
	if user, ok := ctauth.CurrentUser(r); ok && user.Role != "admin" {
		writeDashboardError(w, 403, "forbidden")
		return
	}
	var req struct {
		InstanceID    string `json:"instance_id"`
		StatementType string `json:"statement_type"`
		From          string `json:"from"`
		To            string `json:"to"`
		UserID        int64  `json:"user_id"`
		UpstreamID    int64  `json:"upstream_id"`
	}
	if json.NewDecoder(r.Body).Decode(&req) != nil || !billingSiteAllowed(r, strings.TrimSpace(req.InstanceID), 0) {
		writeDashboardError(w, 400, "invalid_request")
		return
	}
	from, to, err := parseBillingInputRange(req.From, req.To)
	if err != nil {
		writeDashboardError(w, 400, "invalid_range")
		return
	}
	job, steps, err := billing.NewJob(req.InstanceID, from, to, ctauth.Actor(r))
	if err != nil {
		writeDashboardError(w, 400, "invalid_range")
		return
	}
	subjectName := ""
	switch req.StatementType {
	case "user":
		if req.UserID <= 0 {
			writeDashboardError(w, 400, "invalid_user_id")
			return
		}
		job.JobType = "user_statement"
		job.UserID = req.UserID
	case "upstream":
		if req.UpstreamID <= 0 {
			writeDashboardError(w, 400, "invalid_upstream_id")
			return
		}
		upstream, e := h.Store.BillingStatementUpstream(r.Context(), req.InstanceID, req.UpstreamID)
		if e == sql.ErrNoRows {
			writeDashboardError(w, 404, "upstream_not_found")
			return
		}
		if e != nil {
			writeDashboardError(w, 500, "upstream_query_failed")
			return
		}
		if len(upstream.Channels) == 0 {
			writeDashboardError(w, 409, "upstream_channels_missing")
			return
		}
		job.JobType = "upstream_statement"
		job.UpstreamID = req.UpstreamID
		subjectName = upstream.Name
	default:
		writeDashboardError(w, 400, "invalid_statement_type")
		return
	}
	raw := fmt.Sprintf("v1|%s|%s|%d|%s|%s", job.InstanceID, job.JobType, map[bool]int64{true: job.UserID, false: job.UpstreamID}[job.JobType == "user_statement"], from.Format(time.RFC3339), to.Format(time.RFC3339))
	sum := sha256.Sum256([]byte(raw))
	job.RequestKey = "statement:" + hex.EncodeToString(sum[:16])
	err = h.Store.CreateBillingStatementJob(r.Context(), job, steps, subjectName)
	if errors.Is(err, billing.ErrStatementDuplicate) {
		writeDashboardError(w, 409, "billing_statement_duplicate")
		return
	}
	if errors.Is(err, billing.ErrStatementQueueFull) {
		writeDashboardError(w, 409, "billing_statement_queue_full")
		return
	}
	if err != nil {
		writeDashboardError(w, 500, "billing_statement_create_failed")
		return
	}
	writeDashboardJSON(w, http.StatusAccepted, map[string]any{"accepted": true, "job": job})
}
