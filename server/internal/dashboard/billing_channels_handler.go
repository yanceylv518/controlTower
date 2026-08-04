package dashboard

import (
	"context"
	ctauth "controltower/server/internal/auth"
	"controltower/server/internal/billing"
	"encoding/json"
	"math/big"
	"net/http"
	"strconv"
	"strings"
	"time"
)

type BillingChannelStore interface {
	QueryBillingChannelAggregates(context.Context, string, time.Time, time.Time, int64) ([]billing.AggregateRow, error)
	ListBillingChannelSettings(context.Context, string) (map[int64]billing.ChannelSetting, error)
	PutBillingChannelSetting(context.Context, billing.ChannelSetting) error
	ListBillingPrices(context.Context, string) ([]billing.PriceRecord, error)
	ListBillingGroupRatios(context.Context, string) ([]billing.GroupRatio, error)
	BillingRatioSnapshots(context.Context, string, time.Time, time.Time) (map[string]string, error)
}
type BillingChannelsHandler struct{ Store BillingChannelStore }

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
	month := r.URL.Query().Get("month")
	from, e := time.ParseInLocation("2006-01", month, time.Local)
	if e != nil {
		writeDashboardError(w, 400, "invalid_month")
		return
	}
	channelID, _ := strconv.ParseInt(r.URL.Query().Get("channel_id"), 10, 64)
	to := from.AddDate(0, 1, 0)
	rows, e := h.Store.QueryBillingChannelAggregates(r.Context(), site, from, to, channelID)
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
	items := billing.BuildChannelSummary(rows, prices, ratios, snapshots, settings)
	writeDashboardJSON(w, 200, map[string]any{"items": items, "month": month})
}
func validDiscount(raw string) bool {
	v, ok := new(big.Rat).SetString(raw)
	return ok && v.Sign() >= 0 && v.Cmp(big.NewRat(1, 1)) <= 0
}
