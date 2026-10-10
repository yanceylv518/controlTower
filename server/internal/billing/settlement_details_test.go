package billing

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/xml"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestSettlementDetailShardsPaginationAndExport(t *testing.T) {
	path := filepath.Join(t.TempDir(), "details.zip")
	file, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	count := SettlementDetailPartRows + 3
	err = WriteSettlementDetailArchive(context.Background(), file, "CNY", func(visit func(SettlementDetailRow) error) error {
		for i := 0; i < count; i++ {
			model := "a"
			if i%2 == 1 {
				model = "b"
			}
			if err := visit(SettlementDetailRow{Time: "2026-09-11 10:00:00", RequestID: fmt.Sprintf("req-%d", i), Model: model, Token: "prod", TokenID: "9007199254740993", Input: "10", Output: "0", CacheRead: "0", CacheWrite: "0", Amount: "0.012345678901"}); err != nil {
				return err
			}
		}
		return nil
	}, nil)
	file.Close()
	if err != nil {
		t.Fatal(err)
	}
	reader, err := OpenSettlementDetails(path)
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	if len(reader.Manifest.Parts) != 2 || reader.Manifest.Total != int64(count) {
		t.Fatal(reader.Manifest)
	}
	filter := SettlementDetailFilter{Model: "a", Token: "prod (#9007199254740993)", From: "2026-09-11 10:00:00", To: "2026-09-11 10:00:01"}
	rows, next, err := reader.Page(context.Background(), filter, SettlementDetailPartRows-3, 2)
	if err != nil || len(rows) != 2 || rows[0].RequestID != "req-49998" || rows[1].RequestID != "req-50000" || next != 50001 {
		t.Fatalf("boundary page %+v %d %v", rows, next, err)
	}
	last, end, err := reader.Page(context.Background(), filter, next, 2)
	if err != nil || len(last) != 1 || last[0].RequestID != "req-50002" || end != 0 {
		t.Fatalf("lost/duplicated tail %+v %d %v", last, end, err)
	}
	empty, _, err := reader.Page(context.Background(), SettlementDetailFilter{To: "2026-09-11 10:00:00"}, 0, 100)
	if err != nil || len(empty) != 0 {
		t.Fatal("exclusive upper boundary violated", err)
	}
	var out bytes.Buffer
	processed := int64(0)
	n, err := reader.Export(context.Background(), &out, Job{}, SettlementDetailFilter{}, func(v int64) { processed = v })
	if err != nil || n != int64(count) || processed != int64(count) {
		t.Fatal(n, processed, err)
	}
	z, err := zip.NewReader(bytes.NewReader(out.Bytes()), int64(out.Len()))
	if err != nil {
		t.Fatal(err)
	}
	if len(z.File) != 2 {
		t.Fatal("unbounded workbook", len(z.File))
	}
	var actual int
	for i, f := range z.File {
		r, _ := f.Open()
		b, e := io.ReadAll(r)
		r.Close()
		if e != nil {
			t.Fatal(e)
		}
		book, e := zip.NewReader(bytes.NewReader(b), int64(len(b)))
		if e != nil {
			t.Fatal(e)
		}
		sheet, e := book.Open("xl/worksheets/sheet1.xml")
		if e != nil {
			t.Fatal(e)
		}
		dec := xml.NewDecoder(sheet)
		rowCount := 0
		for {
			token, e := dec.Token()
			if e == io.EOF {
				break
			}
			if e != nil {
				t.Fatal(e)
			}
			if start, ok := token.(xml.StartElement); ok && start.Name.Local == "row" {
				rowCount++
			}
		}
		sheet.Close()
		rowCount -= 4
		actual += rowCount
		if i == 0 && rowCount != SettlementDetailPartRows || i == 1 && rowCount != 3 {
			t.Fatal("wrong split", i, rowCount)
		}
	}
	if actual != count {
		t.Fatal("export row loss", actual)
	}
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, _, err = reader.Page(cancelled, SettlementDetailFilter{}, 0, 100); err == nil {
		t.Fatal("cancel ignored")
	}
}

