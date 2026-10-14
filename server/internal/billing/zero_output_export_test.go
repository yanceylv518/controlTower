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

func BenchmarkZeroOutputWorkbook100k(b *testing.B) {
	row := RequestDetail{EmptyOutput: true, SourceLogID: 9007199254740993, RequestID: "request-001", UserID: 7, Username: "benchmark-user", ModelName: "benchmark-model", Charge: LogCharge{Total: "0.5", Settlement: &Settlement{BeforeAmount: "1", Discount: "0.5"}}}
	for n := 0; n < b.N; n++ {
		if err := WriteZeroOutputWorkbook(context.Background(), io.Discard, Job{}, func(visit func(RequestDetail) error) error {
			for i := 0; i < 100000; i++ {
				if err := visit(row); err != nil {
					return err
				}
			}
			return nil
		}); err != nil {
			b.Fatal(err)
		}
	}
}

func TestZeroOutputWorkbookCurrencyAndSplit(t *testing.T) {
	money, err := NewMoneySnapshot("site", `{"QuotaPerUnit":"500000","USDExchangeRate":"7.2","general_setting.quota_display_type":"CNY"}`, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	err = WriteZeroOutputWorkbook(context.Background(), &out, Job{MoneySnapshot: money}, func(visit func(RequestDetail) error) error {
		for i := 0; i <= SettlementDetailPartRows; i++ {
			if err := visit(RequestDetail{EmptyOutput: true, DiagnosticOnly: i%2 == 0, SourceLogID: int64(i + 1), Charge: LogCharge{Total: "0.5", Settlement: &Settlement{BeforeAmount: "1", Discount: "0.5"}}}); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
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
		r, _ := f.Open()
		b, _ := io.ReadAll(r)
		r.Close()
		s := string(b)
		for _, want := range []string{"CNY", "3.600000000000", "7.200000000000", "按生成时可用数据"} {
			if !strings.Contains(s, want) {
				t.Fatal("missing", want)
			}
		}
	}
	if sheets != 2 {
		t.Fatal("missing split", sheets)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err = WriteZeroOutputWorkbook(ctx, io.Discard, Job{}, func(visit func(RequestDetail) error) error { return visit(RequestDetail{EmptyOutput: true}) }); err != context.Canceled {
		t.Fatal(err)
	}
}
