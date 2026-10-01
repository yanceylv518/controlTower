package dashboard

import (
	"context"
	"controltower/server/internal/billing"
	"errors"
	"fmt"
	"math/big"
	"time"
)

type SettlementReportStore interface {
	BillingDiscountStore
	BillingUpstreamConfigStore
	BillingSourceConfigStore
}
type SettlementReportHandler struct {
	Store  SettlementReportStore
	Source BillingSources
}

var errReportArchiveChanged = errors.New("report_archive_version_changed")

func (h SettlementReportHandler) newReportCheckpoint(ctx context.Context, site string, from, to time.Time) (*billing.ReportCheckpoint, error) {
	sourceName, err := h.Store.BillingDataSource(ctx)
	if err != nil {
		return nil, fmt.Errorf("billing_config_unavailable")
	}
	source := h.Source.ForBillingJob(billing.Job{DataSource: sourceName})
	if indexed, ok := source.(billing.BillingIndexSource); ok {
		if err = indexed.ValidateBillingIndexes(ctx, site, false); err != nil {
			return nil, fmt.Errorf("report_source_index_unavailable: %w", err)
		}
	}
	money, err := captureBillingMoney(ctx, h.Source, site)
	if err != nil {
		return nil, fmt.Errorf("billing_money_snapshot_unavailable")
	}
	rules, err := h.Store.ListBillingDiscountRules(ctx, site, "")
	if err != nil {
		return nil, fmt.Errorf("discount_query_failed")
	}
	snapshots := []billing.StatementDiscount{}
	for _, v := range rules {
		snapshots = append(snapshots, billing.StatementDiscount{DiscountType: v.DiscountType, SubjectID: v.SubjectID, ChannelID: v.ChannelID, ModelName: v.ModelName, Discount: v.Discount, EffectiveFrom: v.EffectiveFrom, EffectiveTo: v.EffectiveTo, SourceRuleID: v.ID})
	}
	ups, err := h.Store.ListBillingUpstreams(ctx, site)
	if err != nil {
		return nil, fmt.Errorf("upstream_query_failed")
	}
	return &billing.ReportCheckpoint{Version: 1, Site: site, From: from, To: to, DataSource: sourceName, Money: money, Discounts: snapshots, Upstreams: ups, Groups: map[string]billing.ReportAccumulator{}}, nil
}

func (h SettlementReportHandler) CalculateTask(ctx context.Context, task billing.ReportTask, day billing.ReportTaskDay, progress func(int64) error) (billing.ReportDocument, error) {
	store, ok := h.Store.(billing.ReportCheckpointStore)
	if !ok {
		return billing.ReportDocument{}, fmt.Errorf("report_checkpoint_unavailable")
	}
	from, err := time.ParseInLocation("2006-01-02", day.Day, billing.BusinessLocation)
	if err != nil {
		return billing.ReportDocument{}, err
	}
	cp, err := store.LoadReportCheckpoint(ctx, task.ID, day.Day)
	if err != nil {
		return billing.ReportDocument{}, fmt.Errorf("report_checkpoint_load_failed: %w", err)
	}
	return h.calculate(ctx, task.Site, from, from.AddDate(0, 0, 1), cp, func(cp *billing.ReportCheckpoint) error {
		return store.SaveReportCheckpoint(ctx, task.ID, day.Day, cp)
	}, progress)
}

func (h SettlementReportHandler) Calculate(ctx context.Context, site string, from, to time.Time, progress func(int64) error) (billing.ReportDocument, error) {
	return h.calculate(ctx, site, from, to, nil, nil, progress)
}