func TestSettlementDetailExportKeepsOriginalAmountDecimals(t *testing.T) {
	path := filepath.Join(t.TempDir(), "details.zip")
	file, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	rows := []SettlementDetailRow{
		{RequestID: "small", BeforeAmount: "0.026696", Discount: "0.460000", Amount: "0.012280"},
		{RequestID: "zero", BeforeAmount: "0", Discount: "1", Amount: "0"},
		{RequestID: "unknown", BeforeAmount: "", Discount: "0.46", Amount: "0.012280"},
	}
	err = WriteSettlementDetailArchive(context.Background(), file, "CNY", func(visit func(SettlementDetailRow) error) error {
		for _, row := range rows {
			if err := visit(row); err != nil {
				return err
			}
		}
		return nil
	}, nil)
	file.Close()
	if err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	reader, err := OpenSettlementDetails(path)
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	var out bytes.Buffer
	n, err := reader.Export(context.Background(), &out, Job{}, SettlementDetailFilter{}, nil)
	if err != nil || n != int64(len(rows)) {
		t.Fatal(n, err)
	}
	bundle, err := zip.NewReader(bytes.NewReader(out.Bytes()), int64(out.Len()))
	if err != nil || len(bundle.File) != 1 {
		t.Fatal("invalid detail bundle", err)
	}
	entry, err := bundle.File[0].Open()
	if err != nil {
		t.Fatal(err)
	}
	bookBytes, err := io.ReadAll(entry)
	entry.Close()
	if err != nil {
		t.Fatal(err)
	}
	book, err := zip.NewReader(bytes.NewReader(bookBytes), int64(len(bookBytes)))
	if err != nil {
		t.Fatal(err)
	}
	sheet, err := book.Open("xl/worksheets/sheet1.xml")
	if err != nil {
		t.Fatal(err)
	}
	raw, err := io.ReadAll(sheet)
	sheet.Close()
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`r="O5" s="17"><v>0.026696</v>`, `r="Q5" s="17"><v>0.012280</v>`, `r="O6" s="17"><v>0</v>`, `r="O7" t="inlineStr" s="17"><is><t xml:space="preserve">—</t>`} {
		if !strings.Contains(string(raw), want) {
			t.Errorf("missing amount value/format: %s", want)
		}
	}
	after, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(before, after) {
		t.Fatal("export changed the issued detail archive", err)
	}
}
func TestSettlementLegacyDetailsPreserveCurrencyAndEmptyRows(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "old.xlsx")
	snapshot, err := NewMoneySnapshot("site", `{"QuotaPerUnit":"500000","USDExchangeRate":"7.2","general_setting.quota_display_type":"CNY"}`, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	job := Job{UsageVersion: 3, MoneySnapshot: snapshot}
	original, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	err = WriteUserDailyWorkbook(original, job, UserDailyFile{}, []RequestDetail{{CreatedUnix: time.Now().Unix(), RequestID: "=1+1", ModelName: "m", TokenName: "t", CompletionTokens: 0, MultimediaUsage: MultimediaUsage{ImageInputTokens: 513, ImageOutputTokens: 7, AudioInputTokens: 31, AudioOutputTokens: 11}, EmptyOutput: true, Charge: LogCharge{Total: "1.25", UnitPrices: UnitPrices{"输入|2": true, "输出|8": true}}}})
	original.Close()
	if err != nil {
		t.Fatal(err)
	}
	saved, err := os.Create(filepath.Join(dir, "saved.zip"))
	if err != nil {
		t.Fatal(err)
	}
	err = ConvertSettlementDetails(context.Background(), saved, path, "CNY", nil)
	saved.Close()
	if err != nil {
		t.Fatal(err)
	}
	reader, err := OpenSettlementDetails(filepath.Join(dir, "saved.zip"))
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	rows, _, err := reader.Page(context.Background(), SettlementDetailFilter{}, 0, 100)
	if err != nil || len(rows) != 1 || rows[0].UnitPrice != "输入 14.4；输出 57.6" || rows[0].Amount != "9.000000000000" || rows[0].Output != "0" || rows[0].ImageInput != "513" || rows[0].ImageOutput != "7" || rows[0].AudioInput != "31" || rows[0].AudioOutput != "11" {
		t.Fatal("reconverted currency or removed empty output", rows, err)
	}
	var output bytes.Buffer
	if _, err = reader.Export(context.Background(), &output, job, SettlementDetailFilter{}, nil); err != nil {
		t.Fatal(err)
	}
	z, _ := zip.NewReader(bytes.NewReader(output.Bytes()), int64(output.Len()))
	r, _ := z.File[0].Open()
	data, _ := io.ReadAll(r)
	r.Close()
	book, _ := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	sheet, _ := book.Open("xl/worksheets/sheet1.xml")
	text, _ := io.ReadAll(sheet)
	sheet.Close()
	if strings.Contains(string(text), "<f>") || !strings.Contains(string(text), "=1+1") || strings.Contains(string(text), "空输出") {
		t.Fatal("unexpected formula or diagnostics")
	}
}
func TestSettlementDailyGeneratorSeparatesSummaryAndDetails(t *testing.T) {
	for _, useSpool := range []bool{false, true} {
		day := time.Date(2026, 9, 11, 0, 0, 0, 0, BusinessLocation)
		job := Job{ID: "0123456789abcdef0123456789abcdef", InstanceID: "site", JobType: "user_statement", UserID: 7, UsageVersion: 3, BillPeriod: "daily", From: day, To: day.AddDate(0, 0, 1)}
		row := RequestDetail{JobID: job.ID, InstanceID: job.InstanceID, BillDay: day, CreatedUnix: day.Unix(), SourceLogID: 1, UserID: 7, RequestID: "detail-only", ModelName: "model", TokenID: 8, TokenName: "prod", Charge: LogCharge{Total: "1.25", MatchedTier: "高峰时段"}}
		store := &dailyFileStoreStub{}
		root := t.TempDir()
		g := UserDailyFileGenerator{Store: store, Root: root}
		if useSpool {
			spool := FileDetailSpool{Root: filepath.Join(root, "spool")}
			if err := spool.WritePage(context.Background(), job, JobStep{StepNo: 0}, LogCursor{CreatedUnix: row.CreatedUnix, ID: 1}, []RequestDetail{row}); err != nil {
				t.Fatal(err)
			}
			g.Spool = spool
		} else {
			store.groups = []UserDailyFile{{BillDay: day, UserID: 7}}
			store.details = []RequestDetail{row}
		}
		if err := g.GenerateJobFiles(context.Background(), job); err != nil {
			t.Fatal(err)
		}
		if len(store.tiers) != 1 || store.tiers[0].Tier != "高峰时段" || store.tiers[0].Amount != "1.250000000000" {
			t.Fatalf("saved tiers: %+v", store.tiers)
		}
		path := filepath.Join(root, store.files[0].RelativePath)
		text := workbookText(t, path)
		if strings.Contains(text, "detail-only") || strings.Contains(text, "账单明细") || !strings.Contains(text, "按模型统计") {
			t.Fatal("summary still includes request detail")
		}
		reader, err := OpenSettlementDetails(path + ".details.zip")
		if err != nil {
			t.Fatal(err)
		}
		rows, _, err := reader.Page(context.Background(), SettlementDetailFilter{}, 0, 100)
		reader.Close()
		if err != nil || len(rows) != 1 || rows[0].RequestID != "detail-only" {
			t.Fatal(rows, err)
		}
	}
}

