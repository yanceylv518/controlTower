package billing

import (
	"math/big"
	"sort"
	"strings"
	"time"
)

type UpstreamChannelMapping struct {
	InstanceID, UpstreamFP, UpstreamName, BaseURL, KeyTail, ChannelName string
	ChannelID                                                           int64
	UpdatedAt                                                           time.Time
}

type Upstream struct {
	ID         int64             `json:"id"`
	InstanceID string            `json:"instance_id"`
	Name       string            `json:"name"`
	Enabled    bool              `json:"enabled"`
	Remark     string            `json:"remark"`
	Channels   []UpstreamChannel `json:"channels"`
	CreatedAt  time.Time         `json:"created_at"`
	UpdatedAt  time.Time         `json:"updated_at"`
	UpdatedBy  string            `json:"updated_by"`
}

type UpstreamChannel struct {
	ChannelID   int64  `json:"channel_id"`
	ChannelName string `json:"channel_name"`
}

type UpstreamTotals struct {
	RequestCount     int64  `json:"request_count"`
	PromptTokens     int64  `json:"prompt_tokens"`
	CompletionTokens int64  `json:"completion_tokens"`
	CacheTokens      int64  `json:"cache_tokens"`
	CacheWriteTokens int64  `json:"cache_write_tokens"`
	Quota            int64  `json:"quota"`
	Amount           string `json:"amount"`
	AbnormalRows     int64  `json:"abnormal_rows"`
	AbnormalAmount   string `json:"abnormal_amount"`
}

type ChannelAnomalyRow struct {
	ChannelID   int64
	ChannelName string
	Day         time.Time
	ModelName   string
	Rows        int64
	Amount      string
}

func ApplyUpstreamAnomalies(groups []UpstreamGroup, mappings []UpstreamChannelMapping, anomalies []ChannelAnomalyRow) []UpstreamGroup {
	byMapping := map[int64]UpstreamChannelMapping{}
	for _, mapping := range mappings {
		byMapping[mapping.ChannelID] = mapping
	}
	memberGroup := map[int64]int{}
	for i := range groups {
		for _, member := range groups[i].Members {
			memberGroup[member.ChannelID] = i
		}
	}
	for _, row := range anomalies {
		if _, exists := memberGroup[row.ChannelID]; exists {
			continue
		}
		mapping, mapped := byMapping[row.ChannelID]
		fp, display := "", "未归属上游"
		if mapped {
			fp, display = mapping.UpstreamFP, strings.TrimSpace(mapping.UpstreamName)
		}
		groupIndex := -1
		for i := range groups {
			if groups[i].UpstreamFP == fp {
				groupIndex = i
				break
			}
		}
		if groupIndex < 0 {
			groups = append(groups, UpstreamGroup{UpstreamFP: fp, DisplayName: display})
			groupIndex = len(groups) - 1
		}
		name := row.ChannelName
		if mapped && mapping.ChannelName != "" {
			name = mapping.ChannelName
		}
		groups[groupIndex].Members = append(groups[groupIndex].Members, UpstreamMember{ChannelID: row.ChannelID, ChannelName: name})
		groups[groupIndex].MemberCount = len(groups[groupIndex].Members)
		memberGroup[row.ChannelID] = groupIndex
	}
	byChannel := map[int64][]ChannelAnomalyRow{}
	for _, row := range anomalies {
		byChannel[row.ChannelID] = append(byChannel[row.ChannelID], row)
	}
	for i := range groups {
		groupAmount := new(big.Rat)
		for j := range groups[i].Members {
			memberAmount := new(big.Rat)
			daySet := map[string]bool{}
			for _, day := range groups[i].Members[j].BillDays {
				daySet[day] = true
			}
			for _, row := range byChannel[groups[i].Members[j].ChannelID] {
				daySet[row.Day.Format("2006-01-02")] = true
				groups[i].Members[j].Totals.AbnormalRows += row.Rows
				groups[i].Totals.AbnormalRows += row.Rows
				if value, ok := new(big.Rat).SetString(row.Amount); ok {
					memberAmount.Add(memberAmount, value)
					groupAmount.Add(groupAmount, value)
				}
			}
			groups[i].Members[j].Totals.AbnormalAmount = FormatAmount(memberAmount, 6)
			groups[i].Members[j].BillDays = groups[i].Members[j].BillDays[:0]
			for day := range daySet {
				groups[i].Members[j].BillDays = append(groups[i].Members[j].BillDays, day)
			}
			sort.Sort(sort.Reverse(sort.StringSlice(groups[i].Members[j].BillDays)))
		}
		groups[i].Totals.AbnormalAmount = FormatAmount(groupAmount, 6)
	}
	return groups
}

