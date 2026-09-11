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
	snapshot, err := billing.ParseRatioSnapshot(raw)
	if err != nil {
		return siteCurrency{}, err
	}
	per, err := strconv.ParseFloat(snapshot.QuotaPerUnit, 64)
	if err != nil || per <= 0 || math.IsNaN(per) || math.IsInf(per, 0) {
		return siteCurrency{}, fmt.Errorf("invalid site quota unit")
	}
	if snapshot.Currency.Type == "TOKENS" {
		return siteCurrency{QuotaPerUnit: 1, PriceMultiplier: per, Type: "TOKENS"}, nil
	}
	rate, err := strconv.ParseFloat(snapshot.Currency.ExchangeRate, 64)
	if err != nil || rate <= 0 || math.IsNaN(rate) || math.IsInf(rate, 0) {
		return siteCurrency{}, fmt.Errorf("invalid site exchange rate")
	}
	return siteCurrency{QuotaPerUnit: per / rate, PriceMultiplier: rate, Symbol: snapshot.Currency.Symbol, Type: snapshot.Currency.Type}, nil
}

func (h *PassthroughHandler) Currency(w http.ResponseWriter, r *http.Request) {
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