func TestOldSettlementDetailMediaUnknown(t *testing.T) {
	cells := detailCells(SettlementDetailRow{Input: "10", Amount: "1"})
	for _, c := range cells[9:13] {
		if c.Value != "0" || !c.Number {
			t.Fatalf("old missing media became zero: %+v", c)
		}
	}
}

func TestSettlementDetailUsageBeforePrices(t *testing.T) {
	h := detailHeaders("CNY")
	cells := detailCells(SettlementDetailRow{ImageInput: "513", ImageOutput: "7", AudioInput: "31", AudioOutput: "11", BeforeAmount: "2", Discount: "0.5", Amount: "1"})
	want := []string{"513", "7", "31", "11", "未记录", "2", "5 折", "1"}
	for i, v := range want {
		if cells[i+9].Value != v {
			t.Fatalf("column %s value %s", h[i+9], cells[i+9].Value)
		}
	}
	if h[9] != "图像输入 Token" || h[14] != "原价金额 CNY" || h[13] != UnitPriceHeader("CNY") {
		t.Fatal(h)
	}
	old := []string{"时间", "渠道", "原价金额 CNY", "折扣", "折后金额 CNY", "图像输入 Token", "图像输出 Token", "音频输入 Token", "音频输出 Token"}
	order := settlementDisplayOrder(old, true)
	expected := []int{0, 5, 6, 7, 8, 2, 3, 4}
	for i, v := range expected {
		if order[i] != v {
			t.Fatal(order)
		}
	}
}
