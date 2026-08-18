package dashboard

import (
	"context"
	"encoding/csv"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"

	ctauth "controltower/server/internal/auth"
	"controltower/server/internal/billing"
)

type BillingUpstreamStore interface {
	BillingChannelStore
	UpsertBillingUpstreamChannels(context.Context, []billing.UpstreamChannelMapping) error
	ListBillingUpstreamChannels(context.Context, string) ([]billing.UpstreamChannelMapping, error)
}
type BillingUpstreamSource interface {
	UpstreamChannelMappings(context.Context, string) ([]billing.UpstreamChannelMapping, error)
}
type BillingUpstreamHandler struct {
	Store  BillingUpstreamStore
	Source BillingUpstreamSource
}

type upstreamDetailItem struct {
	Day              string `json:"day"`
	ModelName        string `json:"model_name"`
	GroupName        string `json:"group_name"`
	TierFrom         int64  `json:"tier_from"`
	RequestCount     int64  `json:"request_count"`
	PromptTokens     int64  `json:"prompt_tokens"`
	CompletionTokens int64  `json:"completion_tokens"`
	CacheTokens      int64  `json:"cache_tokens"`
	CacheWriteTokens int64  `json:"cache_write_tokens"`
	Quota            int64  `json:"quota"`
	Amount           string `json:"amount"`
	Unpriced         bool   `json:"unpriced"`
}

func (h BillingUpstreamHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if u, ok := ctauth.CurrentUser(r); ok && u.Role != "admin" {
		writeDashboardError(w, 403, "forbidden")
		return
	}
	site := strings.TrimSpace(r.URL.Query().Get("instance_id"))
	if site == "" {
		writeDashboardError(w, 400, "instance_id_required")
		return
	}
	from, to, _, err := billingPeriodQuery(r)
	if err != nil {
		writeDashboardError(w, 400, "invalid_range")
		return
	}
	job, jobErr := billingJobForRead(r, h.Store, site, "channel_generate", from, to)
	if jobErr != nil || job.Status != "complete" {
		writeBillingReadConflict(w, jobErr)
		return
	}
	if h.Source != nil {
		if fresh, e := h.Source.UpstreamChannelMappings(r.Context(), site); e != nil {
			log.Printf("billing upstream mapping refresh failed site=%s: %v", site, e)
		} else if e = h.Store.UpsertBillingUpstreamChannels(r.Context(), fresh); e != nil {
			log.Printf("billing upstream mapping snapshot failed site=%s: %v", site, e)
		}
	}
	mappings, err := h.Store.ListBillingUpstreamChannels(r.Context(), site)
	if err != nil {
		writeDashboardError(w, 500, "billing_upstream_query_failed")
		return
	}
	rows, err := h.Store.QueryBillingChannelAggregatesForJob(r.Context(), job.ID, 0)
	if err != nil {
		writeDashboardError(w, 500, "billing_upstream_query_failed")
		return
	}
	prices, err := h.Store.ListBillingPrices(r.Context(), site)
	if err != nil {
		writeDashboardError(w, 500, "billing_upstream_query_failed")
		return
	}
	ratios, err := h.Store.ListBillingGroupRatios(r.Context(), site)
	if err != nil {
		writeDashboardError(w, 500, "billing_upstream_query_failed")
		return
	}
	snapshots, err := h.Store.BillingRatioSnapshots(r.Context(), site, from, to)
	if err != nil {
		writeDashboardError(w, 500, "billing_upstream_query_failed")
		return
	}
	groups := billing.BuildUpstreamGroups(rows, mappings)
	billing.ApplyUpstreamAmounts(groups, billing.BuildChannelSummary(rows, prices, ratios, snapshots, nil))
	if strings.HasSuffix(r.URL.Path, "/detail") {
		h.detail(w, r, from, to, groups, rows, prices, ratios, snapshots)
		return
	}
	writeDashboardJSON(w, 200, map[string]any{"items": groups, "job": job})
}

