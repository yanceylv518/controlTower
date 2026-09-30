package dashboard

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// Current discounts are only form defaults. They carry no effective date and
// must never be used to overwrite the immutable discount on a historical log.
func (h *PassthroughHandler) BillingCurrentDiscounts(w http.ResponseWriter, r *http.Request) {
	if !billingAdminAllowed(r) {
		writeDashboardError(w, 403, "forbidden")
		return
	}
	site := strings.TrimSpace(r.URL.Query().Get("instance_id"))
	userID, err := strconv.ParseInt(r.URL.Query().Get("user_id"), 10, 64)
	if site == "" || err != nil || userID <= 0 {
		writeDashboardError(w, 400, "invalid_user_id")
		return
	}
	db, configured, err := h.database(site)
	if err != nil || !configured {
		writeDashboardError(w, 502, "readonly_source_unavailable")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	rows, err := db.QueryContext(ctx, `SELECT model_name,discount_bps FROM user_model_pricings WHERE user_id=? ORDER BY model_name`, userID)
	if err != nil {
		writeDashboardError(w, 502, "newapi_discount_query_failed")
		return
	}
	defer rows.Close()
	items := []map[string]string{}
	for rows.Next() {
		var model string
		var bps int64
		if err = rows.Scan(&model, &bps); err != nil || bps < 0 || bps > 10000 {
			writeDashboardError(w, 502, "newapi_discount_invalid")
			return
		}
		items = append(items, map[string]string{"model_name": model, "discount": fmt.Sprintf("%d.%04d", bps/10000, bps%10000)})
	}
	if rows.Err() != nil {
		writeDashboardError(w, 502, "newapi_discount_query_failed")
		return
	}
	writeDashboardJSON(w, 200, map[string]any{"items": items})
}

// HasCurrentModelDiscount validates future/open-ended supplements against site configuration.
func (h *PassthroughHandler) HasCurrentModelDiscount(ctx context.Context, site string, userID int64, model string) (bool, error) {
	db, configured, err := h.database(site)
	if err != nil {
		return false, err
	}
	if !configured {
		return false, fmt.Errorf("readonly source unavailable")
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	var bps int64
	err = db.QueryRowContext(ctx, `SELECT discount_bps FROM user_model_pricings WHERE user_id=? AND BINARY model_name=BINARY ? LIMIT 1`, userID, model).Scan(&bps)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if bps < 0 || bps > 10000 {
		return false, fmt.Errorf("invalid discount")
	}
	return bps != 10000, nil
}
