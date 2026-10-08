package billing

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

type upstreamWorkbookStore struct {
	dailyFileStoreStub
	channels []ChannelDailyFile
}

func (s *upstreamWorkbookStore) PutBillingChannelDailyFile(_ context.Context, f ChannelDailyFile) error {
	s.channels = append(s.channels, f)
	return nil
}
func TestUpstreamDailyFilesGroupDifferentUsersByChannel(t *testing.T) {
	day := time.Date(2026, 9, 2, 0, 0, 0, 0, BusinessLocation)
	job := Job{ID: "0123456789abcdef0123456789abcdef", InstanceID: "site", JobType: "upstream_statement", UsageVersion: SettlementUsageVersion, From: day, To: day.AddDate(0, 0, 1), UpstreamID: 3}
	rows := []RequestDetail{}
	for i, pair := range [][2]int64{{9, 5}, {11, 5}, {9, 10}} {
		rows = append(rows, RequestDetail{UserID: pair[0], ChannelID: pair[1], BillDay: day, CreatedUnix: day.Unix(), RequestID: []string{"request-a", "request-b", "request-c"}[i], ChannelName: "channel", ModelName: "model", Charge: LogCharge{Total: "0.25", Settlement: &Settlement{Amount: "0.25", BeforeAmount: "0.5", Discount: "0.5"}}})
	}
	spool := FileDetailSpool{Root: t.TempDir()}
	if err := spool.WritePage(context.Background(), job, JobStep{}, LogCursor{ID: 3}, rows); err != nil {
		t.Fatal(err)
	}
	store := &upstreamWorkbookStore{}
	root := t.TempDir()
	if err := (UserDailyFileGenerator{Store: store, Root: root, Spool: spool}).GenerateJobFiles(context.Background(), job); err != nil {
		t.Fatal(err)
	}
	if len(store.channels) != 2 {
		t.Fatalf("channels: %+v", store.channels)
	}
	for _, f := range store.channels {
		content := workbookText(t, filepath.Join(root, f.RelativePath))
		for _, want := range []string{"渠道 ID", "原价金额", "折扣", "折后金额", "0.250000", "0.500000", "5 折"} {
			if !strings.Contains(content, want) {
				t.Fatalf("missing %s", want)
			}
		}
		if strings.Contains(content, "令牌") || strings.Contains(content, "按令牌统计") {
			t.Fatal("user dimension leaked")
		}
		if f.ChannelID == 5 {
			if !strings.Contains(content, "request-a") || !strings.Contains(content, "request-b") || strings.Contains(content, "request-c") {
				t.Fatal("wrong channel grouping")
			}
		} else if !strings.Contains(content, "request-c") || strings.Contains(content, "request-a") {
			t.Fatal("wrong second channel")
		}
	}
}
