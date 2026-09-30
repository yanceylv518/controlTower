package xlsxwriter

import (
	"archive/zip"
	"bytes"
	"encoding/xml"
	"io"
	"strings"
	"testing"
)

func TestWorkbookProducesReadableOpenXML(t *testing.T) {
	w := New()
	s, e := w.AddSheet("账单概览", []float64{12, 20})
	if e != nil {
		t.Fatal(e)
	}
	if e = s.Row([]Cell{{Value: "模型", Style: 1}, {Value: "金额", Style: 1}}); e != nil {
		t.Fatal(e)
	}
	_ = s.Row([]Cell{{Value: "glm-5.1", Style: 5}, {Value: "1.250000", Number: true, Style: 4}})
	var out bytes.Buffer
	if e = w.Write(&out); e != nil {
		t.Fatal(e)
	}
	r, e := zip.NewReader(bytes.NewReader(out.Bytes()), int64(out.Len()))
	if e != nil {
		t.Fatal(e)
	}
	wanted := map[string]bool{"[Content_Types].xml": false, "xl/workbook.xml": false, "xl/worksheets/sheet1.xml": false}
	for _, f := range r.File {
		if _, ok := wanted[f.Name]; !ok {
			continue
		}
		src, _ := f.Open()
		raw, _ := io.ReadAll(src)
		src.Close()
		var root struct{ XMLName xml.Name }
		if e := xml.Unmarshal(raw, &root); e != nil {
			t.Fatalf("%s is invalid XML: %v", f.Name, e)
		}
		wanted[f.Name] = true
		if f.Name == "xl/workbook.xml" && !strings.Contains(string(raw), "账单概览") {
			t.Fatal("sheet name missing")
		}
	}
	for name, ok := range wanted {
		if !ok {
			t.Fatalf("missing %s", name)
		}
	}
}
func TestReconciliationMasthead(t *testing.T) {
	w := New()
	defer w.Discard()
	s, e := w.AddReconciliationSheet("月统计", "2026年8月对账汇总单", "客户甲", "", "2026-08-01 至 2026-08-31", "site-a", []float64{16, 36, 16, 20})
	if e != nil {
		t.Fatal(e)
	}
	if e = s.Row([]Cell{{Value: "日期"}, {Value: "模型"}, {Value: "原价金额 CNY"}, {Value: "折后金额 CNY"}}); e != nil {
		t.Fatal(e)
	}
	if e = s.Row([]Cell{{Value: "2026-08"}, {Value: "model"}, {Value: "12.3456", Number: true}, {Value: "6.1728", Number: true}}); e != nil {
		t.Fatal(e)
	}
	var out bytes.Buffer
	if e = w.Write(&out); e != nil {
		t.Fatal(e)
	}
	z, e := zip.NewReader(bytes.NewReader(out.Bytes()), int64(out.Len()))
	if e != nil {
		t.Fatal(e)
	}
	for _, f := range z.File {
		if f.Name != "xl/worksheets/sheet1.xml" && f.Name != "xl/workbook.xml" {
			continue
		}
		r, _ := f.Open()
		b, _ := io.ReadAll(r)
		r.Close()
		text := string(b)
		if f.Name == "xl/workbook.xml" {
			if !strings.Contains(text, "$1:$5") {
				t.Fatal("print header incorrect")
			}
			continue
		}
		for _, want := range []string{`r="C7" s="20"><f>SUM(C6:C6)</f><v>12.345600</v>`, `r="D7" s="20"><f>SUM(D6:D6)</f><v>6.172800</v>`, "合计", `ySplit="5"`, `topLeftCell="A6"`, `autoFilter ref="A5:D6"`, `r="C6" s="17"`, `r="A5" t="inlineStr" s="14"`, "客户(甲方)：客户甲", "出账方(乙方)：", "服务站点：site-a", "2026年8月对账汇总单"} {
			if !strings.Contains(text, want) {
				t.Fatalf("missing %s", want)
			}
		}
	}
}

func TestReportPriceTextIsWrappedButNotSummed(t *testing.T) {
	w := New()
	s, err := w.AddReconciliationSheet("统计", "单价", "", "", "", "", []float64{20, 48, 20})
	if err != nil {
		t.Fatal(err)
	}
	if err = s.Row([]Cell{{Value: "模型"}, {Value: "模型单价（CNY/百万 Token；按次另标）"}, {Value: "折后金额"}}); err != nil {
		t.Fatal(err)
	}
	text := "输入 2 / 3；输出 8 / 12；缓存读取 0.2 / 0.3；缓存写入 1 / 2；1h写入 4 / 5"
	if err = s.Row([]Cell{{Value: "m"}, {Value: text}, {Value: "5", Number: true}}); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if err = w.Write(&out); err != nil {
		t.Fatal(err)
	}
	z, err := zip.NewReader(bytes.NewReader(out.Bytes()), int64(out.Len()))
	if err != nil {
		t.Fatal(err)
	}
	r, err := z.Open("xl/worksheets/sheet1.xml")
	if err != nil {
		t.Fatal(err)
	}
	raw, err := io.ReadAll(r)
	r.Close()
	if err != nil {
		t.Fatal(err)
	}
	xml := string(raw)
	if strings.Contains(xml, "SUM(B") || !strings.Contains(xml, "SUM(C6:C6)") || !strings.Contains(xml, text) || strings.Contains(xml, `<row r="6" ht="30"`) {
		t.Fatal(xml)
	}
}