func uniqueStrings(values []string) []string {
	if len(values) < 2 {
		return values
	}
	out := values[:1]
	for _, value := range values[1:] {
		if value != out[len(out)-1] {
			out = append(out, value)
		}
	}
	return out
}

func ApplyUpstreamAmounts(groups []UpstreamGroup, channels []ChannelSummary) {
	byChannel := make(map[int64]string, len(channels))
	for _, channel := range channels {
		byChannel[channel.ChannelID] = channel.Amount
	}
	for i := range groups {
		total := new(big.Rat)
		for j := range groups[i].Members {
			amount := byChannel[groups[i].Members[j].ChannelID]
			groups[i].Members[j].Totals.Amount = amount
			if value, ok := new(big.Rat).SetString(amount); ok {
				total.Add(total, value)
			}
		}
		groups[i].Totals.Amount = FormatAmount(total, 6)
	}
}

type UpstreamMember struct {
	ChannelID   int64          `json:"channel_id"`
	ChannelName string         `json:"channel_name"`
	ModelName   string         `json:"model_name"`
	Historical  bool           `json:"historical"`
	BillDays    []string       `json:"bill_days"`
	Totals      UpstreamTotals `json:"totals"`
}

type UpstreamGroup struct {
	UpstreamFP  string           `json:"upstream_fp"`
	DisplayName string           `json:"display_name"`
	BaseURL     string           `json:"base_url"`
	MemberCount int              `json:"member_count"`
	Members     []UpstreamMember `json:"members"`
	Totals      UpstreamTotals   `json:"totals"`
	BillDays    []string         `json:"bill_days"`
}

func addUpstreamTotals(v *UpstreamTotals, row AggregateRow) {
	v.RequestCount += row.RequestCount
	v.PromptTokens += row.PromptTokens
	v.CompletionTokens += row.CompletionTokens
	v.CacheTokens += row.CacheTokens
	v.CacheWriteTokens += row.CacheWriteTokens
	v.Quota += row.Quota
}

