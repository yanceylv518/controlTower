package dashboard

import (
	"context"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// BillingCurrentDiscounts 兼容旧版只读查询，不参与补录保存或历史订单折扣计算。
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
