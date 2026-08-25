package dashboard

import (
	"context"
	"encoding/csv"
	"math/big"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	ctauth "controltower/server/internal/auth"
	"controltower/server/internal/billing"
)

type BillingUpstreamStore interface {
	BillingChannelStore
	ListBillingUpstreams(context.Context, string) ([]billing.Upstream, error)
	QueryBillingChannelAnomalies(context.Context, string, time.Time, time.Time) ([]billing.ChannelAnomalyRow, error)
	QueryBillingChannelRequestDetails(context.Context, string, int64, time.Time, time.Time) ([]billing.RequestDetail, error)
	QueryBillingChannelAnomalyOrders(context.Context, string, int64, time.Time, time.Time) ([]billing.AnomalyOrder, error)
	ActiveBillingChannelDailyFile(context.Context, string, time.Time, int64) (billing.ChannelDailyFile, error)
}

type channelRequestItem struct {
	CreatedAt        string `json:"created_at"`
	RequestID        string `json:"request_id"`
	Username         string `json:"username"`
	TokenName        string `json:"token_name"`
	ModelName        string `json:"model_name"`
	PromptTokens     int64  `json:"prompt_tokens"`
	CompletionTokens int64  `json:"completion_tokens"`
	CacheReadTokens  int64  `json:"cache_read_tokens"`
	CacheWriteTokens int64  `json:"cache_write_tokens"`
	InputPrice       string `json:"input_price"`
	OutputPrice      string `json:"output_price"`
	CacheReadPrice   string `json:"cache_read_price"`
	CacheWritePrice  string `json:"cache_write_price"`
	Amount           string `json:"amount"`
	Abnormal         bool   `json:"abnormal"`
	Reasons          string `json:"reasons"`
}
type BillingUpstreamCurrentChannelSource interface {
	CurrentChannels(context.Context, string) ([]billing.ConfiguredChannel, error)
}
type BillingUpstreamHandler struct {
	Store  BillingUpstreamStore
	Source BillingUpstreamCurrentChannelSource
}

func addAmountStrings(left, right string) string {
	total := new(big.Rat)
	if value, ok := new(big.Rat).SetString(left); ok {
		total.Add(total, value)
	}
	if value, ok := new(big.Rat).SetString(right); ok {
		total.Add(total, value)
	}
	return billing.FormatAmount(total, 6)
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
	AbnormalRows     int64  `json:"abnormal_rows"`
	AbnormalAmount   string `json:"abnormal_amount"`
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
	if !billingReadonlyAvailable(h.Store, site) {
		writeDashboardError(w, http.StatusConflict, "readonly_source_unavailable")
		return
	}
	upstreams, err := h.Store.ListBillingUpstreams(r.Context(), site)
	if err != nil {
		writeDashboardError(w, 500, "billing_upstream_query_failed")
		return
	}
	mappings := make([]billing.UpstreamChannelMapping, 0)
	for _, upstream := range upstreams {
		if !upstream.Enabled {
			continue
		}
		for _, channel := range upstream.Channels {
			mappings = append(mappings, billing.UpstreamChannelMapping{InstanceID: site, ChannelID: channel.ChannelID, ChannelName: channel.ChannelName, UpstreamFP: strconv.FormatInt(upstream.ID, 10), UpstreamName: upstream.Name})
		}
	}
	rows, err := h.Store.QueryBillingChannelAggregates(r.Context(), site, from, to, 0)
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
	anomalies, err := h.Store.QueryBillingChannelAnomalies(r.Context(), site, from, to)
	if err != nil {
		writeDashboardError(w, 500, "billing_upstream_query_failed")
		return
	}
	groups = billing.ApplyUpstreamAnomalies(groups, mappings, anomalies)
	if strings.HasSuffix(r.URL.Path, "/requests") {
		h.requests(w, r, site, from, to, groups)
		return
	}
	if strings.HasSuffix(r.URL.Path, "/detail") {
		h.detail(w, r, from, to, groups, rows, prices, ratios, snapshots, anomalies)
		return
	}
	currentChannelIDs := map[int64]bool{}
	currentChannelsLoaded := false
	if h.Source != nil {
		if currentChannels, sourceErr := h.Source.CurrentChannels(r.Context(), site); sourceErr == nil {
			currentChannelsLoaded = true
			for _, channel := range currentChannels {
				currentChannelIDs[channel.ChannelID] = true
			}
		}
	}
	unmappedChannels := 0
	missingCurrentIDs, historicalIDs := []int64{}, []int64{}
	for _, group := range groups {
		if group.UpstreamFP == "" {
			unmappedChannels = group.MemberCount
			for _, member := range group.Members {
				if currentChannelIDs[member.ChannelID] {
					missingCurrentIDs = append(missingCurrentIDs, member.ChannelID)
				} else {
					historicalIDs = append(historicalIDs, member.ChannelID)
				}
			}
		}
	}
	if currentChannelsLoaded {
		for i := range groups {
			for j := range groups[i].Members {
				groups[i].Members[j].Historical = !currentChannelIDs[groups[i].Members[j].ChannelID]
			}
		}
	}
	writeDashboardJSON(w, 200, map[string]any{"items": groups, "coverage": billingCoverage(r.Context(), h.Store, site, from, to), "configured_upstreams": len(upstreams), "unmapped_channels": unmappedChannels, "unmapped_current_channel_ids": missingCurrentIDs, "historical_channel_ids": historicalIDs})
}

