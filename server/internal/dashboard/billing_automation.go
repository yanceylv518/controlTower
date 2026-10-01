package dashboard

import (
	"context"
	"controltower/server/internal/billing"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"
)

type AutomaticBillingStore interface {
	billing.AutomationStore
	BillingStatementsStore
	BillingSourceConfigStore
}
type BillingAutomation struct {
	Wake    <-chan struct{}
	Store   AutomaticBillingStore
	Source  BillingRatioSource
	Archive interface {
		FirstBillingDay(context.Context, string) (time.Time, error)
	}
}

var billingFillSites sync.Map

func (a BillingAutomation) Fill(ctx context.Context, target billing.AutomaticTarget) (result error) {
	defer func() {
		if recorder, ok := a.Store.(interface {
			RecordBillingGenerationAttempt(context.Context, billing.AutomaticTarget, error) error
		}); ok {
			recordCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			_ = recorder.RecordBillingGenerationAttempt(recordCtx, target, result)
		}
	}()

	// Explicit manual ranges continue even when future automatic billing is off.
	if target.Kind == "upstream_statement" && target.To.IsZero() {
		upstream, err := a.Store.BillingStatementUpstream(ctx, target.InstanceID, target.SubjectID)
		if err != nil {
			return err
		}
		if !upstream.Enabled {
			return nil
		}
	}
	slot, _ := billingFillSites.LoadOrStore(target.InstanceID, make(chan struct{}, 1))
	gate := slot.(chan struct{})
	select {
	case gate <- struct{}{}:
	case <-ctx.Done():
		return ctx.Err()
	}
	defer func() { <-gate }()
	end := billing.CompleteDayBoundary(time.Now())
	if !target.To.IsZero() && target.To.Before(end) {
		end = target.To
	}
	if !target.ProgressUntil.IsZero() && target.ProgressUntil.Before(end) {
		end = target.ProgressUntil
	}
	if !target.From.IsZero() && !target.From.Before(end) {
		return nil
	}
	days, err := a.Store.MissingBillingDays(ctx, target, end)
	if err != nil {
		return err
	}
	var months []time.Time
	if store, ok := a.Store.(interface {
		MissingBillingMonths(context.Context, billing.AutomaticTarget, time.Time) ([]time.Time, error)
	}); ok {
		months, err = store.MissingBillingMonths(ctx, target, end)
		if err != nil {
			return err
		}
	}
	// Completed/pending targets need no source metadata checks on every wake.
	if len(days) == 0 && len(months) == 0 {
		return nil
	}
	if target.Kind == "user_statement" && target.To.IsZero() {
		reader, ok := a.Source.(interface {
			BillingUserRole(context.Context, string, int64) (int, error)
		})
		if !ok {
			return fmt.Errorf("billing user role reader unavailable")
		}
		role, err := reader.BillingUserRole(ctx, target.InstanceID, target.SubjectID)
		if err == sql.ErrNoRows {
			return nil
		}
		if err != nil {
			return err
		}
		// NewAPI roles: ordinary user 1, administrator 10, root 100.
		if role >= 10 {
			return nil
		}
	}
	source, err := a.Store.BillingDataSource(ctx)
	if err != nil {
		return err
	}
	var fillMoney *billing.MoneySnapshot
	observeMoney := func() (*billing.MoneySnapshot, error) {
		if fillMoney == nil {
			var err error
			fillMoney, err = captureBillingMoney(ctx, a.Source, target.InstanceID)
			if err != nil {
				return nil, err
			}
		}
		return fillMoney, nil
	}
	for _, month := range months {
		if e := a.enqueue(ctx, target, month, month.AddDate(0, 1, 0), "monthly", source, observeMoney); errors.Is(e, billing.ErrStatementQueueFull) {
			return nil
		} else if errors.Is(e, billing.ErrDailyBillsIncomplete) || errors.Is(e, billing.ErrStatementNoData) {
			continue
		} else if e != nil {
			return e
		}
	}

	for _, day := range days {
		checked := billing.AutomaticTarget{InstanceID: target.InstanceID, Kind: target.Kind, SubjectID: target.SubjectID, From: day, To: day.AddDate(0, 0, 1)}
		if controller, ok := a.Store.(interface {
			BillingGenerationVersion(context.Context, billing.AutomaticTarget) (string, error)
		}); ok {
			if _, e := controller.BillingGenerationVersion(ctx, checked); errors.Is(e, billing.ErrGenerationCancelled) {
				continue
			} else if e != nil {
				return e
			}
		}
		active := true
		cache, cached := a.Store.(interface {
			BillingDayActivity(context.Context, billing.AutomaticTarget) (bool, bool, error)
			RecordBillingDayActivity(context.Context, billing.AutomaticTarget, bool) error
		})
		if cached {
			var known bool
			active, known, err = cache.BillingDayActivity(ctx, checked)
			if !known {
				active = true
			}
			if err != nil {
				return err
			}
		}
		// Unknown days are checked by the job's first detail page. Empty jobs
		// become no_data before file publication, without a second source query.

		if !active {
			continue
		}
		if err = a.enqueue(ctx, target, day, checked.To, "daily", source, observeMoney); errors.Is(err, billing.ErrStatementQueueFull) {
			return nil
		} else if err != nil {
			return err
		}
	}

	return nil
}
func (a BillingAutomation) enqueue(ctx context.Context, target billing.AutomaticTarget, day, end time.Time, period, source string, observeMoney func() (*billing.MoneySnapshot, error)) error {
	job, steps, err := billing.NewJob(target.InstanceID, day, end, "system:billing")
	if err != nil {
		return err
	}
	job.JobType = target.Kind
	job.BillPeriod = period
	job.DataSource = source
	job.UsageVersion = billing.SettlementUsageVersion
	job.PricingSource = billing.PricingSourceNewAPI
	name := ""
	if target.Kind == "user_statement" {
		job.UserID = target.SubjectID
	} else {
		up, e := a.Store.BillingStatementUpstream(ctx, target.InstanceID, target.SubjectID)
		if e != nil {
			return e
		}
		job.UpstreamID = target.SubjectID
		name = up.Name
	}
	version := ""
	if controller, ok := a.Store.(interface {
		BillingGenerationVersion(context.Context, billing.AutomaticTarget) (string, error)
	}); ok {
		check := target
		check.From = day
		check.To = end
		version, err = controller.BillingGenerationVersion(ctx, check)
		if errors.Is(err, billing.ErrGenerationCancelled) {
			return nil
		}
		if err != nil {
			return err
		}
	}
	hash := sha256.Sum256([]byte(fmt.Sprintf("%s-v3|%s|%s|%d|%s|%s", period, target.InstanceID, target.Kind, target.SubjectID, day.Format("2006-01-02"), source) + version))
	job.RequestKey = "statement:" + hex.EncodeToString(hash[:16])
	if period == "monthly" {
		err = a.Store.CreateBillingStatementJob(ctx, job, nil, name)
		if errors.Is(err, billing.ErrStatementDuplicate) || errors.Is(err, billing.ErrGenerationCancelled) {
			return nil
		}
		return err
	}
	// This is an advisory CT-only check. The insertion transaction still
	// enforces capacity, but a full queue must not trigger source options reads.
	if queue, ok := a.Store.(interface {
		BillingStatementQueueFull(context.Context, string) (bool, error)
	}); ok {
		full, e := queue.BillingStatementQueueFull(ctx, target.InstanceID)
		if e != nil {
			return e
		}
		if full {
			return billing.ErrStatementQueueFull
		}
	}
	if snapshots, ok := a.Store.(interface {
		FailedStatementMoneySnapshot(context.Context, string) (*billing.MoneySnapshot, error)
	}); ok {
		job.MoneySnapshot, err = snapshots.FailedStatementMoneySnapshot(ctx, job.RequestKey)
		if err != nil {
			return err
		}
	}
	if job.MoneySnapshot == nil {
		job.MoneySnapshot, err = observeMoney()
		if err != nil {
			return err
		}
	}
	err = a.Store.CreateBillingStatementJob(ctx, job, steps, name)
	if errors.Is(err, billing.ErrStatementQueueFull) {
		return err
	}
	if err != nil && !(errors.Is(err, billing.ErrStatementDuplicate) || errors.Is(err, billing.ErrGenerationCancelled)) {
		return err
	}
	return nil
}

