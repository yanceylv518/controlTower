package billing

import (
	"archive/zip"
	"bytes"
	"encoding/xml"
	"io"
	"testing"
	"time"
)

func TestDiscountGroupKey(t *testing.T) {
	for _, tc := range []struct{ value, want string }{
		{"", "1.000000"}, {"  ", "1.000000"}, {"1", "1.000000"}, {"1.0", "1.000000"},
		{"0.46", "0.460000"}, {"0.460000", "0.460000"}, {"0", "0.000000"}, {"mixed", "mixed"},
	} {
		if got := DiscountGroupKey(tc.value); got != tc.want {
			t.Fatalf("%q = %q, want %q", tc.value, got, tc.want)
		}
	}
}

func TestDailyWorkbookSeparatesDiscountUsage(t *testing.T) {
	snapshot, _ := NewMoneySnapshot("site", `{"QuotaPerUnit":"500000","general_setting.quota_display_type":"USD"}`, time.Now())
	job := Job{UsageVersion: SettlementUsageVersion, JobType: "user_statement", BillPeriod: "daily", MoneySnapshot: snapshot}
	rows := []RequestDetail{}
	for _, rate := range []string{"1", "0.46", "0.460000", "0"} {
		amount := rate
		rows = append(rows, RequestDetail{ModelName: "m", TokenName: "prod", TokenID: 7, PromptTokens: 10, Charge: LogCharge{Total: amount, Settlement: &Settlement{BeforeAmount: "1", Discount: rate}}})
	}
	var out bytes.Buffer
	err := WriteUserDailyWorkbook(&out, job, UserDailyFile{}, rows)
	if err != nil {
		t.Fatal(err)
	}
	z, err := zip.NewReader(bytes.NewReader(out.Bytes()), int64(out.Len()))
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"xl/worksheets/sheet2.xml", "xl/worksheets/sheet3.xml"} {
		f, err := z.Open(name)
		if err != nil {
			t.Fatal(err)
		}
		data, err := io.ReadAll(f)
		f.Close()
		if err != nil {
			t.Fatal(err)
		}
		var sheet struct {
			Rows []struct {
				Cells []struct {
					Value string `xml:"v"`
					Text  string `xml:"is>t"`
				} `xml:"c"`
			} `xml:"sheetData>row"`
		}
		if err = xml.Unmarshal(data, &sheet); err != nil {
			t.Fatal(err)
		}
		counts := map[string]string{}
		amounts := map[string]string{}
		for _, row := range sheet.Rows {
			if len(row.Cells) != 14 || (row.Cells[0].Text != "m" && row.Cells[0].Text != "prod (#7)") {
				continue
			}
			label := row.Cells[12].Text
			if _, exists := counts[label]; exists {
				t.Fatal("duplicate rate", label)
			}
			counts[label], amounts[label] = row.Cells[1].Value, row.Cells[13].Value
		}
		if len(counts) != 3 || counts["原价"] != "1" || counts["4.6 折"] != "2" || counts["0 折"] != "1" || amounts["4.6 折"] != "0.920000000000" || amounts["原价"] != "1.000000000000" || amounts["0 折"] != "0.000000000000" {
			t.Fatalf("%s: counts=%v amounts=%v", name, counts, amounts)
		}
	}
}