func (h BillingUpstreamHandler) requests(w http.ResponseWriter, r *http.Request, site string, from, to time.Time, groups []billing.UpstreamGroup) {
	channelID, _ := strconv.ParseInt(r.URL.Query().Get("channel_id"), 10, 64)
	fp := r.URL.Query().Get("fp")
	allowed := false
	for _, group := range groups {
		if group.UpstreamFP == fp {
			for _, member := range group.Members {
				allowed = allowed || member.ChannelID == channelID
			}
		}
	}
	if channelID <= 0 || !allowed {
		writeDashboardError(w, 400, "invalid_channel")
		return
	}
	if to.Sub(from) > 25*time.Hour {
		writeDashboardError(w, 400, "daily_detail_only")
		return
	}
	file, fileErr := h.Store.ActiveBillingChannelDailyFile(r.Context(), site, from, channelID)
	if fileErr != nil {
		writeDashboardError(w, 404, "billing_channel_daily_file_not_found")
		return
	}
	root, pathErr := filepath.Abs(billing.DefaultBillingFileRoot)
	if pathErr != nil {
		writeDashboardError(w, 500, "billing_file_unavailable")
		return
	}
	path := filepath.Join(root, filepath.FromSlash(file.RelativePath))
	relative, pathErr := filepath.Rel(root, path)
	if pathErr != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) || filepath.IsAbs(relative) {
		writeDashboardError(w, 500, "billing_file_path_invalid")
		return
	}
	handle, openErr := os.Open(path)
	if openErr != nil {
		writeDashboardError(w, 404, "billing_channel_daily_file_missing")
		return
	}
	defer handle.Close()
	info, statErr := handle.Stat()
	if statErr != nil {
		writeDashboardError(w, 500, "billing_file_unavailable")
		return
	}
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="billing-channel-`+strconv.FormatInt(channelID, 10)+`-`+from.In(billing.BusinessLocation).Format("2006-01-02")+`.csv"`)
	http.ServeContent(w, r, info.Name(), info.ModTime(), handle)
	return
	/*
		normal, err := h.Store.QueryBillingChannelRequestDetails(r.Context(), site, channelID, from, to)
		if err != nil {
			writeDashboardError(w, 500, "billing_channel_details_failed")
			return
		}
		abnormal, err := h.Store.QueryBillingChannelAnomalyOrders(r.Context(), site, channelID, from, to)
		if err != nil {
			writeDashboardError(w, 500, "billing_channel_details_failed")
			return
		}
		items := make([]channelRequestItem, 0, len(normal)+len(abnormal))
		for _, row := range normal {
			items = append(items, channelRequestItem{CreatedAt: billing.FormatBusinessTime(row.CreatedUnix), RequestID: row.RequestID, Username: row.Username, TokenName: row.TokenName, ModelName: row.ModelName, PromptTokens: row.PromptTokens, CompletionTokens: row.CompletionTokens, CacheReadTokens: row.CacheReadTokens, CacheWriteTokens: row.CacheWriteTokens, InputPrice: row.Charge.InputPrice, OutputPrice: row.Charge.OutputPrice, CacheReadPrice: row.Charge.CacheReadPrice, CacheWritePrice: row.Charge.CacheWritePrice, Amount: row.Charge.Total})
		}
		for _, row := range abnormal {
			items = append(items, channelRequestItem{CreatedAt: row.CreatedAt.In(billing.BusinessLocation).Format("2006-01-02 15:04:05"), RequestID: row.RequestID, Username: row.Username, TokenName: row.TokenName, ModelName: row.ModelName, PromptTokens: row.PromptTokens.Int64, CompletionTokens: row.CompletionTokens.Int64, CacheReadTokens: row.CacheTokens, CacheWriteTokens: row.CacheWriteTokens, InputPrice: row.InputPrice, OutputPrice: row.OutputPrice, CacheReadPrice: row.CachePrice, CacheWritePrice: row.CacheWritePrice, Amount: row.ActualAmount, Abnormal: true, Reasons: row.Reasons})
		}
		sort.Slice(items, func(i, j int) bool { return items[i].CreatedAt < items[j].CreatedAt })
		if r.URL.Query().Get("format") == "csv" {
			writeChannelRequestsCSV(w, channelID, from, to, items)
			return
		}
		writeDashboardJSON(w, 200, map[string]any{"items": items})
	*/
}

func writeChannelRequestsCSV(w http.ResponseWriter, channelID int64, from, to time.Time, items []channelRequestItem) {
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="`+billingDownloadName("billing-channel-details", 0, channelID, from, to)+`.csv"`)
	_, _ = w.Write([]byte{0xef, 0xbb, 0xbf})
	cw := csv.NewWriter(w)
	_ = cw.Write([]string{"请求时间", "请求ID", "用户", "令牌", "模型", "输入Token", "输出Token", "缓存读取Token", "缓存写入Token", "输入单价", "输出单价", "缓存读取单价", "缓存写入单价", "金额", "订单状态", "异常原因"})
	for _, v := range items {
		_ = cw.Write([]string{v.CreatedAt, v.RequestID, v.Username, v.TokenName, v.ModelName, strconv.FormatInt(v.PromptTokens, 10), strconv.FormatInt(v.CompletionTokens, 10), strconv.FormatInt(v.CacheReadTokens, 10), strconv.FormatInt(v.CacheWriteTokens, 10), v.InputPrice, v.OutputPrice, v.CacheReadPrice, v.CacheWritePrice, v.Amount, map[bool]string{true: "异常订单", false: "正常"}[v.Abnormal], v.Reasons})
	}
	cw.Flush()
}