func BuildUpstreamGroups(rows []AggregateRow, mappings []UpstreamChannelMapping) []UpstreamGroup {
	byChannel := map[int64]UpstreamChannelMapping{}
	for _, m := range mappings {
		byChannel[m.ChannelID] = m
	}
	type memberAcc struct {
		item   UpstreamMember
		models map[string]bool
	}
	type groupAcc struct {
		item    UpstreamGroup
		members map[int64]*memberAcc
	}
	groups := map[string]*groupAcc{}
	for _, row := range rows {
		m, mapped := byChannel[row.UserID]
		fp := ""
		display := "未归组"
		base := ""
		if mapped {
			fp, base = m.UpstreamFP, m.BaseURL
			display = strings.TrimSpace(m.UpstreamName)
			if display == "" {
				display = strings.TrimSpace(m.BaseURL)
				if m.KeyTail != "" {
					display += " …" + m.KeyTail
				}
			}
		}
		g := groups[fp]
		if g == nil {
			g = &groupAcc{item: UpstreamGroup{UpstreamFP: fp, DisplayName: display, BaseURL: base}, members: map[int64]*memberAcc{}}
			groups[fp] = g
		}
		member := g.members[row.UserID]
		if member == nil {
			name := row.Username
			if mapped && m.ChannelName != "" {
				name = m.ChannelName
			}
			member = &memberAcc{item: UpstreamMember{ChannelID: row.UserID, ChannelName: name}, models: map[string]bool{}}
			g.members[row.UserID] = member
		}
		member.models[row.ModelName] = true
		member.item.BillDays = append(member.item.BillDays, row.Day.Format("2006-01-02"))
		addUpstreamTotals(&member.item.Totals, row)
		addUpstreamTotals(&g.item.Totals, row)
	}
	out := make([]UpstreamGroup, 0, len(groups))
	for _, g := range groups {
		daySet := map[string]bool{}
		for _, row := range rows {
			mapping, mapped := byChannel[row.UserID]
			if (g.item.UpstreamFP == "" && !mapped) || (mapped && mapping.UpstreamFP == g.item.UpstreamFP) {
				daySet[row.Day.Format("2006-01-02")] = true
			}
		}
		for day := range daySet {
			g.item.BillDays = append(g.item.BillDays, day)
		}
		sort.Sort(sort.Reverse(sort.StringSlice(g.item.BillDays)))
		for _, m := range g.members {
			names := make([]string, 0, len(m.models))
			for name := range m.models {
				names = append(names, name)
			}
			sort.Strings(names)
			m.item.ModelName = strings.Join(names, ", ")
			sort.Sort(sort.Reverse(sort.StringSlice(m.item.BillDays)))
			m.item.BillDays = uniqueStrings(m.item.BillDays)
			g.item.Members = append(g.item.Members, m.item)
		}
		sort.Slice(g.item.Members, func(i, j int) bool { return g.item.Members[i].ChannelID < g.item.Members[j].ChannelID })
		g.item.MemberCount = len(g.item.Members)
		out = append(out, g.item)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].UpstreamFP == "" {
			return false
		}
		if out[j].UpstreamFP == "" {
			return true
		}
		if out[i].Totals.Quota != out[j].Totals.Quota {
			return out[i].Totals.Quota > out[j].Totals.Quota
		}
		return out[i].DisplayName < out[j].DisplayName
	})
	return out
}

func MergeUpstreamDetails(rows []AggregateRow) []AggregateRow {
	type key struct {
		day          string
		model, group string
		tier         int64
	}
	acc := map[key]AggregateRow{}
	for _, r := range rows {
		k := key{r.Day.Format("2006-01-02"), r.ModelName, r.GroupName, r.TierFrom}
		v := acc[k]
		if v.Day.IsZero() {
			v = r
			v.UserID = 0
			v.Username = ""
			v.RequestCount = 0
			v.PromptTokens = 0
			v.CompletionTokens = 0
			v.CacheTokens = 0
			v.CacheWriteTokens = 0
			v.CacheWrite5mTokens = 0
			v.CacheWrite1hTokens = 0
			v.Quota = 0
		}
		v.RequestCount += r.RequestCount
		v.PromptTokens += r.PromptTokens
		v.CompletionTokens += r.CompletionTokens
		v.CacheTokens += r.CacheTokens
		v.CacheWriteTokens += r.CacheWriteTokens
		v.CacheWrite5mTokens += r.CacheWrite5mTokens
		v.CacheWrite1hTokens += r.CacheWrite1hTokens
		v.Quota += r.Quota
		acc[k] = v
	}
	out := make([]AggregateRow, 0, len(acc))
	for _, v := range acc {
		out = append(out, v)
	}
	sort.Slice(out, func(i, j int) bool {
		if !out[i].Day.Equal(out[j].Day) {
			return out[i].Day.Before(out[j].Day)
		}
		return out[i].ModelName < out[j].ModelName
	})
	return out
}