func (h SettlementReportHandler) calculate(ctx context.Context, site string, from, to time.Time, cp *billing.ReportCheckpoint, save func(*billing.ReportCheckpoint) error, progress func(int64) error) (billing.ReportDocument, error) {
	var err error
	if cp == nil {
		cp, err = h.newReportCheckpoint(ctx, site, from, to)
		if err != nil {
			return billing.ReportDocument{}, err
		}
		if save != nil {
			if err = save(cp); err != nil {
				return billing.ReportDocument{}, fmt.Errorf("report_checkpoint_save_failed: %w", err)
			}
		}
	}
	if cp.Version != 1 || cp.Site != site || !cp.From.Equal(from) || !cp.To.Equal(to) || cp.Money == nil || cp.Processed < 0 {
		return billing.ReportDocument{}, fmt.Errorf("report_checkpoint_invalid")
	}
	sourceName, money, snapshots, ups := cp.DataSource, cp.Money, cp.Discounts, cp.Upstreams
	source := h.Source.ForBillingJob(billing.Job{DataSource: sourceName})
	checkArchiveVersion := func(identity, version string) error {
		if identity == "" || version == "" {
			return billing.PermanentPageError{Err: errReportArchiveChanged}
		}
		if cp.ArchiveSource == "" && cp.ArchiveVersion == "" && cp.Processed == 0 {
			cp.ArchiveSource, cp.ArchiveVersion = identity, version
			if save != nil {
				if err := save(cp); err != nil {
					return billing.PermanentPageError{Err: fmt.Errorf("report_checkpoint_save_failed: %w", err)}
				}
			}
		} else if cp.ArchiveSource != identity || cp.ArchiveVersion != version {
			return billing.PermanentPageError{Err: errReportArchiveChanged}
		}
		return nil
	}
	if sourceName == "archive" {
		source = archiveBillingSource{reader: h.Source.Archive, versionCheck: checkArchiveVersion}
	}
	if indexed, ok := source.(billing.BillingIndexSource); ok && !cp.ScanComplete {
		if err = indexed.ValidateBillingIndexes(ctx, site, false); err != nil {
			return billing.ReportDocument{}, fmt.Errorf("report_source_index_unavailable: %w", err)
		}
	}
	type owner struct {
		up      billing.Upstream
		channel billing.UpstreamChannel
	}
	owners := map[int64]owner{}
	for _, up := range ups {
		for _, c := range up.Channels {
			owners[c.ChannelID] = owner{up, c}
		}
	}
	type row struct {
		billing.ReportRow
		unknownOriginal   bool
		amount, cost, raw *big.Rat
	}

	groups := map[string]*row{}
	for key, saved := range cp.Groups {
		amount, a := new(big.Rat).SetString(saved.Amount)
		cost, b := new(big.Rat).SetString(saved.Cost)
		raw, c := new(big.Rat).SetString(saved.Raw)
		if !a || !b || !c {
			return billing.ReportDocument{}, fmt.Errorf("report_checkpoint_invalid")
		}
		groups[key] = &row{ReportRow: saved.Row, unknownOriginal: saved.UnknownOriginal, amount: amount, cost: cost, raw: raw}
	}
	checkpoint := func() error {
		if save == nil {
			return nil
		}
		cp.Groups = make(map[string]billing.ReportAccumulator, len(groups))
		for key, g := range groups {
			cp.Groups[key] = billing.ReportAccumulator{Row: g.ReportRow, Amount: g.amount.RatString(), Cost: g.cost.RatString(), Raw: g.raw.RatString(), UnknownOriginal: g.unknownOriginal}
		}
		if e := save(cp); e != nil {
			return fmt.Errorf("report_checkpoint_save_failed: %w", e)
		}
		return nil
	}
	add := func(to *big.Rat, raw string) {
		if v, ok := new(big.Rat).SetString(raw); ok {
			to.Add(to, v)
		}
	}
	processed := cp.Processed
	lastSaved, lastSaveTime := processed, time.Now()
	for day := from; day.Before(to) && !cp.ScanComplete; {
		end := billing.CompleteDayBoundary(day).AddDate(0, 0, 1)
		if end.After(to) {
			end = to
		}
		cursor := billing.LogCursor{}
		if day.Equal(from) {
			cursor = cp.Cursor
		}
		for {
			logs, e := billing.ReadPageWithRetry(ctx, "report "+site, cursor, func() ([]billing.PagedLogRecord, error) {
				return source.LogsPage(ctx, site, day, end, cursor, billing.BillingPageSize)
			})
			if e != nil {
				return billing.ReportDocument{}, fmt.Errorf("report_source_read_failed: %w", e)
			}
			if len(logs) == 0 {
				break
			}
			for _, v := range logs {
				if err = ctx.Err(); err != nil {
					return billing.ReportDocument{}, err
				}
				income, e := billing.ResolveSettlement(v, "user_statement", v.UserID, snapshots, money.QuotaPerUnit)
				if e != nil {
					return billing.ReportDocument{}, fmt.Errorf("report_user_price_invalid")
				}
				o, known := owners[v.ChannelID]
				if known && len(o.channel.Models) > 0 {
					known = false
					for _, m := range o.channel.Models {
						if m == v.ModelName {
							known = true
							break
						}
					}
				}
				var cost billing.Settlement
				var costErr error
				if known {
					cost, costErr = billing.ResolveSettlement(v, "upstream_statement", o.up.ID, snapshots, money.QuotaPerUnit)
				}
				key := fmt.Sprintf("%d|%d|%s|%s|%s", v.UserID, v.ChannelID, v.ModelName, income.Discount, cost.Discount)
				g := groups[key]
				if g == nil {
					g = &row{ReportRow: billing.ReportRow{UserID: v.UserID, User: v.Username, Model: v.ModelName, ChannelID: v.ChannelID, Channel: v.ChannelName, Discount: income.Discount}, amount: new(big.Rat), cost: new(big.Rat), raw: new(big.Rat)}
					groups[key] = g
				}
				g.Requests++
				g.Input += v.PromptTokens.Int64
				g.Output += v.CompletionTokens.Int64
				g.Cache += v.CacheTokens + v.CacheWriteTokens
				if v.CompletionTokens.Valid && v.CompletionTokens.Int64 == 0 {
					g.Empty++
				}
				add(g.amount, income.Amount)
				base, fallback, baseErr := billing.OriginalBillingBase(v, money.QuotaPerUnit)
				if baseErr != nil {
					g.unknownOriginal = true
				} else {
					add(g.raw, base)
				}
				if fallback {
					g.BaseFallback++
				}
				if known {
					g.Upstream = o.up.Name
					if g.Channel == "" {
						g.Channel = o.channel.ChannelName
					}
					if costErr == nil {
						add(g.cost, cost.Amount)
						if g.Requests == 1 {
							g.CostDiscount = cost.Discount
						} else {
							g.CostDiscount = billing.MergeDiscount(g.CostDiscount, cost.Discount)
						}
					} else {
						g.UnknownCost++
						g.CostDiscount = ""
					}
				} else {
					g.UnknownCost++
					g.CostDiscount = ""
				}
			}
			last := logs[len(logs)-1]
			next := billing.LogCursor{CreatedUnix: last.CreatedUnix, ID: last.ID}
			if next.CreatedUnix < cursor.CreatedUnix || (next.CreatedUnix == cursor.CreatedUnix && next.ID <= cursor.ID) {
				return billing.ReportDocument{}, fmt.Errorf("report_cursor_stalled")
			}
			cursor = next
			processed += int64(len(logs))
			cp.Cursor, cp.Processed = cursor, processed
			// Checkpoint only whole pages after 20,000 records or 15 seconds.
			// Source and archive page sizes differ; never persist half a page.
			if save != nil && (processed-lastSaved >= 20000 || time.Since(lastSaveTime) >= 15*time.Second) {
				if e = checkpoint(); e != nil {
					return billing.ReportDocument{}, e
				}
				lastSaved, lastSaveTime = processed, time.Now()
			}
			if progress != nil {
				if e = progress(processed); e != nil {
					return billing.ReportDocument{}, e
				}
			}
			if len(logs) < billing.BillingPageSize && sourceName != "archive" {
				break
			}
			pause := time.NewTimer(500 * time.Millisecond)
			select {
			case <-ctx.Done():
				pause.Stop()
				return billing.ReportDocument{}, ctx.Err()
			case <-pause.C:
			}
		}
		day = end
	}
	if err = ctx.Err(); err != nil {
		return billing.ReportDocument{}, err
	}
	if !cp.ScanComplete {
		cp.ScanComplete = true
		if err = checkpoint(); err != nil {
			return billing.ReportDocument{}, err
		}
	}
	display, rate, e := billing.SettlementDisplay(money)
	if e != nil {
		return billing.ReportDocument{}, fmt.Errorf("billing_currency_unavailable")
	}
	out := []billing.ReportRow{}
	for _, g := range groups {
		g.Amount = billing.DisplaySettlementAmount(g.amount.FloatString(12), rate)
		g.Cost = billing.DisplaySettlementAmount(g.cost.FloatString(12), rate)
		if !g.unknownOriginal {
			g.RawAmount = billing.DisplaySettlementAmount(g.raw.FloatString(12), rate)
		}
		// Keep the old field as an alias for clients reading the shared base.
		g.RawCost = g.RawAmount
		out = append(out, g.ReportRow)
	}
	var failures *int64
	if sourceName == "source" {
		if failed, err := h.Source.Handler.BillingFailedRequestCount(ctx, site, from, to); err == nil {
			failures = &failed
		}
	} else {
		archive, ok := h.Source.Archive.(interface {
			BillingFailedRequestsChecked(context.Context, string, time.Time, time.Time, func(string, string) error) (int64, error)
		})
		if !ok {
			return billing.ReportDocument{}, fmt.Errorf("report_archive_version_unavailable")
		}
		// Completed checkpoints skip all pages on resume. Confirm their frozen
		// archive identity even when the optional failed-request count is absent.
		verified := false
		failed, err := archive.BillingFailedRequestsChecked(ctx, site, from, to, func(identity, version string) error {
			if err := checkArchiveVersion(identity, version); err != nil {
				return err
			}
			verified = true
			return nil
		})
		if errors.Is(err, errReportArchiveChanged) {
			return billing.ReportDocument{}, err
		}
		if !verified {
			if err != nil {
				return billing.ReportDocument{}, fmt.Errorf("report_archive_version_unavailable: %w", err)
			}
			return billing.ReportDocument{}, fmt.Errorf("report_archive_version_unavailable")
		}
		if err == nil {
			failures = &failed
		}
	}
	if err = ctx.Err(); err != nil {
		return billing.ReportDocument{}, err
	}
	return billing.ReportDocument{Items: out, FailedRequests: failures, DataSource: sourceName, From: from, To: to, MoneySnapshot: money, Currency: display, GeneratedAt: time.Now().UTC(), Discounts: snapshots, Upstreams: ups}, nil
}
func (h *PassthroughHandler) BillingFailedRequestCount(ctx context.Context, site string, from, to time.Time) (int64, error) {
	db, configured, err := h.database(site)
	if err != nil || !configured {
		return 0, fmt.Errorf("source unavailable")
	}
	release, err := sourceBillingBudget.acquire(ctx, site)
	if err != nil {
		return 0, err
	}
	defer release()
	queryCtx, cancel := context.WithTimeout(ctx, readonlyQueryTimeout)
	defer cancel()
	var n int64
	err = db.QueryRowContext(queryCtx, `SELECT COUNT(*) FROM logs WHERE type=5 AND created_at>=? AND created_at<?`, from.Unix(), to.Unix()).Scan(&n)
	return n, err
}