func (h BillingUpstreamHandler) detail(w http.ResponseWriter, r *http.Request, from, to time.Time, groups []billing.UpstreamGroup, rows []billing.AggregateRow, prices []billing.PriceRecord, ratios []billing.GroupRatio, snapshots map[string]string, anomalies []billing.ChannelAnomalyRow) {
	fp := r.URL.Query().Get("fp")
	channelID, _ := strconv.ParseInt(r.URL.Query().Get("channel_id"), 10, 64)
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
		if channelID == 0 || m.ChannelID == channelID {
			ids[m.ChannelID] = true
		}
	}
	if channelID > 0 && !ids[channelID] {
		writeDashboardError(w, 404, "billing_channel_not_in_upstream")
		return
	}
	filtered := []billing.AggregateRow{}
	for _, row := range rows {
		if ids[row.UserID] {
			filtered = append(filtered, row)
		}
	}
	merged := billing.MergeUpstreamDetails(filtered)
	priced := billing.BuildDetails(merged, prices, ratios, snapshots)
	anomalyByKey := map[string]billing.ChannelAnomalyRow{}
	for _, row := range anomalies {
		if ids[row.ChannelID] {
			key := row.Day.Format("2006-01-02") + "\x00" + row.ModelName
			current := anomalyByKey[key]
			current.Rows += row.Rows
			current.Amount = addAmountStrings(current.Amount, row.Amount)
			anomalyByKey[key] = current
		}
	}
	details := make([]upstreamDetailItem, 0, len(priced))
	for _, v := range priced {
		anomaly := anomalyByKey[v.Day+"\x00"+v.ModelName]
		details = append(details, upstreamDetailItem{Day: v.Day, ModelName: v.ModelName, GroupName: v.GroupName, TierFrom: v.TierFrom, RequestCount: v.RequestCount, PromptTokens: v.PromptTokens, CompletionTokens: v.CompletionTokens, CacheTokens: v.CacheTokens, CacheWriteTokens: v.CacheWriteTokens, Quota: v.Quota, Amount: v.Amount, Unpriced: v.Unpriced, AbnormalRows: anomaly.Rows, AbnormalAmount: anomaly.Amount})
		delete(anomalyByKey, v.Day+"\x00"+v.ModelName)
	}
	for key, anomaly := range anomalyByKey {
		parts := strings.SplitN(key, "\x00", 2)
		details = append(details, upstreamDetailItem{Day: parts[0], ModelName: parts[1], Unpriced: true, AbnormalRows: anomaly.Rows, AbnormalAmount: anomaly.Amount})
	}
	if r.URL.Query().Get("format") == "csv" {
		writeUpstreamCSV(w, billingDownloadName("billing-upstream", 0, channelID, from, to)+".csv", from, to, *selected, details)
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
	_ = cw.Write([]string{"上游", group.DisplayName})
	_ = cw.Write([]string{"成员渠道", strings.Join(memberNames, "、")})
	_ = cw.Write([]string{"账单区间", billing.FormatBusinessTime(from.Unix()), billing.FormatBusinessTime(to.Unix())})
	_ = cw.Write(nil)
	_ = cw.Write([]string{"日×模型明细"})
	_ = cw.Write([]string{"日期", "模型", "分组", "档位", "请求数", "普通输入Token", "缓存读取Token", "缓存写入Token", "输出Token", "正常金额", "异常订单数", "异常金额"})
	for _, v := range details {
		amount := v.Amount
		if v.Unpriced {
			amount = ""
		}
		_ = cw.Write([]string{v.Day, v.ModelName, v.GroupName, strconv.FormatInt(v.TierFrom, 10), strconv.FormatInt(v.RequestCount, 10), strconv.FormatInt(v.PromptTokens, 10), strconv.FormatInt(v.CacheTokens, 10), strconv.FormatInt(v.CacheWriteTokens, 10), strconv.FormatInt(v.CompletionTokens, 10), amount, strconv.FormatInt(v.AbnormalRows, 10), v.AbnormalAmount})
	}
	_ = cw.Write(nil)
	_ = cw.Write([]string{"成员渠道小计"})
	_ = cw.Write([]string{"渠道ID", "渠道名", "模型", "请求数", "普通输入Token", "缓存读取Token", "缓存写入Token", "输出Token", "正常金额", "异常订单数", "异常金额"})
	for _, m := range group.Members {
		v := m.Totals
		_ = cw.Write([]string{strconv.FormatInt(m.ChannelID, 10), m.ChannelName, m.ModelName, strconv.FormatInt(v.RequestCount, 10), strconv.FormatInt(v.PromptTokens, 10), strconv.FormatInt(v.CacheTokens, 10), strconv.FormatInt(v.CacheWriteTokens, 10), strconv.FormatInt(v.CompletionTokens, 10), v.Amount, strconv.FormatInt(v.AbnormalRows, 10), v.AbnormalAmount})
	}
	cw.Flush()
}
