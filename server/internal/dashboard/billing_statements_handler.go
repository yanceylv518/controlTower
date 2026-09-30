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

type BillingStatementsHandler struct {
	Store  BillingStatementsStore
	Source BillingRatioSource
}

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
		InstanceID        string `json:"instance_id"`
		NewEdition        bool   `json:"new_edition"`
		Period            string `json:"period"`
		StatementType     string `json:"statement_type"`
		From              string `json:"from"`
		To                string `json:"to"`
		UserID            int64  `json:"user_id"`
		UpstreamID        int64  `json:"upstream_id"`
		Recalculate       bool   `json:"recalculate"`
		ExcludeZeroOutput bool   `json:"exclude_zero_output"`
	}
	if json.NewDecoder(r.Body).Decode(&req) != nil || !billingSiteAllowed(r, strings.TrimSpace(req.InstanceID), 0) {
		writeDashboardError(w, 400, "invalid_request")
		return
	}
	if !billingTypeAllowed(r, req.StatementType+"_statement") {
		writeDashboardError(w, 403, "forbidden")
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
	job.ExcludeZeroOutput = req.ExcludeZeroOutput
	job.PricingSource = billing.PricingSourceNewAPI
	if req.Recalculate {
		job.PricingSource = billing.PricingSourceRecalculate
	}
	job.UsageVersion = billing.HistoricalPriceUsageVersion
	if req.NewEdition {
		job.BillPeriod = "temporary"
		if req.Period == "monthly" {
			local := from.In(billing.BusinessLocation)
			if !from.Equal(billing.CompleteDayBoundary(from)) || local.Day() != 1 || !to.Equal(from.AddDate(0, 1, 0)) || to.After(billing.CompleteDayBoundary(time.Now())) {
				writeDashboardError(w, 400, "month_not_complete")
				return
			}
			job.BillPeriod = "monthly"
		} else if req.Period != "" && req.Period != "temporary" {
			writeDashboardError(w, 400, "invalid_bill_period")
			return
		}
		if to.After(time.Now()) {
			writeDashboardError(w, 400, "billing_range_in_future")
			return
		}
		job.UsageVersion = billing.SettlementUsageVersion
		job.PricingSource = billing.PricingSourceNewAPI
		job.ExcludeZeroOutput = false
	}
	if req.NewEdition {
		if guard, ok := h.Store.(interface {
			BeginBillingGeneration(context.Context, string) (func(), error)
		}); ok {
			release, e := guard.BeginBillingGeneration(r.Context(), req.InstanceID)
			if e != nil {
				if errors.Is(e, billing.ErrGenerationInProgress) {
					writeDashboardError(w, 409, "billing_generation_in_progress")
				} else {
					writeDashboardError(w, 500, "billing_progress_unavailable")
				}
				return
			}
			defer release()
		}
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
		if !req.Recalculate {
			job.UsageVersion = billing.SettlementUsageVersion
		}
		job.UpstreamID = req.UpstreamID
		subjectName = upstream.Name
	default:
		writeDashboardError(w, 400, "invalid_statement_type")
		return
	}
	if job.UsageVersion >= billing.SettlementUsageVersion {
		if store, ok := h.Store.(BillingSourceConfigStore); ok {
			job.DataSource, err = store.BillingDataSource(r.Context())
			if err != nil {
				writeDashboardError(w, 500, "billing_config_unavailable")
				return
			}
		}
	}
	raw := fmt.Sprintf("v2|%s|%s|%d|%s|%s|exclude-zero:%t", job.InstanceID, job.JobType, map[bool]int64{true: job.UserID, false: job.UpstreamID}[job.JobType == "user_statement"], from.Format(time.RFC3339), to.Format(time.RFC3339), job.ExcludeZeroOutput)
	raw += fmt.Sprintf("|pricing:%s|usage:%d", job.PricingSource, job.UsageVersion)
	if job.UsageVersion >= billing.SettlementUsageVersion {
		raw += "|source:" + job.DataSource + "|period:" + job.BillPeriod
	}
	sum := sha256.Sum256([]byte(raw))
	job.RequestKey = "statement:" + hex.EncodeToString(sum[:16])
	if previous, ok := h.Store.(interface {
		FailedStatementMoneySnapshot(context.Context, string) (*billing.MoneySnapshot, error)
	}); ok {
		job.MoneySnapshot, err = previous.FailedStatementMoneySnapshot(r.Context(), job.RequestKey)
		if err != nil {
			writeDashboardError(w, 500, "billing_money_snapshot_query_failed")
			return
		}
	}
	if job.MoneySnapshot == nil && h.Source != nil && job.BillPeriod != "monthly" {
		job.MoneySnapshot, err = captureBillingMoney(r.Context(), h.Source, job.InstanceID)
		if err != nil {
			writeDashboardError(w, 503, "billing_money_snapshot_unavailable")
			return
		}
	}
	err = h.Store.CreateBillingStatementJob(r.Context(), job, steps, subjectName)
	if errors.Is(err, billing.ErrDailyCurrencyMismatch) {
		writeDashboardError(w, 409, "billing_daily_currency_mismatch")
		return
	}
	if errors.Is(err, billing.ErrDailyBillsIncomplete) {
		writeDashboardError(w, 409, "billing_daily_bills_incomplete")
		return
	}
	if errors.Is(err, billing.ErrStatementNoData) {
		writeDashboardError(w, 409, "billing_period_no_consumption")
		return
	}
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
	if job.BillPeriod == "monthly" && job.UsageVersion >= billing.SettlementUsageVersion {
		job.Status = "complete"
		job.TotalSteps = 0
		job.CompletedSteps = 0
	}
	writeDashboardJSON(w, http.StatusAccepted, map[string]any{"accepted": true, "job": job})
}
