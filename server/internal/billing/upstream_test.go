package billing

import (
	"testing"
	"time"
)

func TestBuildUpstreamGroupsMergesMembersAndKeepsUnmapped(t *testing.T) {
	day := time.Date(2026, 8, 1, 0, 0, 0, 0, BusinessLocation)
	rows := []AggregateRow{{UserID: 1, Username: "a", ModelName: "m", Day: day, RequestCount: 2, PromptTokens: 10, Quota: 20}, {UserID: 2, Username: "b", ModelName: "m", Day: day, RequestCount: 3, PromptTokens: 11, Quota: 30}, {UserID: 9, Username: "old", ModelName: "x", Day: day, RequestCount: 4, Quota: 40}}
	maps := []UpstreamChannelMapping{{ChannelID: 1, UpstreamFP: "fp", BaseURL: "https://up", KeyTail: "1234", ChannelName: "a"}, {ChannelID: 2, UpstreamFP: "fp", BaseURL: "https://up", KeyTail: "1234", ChannelName: "b"}}
	groups := BuildUpstreamGroups(rows, maps)
	if len(groups) != 2 || groups[0].UpstreamFP != "fp" || groups[0].MemberCount != 2 || groups[0].Totals.RequestCount != 5 || groups[0].Totals.PromptTokens != 21 {
		t.Fatalf("groups=%#v", groups)
	}
	if groups[1].UpstreamFP != "" || groups[1].Members[0].ChannelID != 9 {
		t.Fatalf("unmapped=%#v", groups[1])
	}
}

func TestMergeUpstreamDetailsAddsSameModelAcrossChannels(t *testing.T) {
	day := time.Now()
	out := MergeUpstreamDetails([]AggregateRow{{UserID: 1, Day: day, ModelName: "m", RequestCount: 2, Quota: 3}, {UserID: 2, Day: day, ModelName: "m", RequestCount: 5, Quota: 7}})
	if len(out) != 1 || out[0].RequestCount != 7 || out[0].Quota != 10 {
		t.Fatalf("out=%#v", out)
	}
}

func TestApplyUpstreamAmountsAddsMembersAndGroupTotal(t *testing.T) {
	groups := []UpstreamGroup{{Members: []UpstreamMember{{ChannelID: 1}, {ChannelID: 2}}}}
	ApplyUpstreamAmounts(groups, []ChannelSummary{{ChannelID: 1, Amount: "1.250000"}, {ChannelID: 2, Amount: "2.750000"}})
	if groups[0].Members[0].Totals.Amount != "1.250000" || groups[0].Totals.Amount != "4.000000" {
		t.Fatalf("groups=%#v", groups)
	}
}
