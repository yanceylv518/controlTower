package dashboard

import (
	"context"
	"fmt"
	"math"
	"net/http"
	"strconv"
	"time"

	"controltower/server/internal/billing"
)

type siteCurrencySource interface {
	RatioSnapshotForBilling(context.Context, string) (string, error)
}

type siteCurrency struct {
	SiteID          string  `json:"site_id"`
	RawQuotaPerUnit string  `json:"raw_quota_per_unit"`
	ExchangeRate    string  `json:"exchange_rate"`
	ObservedAt      string  `json:"observed_at"`
	PriceMultiplier float64 `json:"price_multiplier"`
	QuotaPerUnit    float64 `json:"quota_per_unit"`
	Symbol          string  `json:"symbol"`
	Type            string  `json:"type"`
}

func readSiteCurrency(ctx context.Context, source siteCurrencySource, site string) (siteCurrency, error) {
	raw, err := source.RatioSnapshotForBilling(ctx, site)
	if err != nil {
		return siteCurrency{}, err
	}
	snapshot, err := billing.NewMoneySnapshot(site, raw, time.Now())
	if err != nil {
		return siteCurrency{}, err
	}
	per, err := strconv.ParseFloat(snapshot.QuotaPerUnit, 64)
	if err != nil || per <= 0 || math.IsNaN(per) || math.IsInf(per, 0) {
		return siteCurrency{}, fmt.Errorf("invalid site quota unit")
	}
	value := siteCurrency{SiteID: site, RawQuotaPerUnit: snapshot.QuotaPerUnit, ExchangeRate: snapshot.Display.ExchangeRate, ObservedAt: snapshot.ObservedAt.Format(time.RFC3339Nano), Type: snapshot.Display.Type, Symbol: snapshot.Display.Symbol}
	if snapshot.Display.Type == "TOKENS" {
		value.QuotaPerUnit, value.PriceMultiplier = 1, per
		return value, nil
	}
	rate, err := strconv.ParseFloat(snapshot.Display.ExchangeRate, 64)
	if err != nil || rate <= 0 || math.IsNaN(rate) || math.IsInf(rate, 0) {
		return siteCurrency{}, fmt.Errorf("invalid site exchange rate")
	}
	value.QuotaPerUnit, value.PriceMultiplier = per/rate, rate
	return value, nil
}

func (h *PassthroughHandler) Currency(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	site, _, err := passthroughScope(r)
	if err != nil {
		writeDashboardError(w, 400, "site_required")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	value, err := readSiteCurrency(ctx, h, site)
	if err != nil {
		writeDashboardError(w, 503, "site_currency_unavailable")
		return
	}
	writeDashboardJSON(w, 200, value)
}