func (a BillingAutomation) Run(ctx context.Context) {
	var wg sync.WaitGroup
	defer wg.Wait()
	var mu sync.Mutex
	active := map[string]bool{}
	run := func() {
		targets, err := a.Store.ListBillingAutomaticTargets(ctx)
		if err != nil {
			if ctx.Err() == nil {
				log.Printf("billing automation targets: %v", err)
			}
			return
		}
		groups := map[string][]billing.AutomaticTarget{}
		for _, t := range targets {
			groups[t.InstanceID] = append(groups[t.InstanceID], t)
		}
		for site, targets := range groups {
			mu.Lock()
			if active[site] {
				mu.Unlock()
				continue
			}
			active[site] = true
			mu.Unlock()
			wg.Add(1)
			go func(site string, targets []billing.AutomaticTarget) {
				defer wg.Done()
				defer func() { mu.Lock(); delete(active, site); mu.Unlock() }()
				for _, t := range targets {
					if ctx.Err() != nil {
						return
					}
					if err := a.Fill(ctx, t); err != nil {
						log.Printf("billing automation site=%s subject=%d: %v", site, t.SubjectID, err)
					}
				}
			}(site, targets)
		}
	}
	run()
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-a.Wake:
			run()
		case <-ticker.C:
			run()
		}
	}
}
func (a BillingAutomation) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if !billingAdminAllowed(r) {
		writeDashboardError(w, 403, "forbidden")
		return
	}
	if r.Method != http.MethodPost {
		writeDashboardError(w, 405, "method_not_allowed")
		return
	}
	var t billing.AutomaticTarget
	if json.NewDecoder(r.Body).Decode(&t) != nil || t.InstanceID == "" || t.SubjectID <= 0 || (t.Kind != "user_statement" && t.Kind != "upstream_statement") || (!t.From.IsZero() && !t.From.Before(billing.CompleteDayBoundary(time.Now()))) {
		writeDashboardError(w, 400, "invalid_generation_range")
		return
	}
	if !t.To.IsZero() {
		from := t.From.In(billing.BusinessLocation)
		to := t.To.In(billing.BusinessLocation)
		if t.From.IsZero() || from.Day() != 1 || !from.Equal(billing.CompleteDayBoundary(from)) || !to.Equal(from.AddDate(0, 1, 0)) {
			writeDashboardError(w, 400, "invalid_billing_month")
			return
		}
	}
	if !billingTypeAllowed(r, t.Kind) {
		writeDashboardError(w, 403, "forbidden")
		return
	}
	if t.From.IsZero() {
		source, e := a.Store.BillingDataSource(r.Context())
		if e != nil {
			writeDashboardError(w, 500, "billing_config_unavailable")
			return
		}
		if source == "archive" && a.Archive != nil {
			t.From, e = a.Archive.FirstBillingDay(r.Context(), t.InstanceID)
		} else if source == "source" {
			if first, ok := a.Source.(interface {
				FirstBillingDay(context.Context, billing.AutomaticTarget, []int64) (time.Time, error)
			}); ok {
				var channels []int64
				if t.Kind == "upstream_statement" {
					up, err := a.Store.BillingStatementUpstream(r.Context(), t.InstanceID, t.SubjectID)
					if err != nil {
						writeDashboardError(w, 400, "upstream_not_found")
						return
					}
					for _, c := range up.Channels {
						channels = append(channels, c.ChannelID)
					}
				}
				t.From, e = first.FirstBillingDay(r.Context(), t, channels)
			} else {
				e = fmt.Errorf("billing start unavailable")
			}
		} else {
			e = fmt.Errorf("archive unavailable")
		}
		if e != nil || t.From.IsZero() {
			writeDashboardError(w, 502, "billing_first_day_unavailable")
			return
		}
	}
	t.From = billing.CompleteDayBoundary(t.From)
	if t.Kind == "user_statement" || t.Kind == "upstream_statement" {
		if guard, ok := a.Store.(interface {
			BeginBillingGeneration(context.Context, string) (func(), error)
		}); ok {
			release, e := guard.BeginBillingGeneration(r.Context(), t.InstanceID)
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
	if err := a.Store.PutBillingAutomaticTarget(r.Context(), t); err != nil {
		writeDashboardError(w, 500, "billing_target_save_failed")
		return
	}
	if err := a.Fill(r.Context(), t); err != nil {
		writeDashboardError(w, 502, "billing_generation_failed")
		return
	}
	outcome := "registered"
	if feedback, ok := a.Store.(interface {
		BillingGenerationFeedback(context.Context, billing.AutomaticTarget) (string, error)
	}); ok {
		if result, e := feedback.BillingGenerationFeedback(r.Context(), t); e == nil {
			outcome = result
		}
	}
	writeDashboardJSON(w, 202, map[string]any{"accepted": true, "automatic": true, "outcome": outcome})
}

func (s BillingReadonlySource) FirstBillingDay(ctx context.Context, t billing.AutomaticTarget, channels []int64) (time.Time, error) {
	db, configured, err := s.Handler.database(t.InstanceID)
	if err != nil || !configured {
		return time.Time{}, fmt.Errorf("source unavailable")
	}
	query := `SELECT created_at FROM logs WHERE type=2`
	args := []any{}
	if t.Kind == "user_statement" {
		query += ` AND user_id=?`
		args = append(args, t.SubjectID)
	} else {
		if len(channels) == 0 {
			return time.Time{}, fmt.Errorf("no channels")
		}
		query += ` AND channel_id IN (` + strings.TrimSuffix(strings.Repeat("?,", len(channels)), ",") + `)`
		for _, id := range channels {
			args = append(args, id)
		}
	}
	query += ` ORDER BY created_at,id LIMIT 1`
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	var unix int64
	err = db.QueryRowContext(ctx, query, args...).Scan(&unix)
	return time.Unix(unix, 0), err
}

func (s BillingReadonlySource) ActiveBillingDays(ctx context.Context, t billing.AutomaticTarget, channels []int64, end time.Time) ([]time.Time, error) {
	if !t.From.Equal(billing.CompleteDayBoundary(t.From)) || !end.Equal(t.From.AddDate(0, 0, 1)) {
		return nil, fmt.Errorf("activity check requires one complete day")
	}
	if err := s.ValidateBillingIndexes(ctx, t.InstanceID, t.Kind == "user_statement"); err != nil {
		return nil, err
	}
	db, ok, err := s.Handler.database(t.InstanceID)
	if err != nil || !ok {
		return nil, fmt.Errorf("billing source unavailable")
	}
	release, err := sourceBillingBudget.acquire(ctx, t.InstanceID)
	if err != nil {
		return nil, err
	}
	defer release()
	q := `SELECT created_at FROM logs WHERE type=2 AND created_at>=? AND created_at<?`
	args := []any{t.From.Unix(), end.Unix()}
	if t.Kind == "user_statement" {
		q += ` AND user_id=?`
		args = append(args, t.SubjectID)
	} else {
		if len(channels) == 0 {
			return nil, fmt.Errorf("upstream has no channels")
		}
		q += ` AND channel_id IN (` + strings.TrimSuffix(strings.Repeat("?,", len(channels)), ",") + `)`
		for _, id := range channels {
			args = append(args, id)
		}
	}
	q += ` ORDER BY created_at,id LIMIT 1`
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	rows, err := db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	dates := []time.Time{}
	for rows.Next() {
		var bucket int64
		if err = rows.Scan(&bucket); err != nil {
			return nil, err
		}
		dates = append(dates, billing.CompleteDayBoundary(time.Unix(bucket, 0)))
	}
	return dates, rows.Err()
}

// BillingUserRole uses the primary-key user lookup even when order data comes
// from the archive. Role changes therefore take effect on the next auto run.
func (s BillingReadonlySource) BillingUserRole(ctx context.Context, site string, id int64) (int, error) {
	db, configured, err := s.Handler.database(site)
	if err != nil {
		return 0, err
	}
	if !configured {
		return 0, fmt.Errorf("billing user role source unavailable")
	}
	ctx, cancel := context.WithTimeout(ctx, readonlyQueryTimeout)
	defer cancel()
	var role int
	err = db.QueryRowContext(ctx, `SELECT role FROM users WHERE id=?`, id).Scan(&role)
	return role, err
}
