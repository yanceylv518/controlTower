package billing

import (
	"archive/zip"
	"bytes"
	"context"
	"io"
	"strings"
	"testing"
	"time"
)

func TestSettlementDailyExportIncludesEmptyOrdersWithoutDiagnosticColumns(t *testing.T) {
	var out bytes.Buffer
	rows := []RequestDetail{{RequestID: "regular-request", MultimediaUsage: MultimediaUsage{ImageInputTokens: 513, ImageOutputTokens: 7, AudioInputTokens: 31, AudioOutputTokens: 11}, ModelName: "model", TokenName: "token", PromptTokens: 10, CompletionTokens: 3, Charge: LogCharge{Total: "1.250000000000"}}, {RequestID: "empty-request", ModelName: "model", TokenName: "token", PromptTokens: 5, CompletionTokens: 0, EmptyOutput: true, Charge: LogCharge{Total: "0.500000000000"}}}
	if err := WriteUserDailyWorkbook(&out, Job{UsageVersion: SettlementUsageVersion}, UserDailyFile{BillDay: time.Now()}, rows); err != nil {
		t.Fatal(err)
	}
	z, err := zip.NewReader(bytes.NewReader(out.Bytes()), int64(out.Len()))
	if err != nil {
		t.Fatal(err)
	}
	all := ""
	sheets := 0
	for _, f := range z.File {
		r, e := f.Open()
		if e != nil {
			t.Fatal(e)
		}
		b, e := io.ReadAll(r)
		r.Close()
		if e != nil {
			t.Fatal(e)
		}
		all += string(b)
		if strings.HasPrefix(f.Name, "xl/worksheets/sheet") {
			sheets++
		}
	}
	for _, want := range []string{"每日统计", "按模型统计", "按令牌统计", "账单明细", "regular-request", "empty-request", "1.750000000000", "图像输入 Token", "图像输出 Token", "音频输入 Token", "音频输出 Token", "<v>513</v>"} {
		if !strings.Contains(all, want) {
			t.Errorf("missing export content %s", want)
		}
	}
	for _, forbidden := range []string{"空输出", "异常原因", "订单状态", "EmptyOutput"} {
		if strings.Contains(all, forbidden) {
			t.Errorf("administrative diagnostics leaked into bill: %s", forbidden)
		}
	}
	if sheets != 4 {
		t.Fatalf("sheets=%d", sheets)
	}
}
func TestSettlementDailyZeroOrdersStillHasStatistics(t *testing.T) {
	var out bytes.Buffer
	if err := WriteUserDailyWorkbook(&out, Job{UsageVersion: SettlementUsageVersion}, UserDailyFile{BillDay: time.Now()}, nil); err != nil {
		t.Fatal(err)
	}
	if out.Len() == 0 {
		t.Fatal("empty download")
	}
}

func TestSettlementDailyExportConvertsSnapshotCNY(t *testing.T) {
	snapshot, err := NewMoneySnapshot("site", `{"QuotaPerUnit":"500000","USDExchangeRate":"7.2","general_setting.quota_display_type":"CNY"}`, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	rows := []RequestDetail{{RequestID: "cny-request", Charge: LogCharge{Total: "1.25", Settlement: &Settlement{BeforeAmount: "2.5", Discount: "0.5"}}}}
	var out bytes.Buffer
	if err = WriteUserDailyWorkbook(&out, Job{UsageVersion: 3, MoneySnapshot: snapshot}, UserDailyFile{}, rows); err != nil {
		t.Fatal(err)
	}
	z, err := zip.NewReader(bytes.NewReader(out.Bytes()), int64(out.Len()))
	if err != nil {
		t.Fatal(err)
	}
	sheets := 0
	for _, f := range z.File {
		if !strings.HasPrefix(f.Name, "xl/worksheets/sheet") {
			continue
		}
		sheets++
		r, e := f.Open()
		if e != nil {
			t.Fatal(e)
		}
		b, e := io.ReadAll(r)
		r.Close()
		if e != nil {
			t.Fatal(e)
		}
		text := string(b)
		if !strings.Contains(text, "折后金额 CNY") || !strings.Contains(text, `s="17"><v>9.000000000000</v>`) || !strings.Contains(text, `s="17"><v>18.000000000000</v>`) || !strings.Contains(text, "5 折") || strings.Contains(text, "折后金额 USD") {
			t.Fatalf("sheet not converted: %s", f.Name)
		}
	}
	if sheets != 4 {
		t.Fatal(sheets)
	}
	if rows[0].Charge.Total != "1.25" {
		t.Fatal("stored USD amount changed")
	}
}

func TestSettlementRestylePreservesAmountsAndLayout(t *testing.T) {
	snapshot, err := NewMoneySnapshot("site", `{"QuotaPerUnit":"500000","USDExchangeRate":"7.2","general_setting.quota_display_type":"CNY"}`, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	job := Job{UsageVersion: 3, MoneySnapshot: snapshot, UserID: 4}
	var original, styled bytes.Buffer
	rows := []RequestDetail{{RequestID: "long-request-id-to-preserve", Charge: LogCharge{Total: "1.25", Settlement: &Settlement{BeforeAmount: "2.5", Discount: "0.5"}}}}
	if err = WriteUserDailyWorkbook(&original, job, UserDailyFile{}, rows); err != nil {
		t.Fatal(err)
	}
	if err = RestyleSettlementDailyFile(context.Background(), &styled, bytes.NewReader(original.Bytes()), int64(original.Len()), job); err != nil {
		t.Fatal(err)
	}
	z, err := zip.NewReader(bytes.NewReader(styled.Bytes()), int64(styled.Len()))
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range z.File {
		if !strings.HasPrefix(f.Name, "xl/worksheets/sheet") {
			continue
		}
		r, e := f.Open()
		if e != nil {
			t.Fatal(e)
		}
		b, e := io.ReadAll(r)
		r.Close()
		if e != nil {
			t.Fatal(e)
		}
		s := string(b)
		for _, want := range []string{`s="17"><v>9.000000000000</v>`, `s="17"><v>18.000000000000</v>`, `topLeftCell="A5"`, `<autoFilter ref="A4:`, `orientation="landscape"`, `用户 #4`} {
			if !strings.Contains(s, want) {
				t.Fatal("missing formatting or amount", f.Name, want)
			}
		}
		if strings.Contains(s, "64.800000000000") {
			t.Fatal("restyling applied exchange rate twice")
		}
	}
}