func (h BillingUpstreamHandler) detail(w http.ResponseWriter, r *http.Request, from, to time.Time, groups []billing.UpstreamGroup, rows []billing.AggregateRow, prices []billing.PriceRecord, ratios []billing.GroupRatio, snapshots map[string]string) {
	fp := r.URL.Query().Get("fp")
	var selected *billing.UpstreamGroup
	for i := range groups {
		if groups[i].UpstreamFP == fp {
			selected = &groups[i]
			break
		}
	}
	if selected == nil {
		writeDashboardError(w, 404, "upstream_group_not_found")
		return
	}
	ids := map[int64]bool{}
	for _, m := range selected.Members {
		ids[m.ChannelID] = true
	}
	filtered := []billing.AggregateRow{}
	for _, row := range rows {
		if ids[row.UserID] {
			filtered = append(filtered, row)
		}
	}
	merged := billing.MergeUpstreamDetails(filtered)
	priced := billing.BuildDetails(merged, prices, ratios, snapshots)
	details := make([]upstreamDetailItem, 0, len(priced))
	for _, v := range priced {
		details = append(details, upstreamDetailItem{Day: v.Day, ModelName: v.ModelName, GroupName: v.GroupName, TierFrom: v.TierFrom, RequestCount: v.RequestCount, PromptTokens: v.PromptTokens, CompletionTokens: v.CompletionTokens, CacheTokens: v.CacheTokens, CacheWriteTokens: v.CacheWriteTokens, Quota: v.Quota, Amount: v.Amount, Unpriced: v.Unpriced})
	}
	if r.URL.Query().Get("format") == "csv" {
		writeUpstreamCSV(w, billingDownloadName("billing-upstream", 0, 0, from, to)+".csv", from, to, *selected, details)
		return
	}
	writeDashboardJSON(w, 200, map[string]any{"group": selected, "details": details})
}

func writeUpstreamCSV(w http.ResponseWriter, filename string, from, to time.Time, group billing.UpstreamGroup, details []upstreamDetailItem) {
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="`+filename+`"`)
	_, _ = w.Write([]byte{0xef, 0xbb, 0xbf})
	cw := csv.NewWriter(w)
	memberNames := make([]string, 0, len(group.Members))
	for _, member := range group.Members {
		memberNames = append(memberNames, member.ChannelName+" (#"+strconv.FormatInt(member.ChannelID, 10)+")")
	}
	_ = cw.Write([]string{"上游 key", group.DisplayName})
	_ = cw.Write([]string{"Base URL", group.BaseURL})
	_ = cw.Write([]string{"成员渠道", strings.Join(memberNames, "、")})
	_ = cw.Write([]string{"账单区间", billing.FormatBusinessTime(from.Unix()), billing.FormatBusinessTime(to.Unix())})
	_ = cw.Write(nil)
	_ = cw.Write([]string{"日×模型明细"})
	_ = cw.Write([]string{"日期", "模型", "分组", "档位", "请求数", "普通输入Token", "缓存读取Token", "缓存写入Token", "输出Token", "金额", "Quota参考"})
	for _, v := range details {
		amount := v.Amount
		if v.Unpriced {
			amount = ""
		}
		_ = cw.Write([]string{v.Day, v.ModelName, v.GroupName, strconv.FormatInt(v.TierFrom, 10), strconv.FormatInt(v.RequestCount, 10), strconv.FormatInt(v.PromptTokens, 10), strconv.FormatInt(v.CacheTokens, 10), strconv.FormatInt(v.CacheWriteTokens, 10), strconv.FormatInt(v.CompletionTokens, 10), amount, strconv.FormatInt(v.Quota, 10)})
	}
	_ = cw.Write(nil)
	_ = cw.Write([]string{"成员渠道小计"})
	_ = cw.Write([]string{"渠道ID", "渠道名", "模型", "请求数", "普通输入Token", "缓存读取Token", "缓存写入Token", "输出Token", "金额", "Quota参考"})
	for _, m := range group.Members {
		v := m.Totals
		_ = cw.Write([]string{strconv.FormatInt(m.ChannelID, 10), m.ChannelName, m.ModelName, strconv.FormatInt(v.RequestCount, 10), strconv.FormatInt(v.PromptTokens, 10), strconv.FormatInt(v.CacheTokens, 10), strconv.FormatInt(v.CacheWriteTokens, 10), strconv.FormatInt(v.CompletionTokens, 10), v.Amount, strconv.FormatInt(v.Quota, 10)})
	}
	cw.Flush()
}
