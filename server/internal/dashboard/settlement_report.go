package dashboard

import (
	"context"
	"controltower/server/internal/billing"
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

func (h SettlementReportHandler) Calculate(ctx context.Context, site string, from, to time.Time, progress func(int64) error) (billing.ReportDocument, error) {
	sourceName, err := h.Store.BillingDataSource(ctx)
	if err != nil {
		return billing.ReportDocument{}, fmt.Errorf("billing_config_unavailable")
	}
	source := h.Source.ForBillingJob(billing.Job{DataSource: sourceName})
	if indexed, ok := source.(billing.BillingIndexSource); ok {
		if err = indexed.ValidateBillingIndexes(ctx, site, false); err != nil {
			return billing.ReportDocument{}, fmt.Errorf("report_source_index_unavailable: %w", err)
		}
	}
	money, err := captureBillingMoney(ctx, h.Source, site)
	if err != nil {
		return billing.ReportDocument{}, fmt.Errorf("billing_money_snapshot_unavailable")
	}
	rules, err := h.Store.ListBillingDiscountRules(ctx, site, "")
	if err != nil {
		return billing.ReportDocument{}, fmt.Errorf("discount_query_failed")
	}
	snapshots := []billing.StatementDiscount{}
	for _, v := range rules {
		snapshots = append(snapshots, billing.StatementDiscount{DiscountType: v.DiscountType, SubjectID: v.SubjectID, ChannelID: v.ChannelID, ModelName: v.ModelName, Discount: v.Discount, EffectiveFrom: v.EffectiveFrom, EffectiveTo: v.EffectiveTo, SourceRuleID: v.ID})
	}
	ups, err := h.Store.ListBillingUpstreams(ctx, site)
	if err != nil {
		return billing.ReportDocument{}, fmt.Errorf("upstream_query_failed")
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
	add := func(to *big.Rat, raw string) {
		if v, ok := new(big.Rat).SetString(raw); ok {
			to.Add(to, v)
		}
	}
	var processed int64
	for day := from; day.Before(to); {
		end := billing.CompleteDayBoundary(day).AddDate(0, 0, 1)
		if end.After(to) {
			end = to
		}
		cursor := billing.LogCursor{}
		for {
			logs, e := billing.ReadPageWithRetry(ctx, "report "+site, cursor, func() ([]billing.PagedLogRecord, error) {
				return source.LogsPage(ctx, site, day, end, cursor, billing.BillingPageSize)
			})
			if e != nil {
				return billing.ReportDocument{}, fmt.Errorf("report_source_read_failed")
			}
			if len(logs) == 0 {
				break
			}
			for _, v := range logs {
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
	} else if archive, ok := h.Source.Archive.(interface {
		BillingFailedRequests(context.Context, string, time.Time, time.Time) (int64, error)
	}); ok {
		if failed, err := archive.BillingFailedRequests(ctx, site, from, to); err == nil {
			failures = &failed
		}
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
