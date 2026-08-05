package dashboard

import (
	"context"
	ctauth "controltower/server/internal/auth"
	"controltower/server/internal/billing"
	"database/sql"
	"encoding/json"
	"math/big"
	"net/http"
	"strconv"
	"strings"
	"time"
)

type BillingChannelStore interface {
	QueryBillingChannelAggregates(context.Context, string, time.Time, time.Time, int64) ([]billing.AggregateRow, error)
	QueryBillingChannelAggregatesForJob(context.Context, string, int64) ([]billing.AggregateRow, error)
	ListBillingChannelSettings(context.Context, string) (map[int64]billing.ChannelSetting, error)
	PutBillingChannelSetting(context.Context, billing.ChannelSetting) error
	ListBillingPrices(context.Context, string) ([]billing.PriceRecord, error)
	ListBillingGroupRatios(context.Context, string) ([]billing.GroupRatio, error)
	BillingRatioSnapshots(context.Context, string, time.Time, time.Time) (map[string]string, error)
	LatestBillingJob(context.Context, string, string, time.Time, time.Time) (billing.Job, error)
}
type BillingChannelSource interface {
	CurrentChannels(context.Context, string) ([]billing.ConfiguredChannel, error)
}
type BillingChannelsHandler struct {
	Store  BillingChannelStore
	Source BillingChannelSource
}

func (h BillingChannelsHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodPut {
		if u, ok := ctauth.CurrentUser(r); ok && u.Role != "admin" {
			writeDashboardError(w, 403, "forbidden")
			return
		}
		var v billing.ChannelSetting
		if json.NewDecoder(r.Body).Decode(&v) != nil || v.InstanceID == "" || v.ChannelID <= 0 || !validDiscount(v.Discount) {
			writeDashboardError(w, 400, "invalid_request")
			return
		}
		v.UpdatedAt = time.Now().UTC()
		v.UpdatedBy = ctauth.Actor(r)
		if e := h.Store.PutBillingChannelSetting(r.Context(), v); e != nil {
			writeDashboardError(w, 500, "billing_channel_setting_failed")
			return
		}
		writeDashboardJSON(w, 200, v)
		return
	}
	if r.Method != http.MethodGet {
		writeDashboardError(w, 405, "method_not_allowed")
		return
	}
	site := strings.TrimSpace(r.URL.Query().Get("instance_id"))
	if !billingSiteAllowed(r, site, 0) {
		writeDashboardError(w, 403, "forbidden")
		return
	}
	from, to, period, e := billingPeriodQuery(r)
	if e != nil {
		writeDashboardError(w, 400, "invalid_range")
		return
	}
	channelID, _ := strconv.ParseInt(r.URL.Query().Get("channel_id"), 10, 64)
	job, jobErr := h.Store.LatestBillingJob(r.Context(), site, "channel_generate", from, to)
	var rows []billing.AggregateRow
	if jobErr == nil && job.Status == "complete" {
		rows, e = h.Store.QueryBillingChannelAggregatesForJob(r.Context(), job.ID, channelID)
	}
	if e != nil {
		writeDashboardError(w, 500, "billing_channel_query_failed")
		return
	}
	prices, e := h.Store.ListBillingPrices(r.Context(), site)
	if e != nil {
		writeDashboardError(w, 500, "billing_channel_query_failed")
		return
	}
	ratios, e := h.Store.ListBillingGroupRatios(r.Context(), site)
	if e != nil {
		writeDashboardError(w, 500, "billing_channel_query_failed")
		return
	}
	snapshots, e := h.Store.BillingRatioSnapshots(r.Context(), site, from, to)
	if e != nil {
		writeDashboardError(w, 500, "billing_channel_query_failed")
		return
	}
	settings, e := h.Store.ListBillingChannelSettings(r.Context(), site)
	if e != nil {
		writeDashboardError(w, 500, "billing_channel_query_failed")
		return
	}
	billed := billing.BuildChannelSummary(rows, prices, ratios, snapshots, settings)
	items := billed
	warning := ""
	if h.Source != nil {
		configured, sourceErr := h.Source.CurrentChannels(r.Context(), site)
		if sourceErr != nil {
			warning = sourceErr.Error()
		} else {
			byID := make(map[int64]billing.ChannelSummary, len(billed))
			for _, item := range billed {
				byID[item.ChannelID] = item
			}
			items = make([]billing.ChannelSummary, 0, len(configured))
			for _, channel := range configured {
				if channelID > 0 && channel.ChannelID != channelID {
					continue
				}
				item, ok := byID[channel.ChannelID]
				if !ok {
					discount := "1"
					if setting, exists := settings[channel.ChannelID]; exists && setting.Discount != "" {
						discount = setting.Discount
					}
					item = billing.ChannelSummary{ChannelID: channel.ChannelID, Discount: discount, DiscountedAmount: "0.000000", Amount: "0.000000", UnpricedModels: []string{}}
				}
				item.ChannelName = channel.ChannelName
				items = append(items, item)
			}
		}
	}
	if jobErr != nil && jobErr != sql.ErrNoRows {
		writeDashboardError(w, 500, "billing_channel_query_failed")
		return
	}
	var generationJob any
	if jobErr == nil {
		generationJob = job
	}
	writeDashboardJSON(w, 200, map[string]any{"items": items, "period": period, "generation_job": generationJob, "warning": warning})
}

func billingPeriodQuery(r *http.Request) (time.Time, time.Time, string, error) {
	q := r.URL.Query()
	if q.Get("from") != "" || q.Get("to") != "" {
		from, to, err := parseBillingInputRange(q.Get("from"), q.Get("to"))
		if err != nil {
			return time.Time{}, time.Time{}, "", err
		}
		return from, to, q.Get("from") + "_" + q.Get("to"), nil
	}
	month := q.Get("month")
	from, err := time.ParseInLocation("2006-01", month, billing.BusinessLocation)
	if err != nil {
		return time.Time{}, time.Time{}, "", err
	}
	return from, from.AddDate(0, 1, 0), month, nil
}
func validDiscount(raw string) bool {
	v, ok := new(big.Rat).SetString(raw)
	return ok && v.Sign() >= 0 && v.Cmp(big.NewRat(1, 1)) <= 0
}
