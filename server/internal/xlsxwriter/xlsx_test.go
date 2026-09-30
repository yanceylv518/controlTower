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

func TestReportMoneyColumnsKeepFractionalAmounts(t *testing.T) {
	for _, reconciliation := range []bool{false, true} {
		name := "report"
		if reconciliation {
			name = "reconciliation"
		}
		t.Run(name, func(t *testing.T) {
			w := New()
			defer w.Discard()
			widths := []float64{16, 20, 16, 20}
			var sheet *Sheet
			var err error
			if reconciliation {
				sheet, err = w.AddReconciliationSheet("金额", "金额", "", "", "", "", widths)
			} else {
				sheet, err = w.AddReportSheet("金额", "金额", "", widths)
			}
			if err != nil {
				t.Fatal(err)
			}
			if err = sheet.Row([]Cell{{Value: "请求数"}, {Value: "原价金额 CNY"}, {Value: "折扣"}, {Value: "折后金额 CNY"}}); err != nil {
				t.Fatal(err)
			}
			rows := [][]Cell{
				{{Value: "1", Number: true}, {Value: "0.026696", Number: true}, {Value: "4.6 折"}, {Value: "0.012280", Number: true}},
				{{Value: "2", Number: true}, {Value: "1234.567890", Number: true}, {Value: "原价"}, {Value: "1234.567890", Number: true}},
				{{Value: "1", Number: true}, {Value: "0", Number: true}, {Value: "原价"}, {Value: "0", Number: true}},
				{{Value: "1", Number: true}, {Value: "—"}, {Value: "4.6 折"}, {Value: "0.012280", Number: true}},
			}
			for _, row := range rows {
				if err = sheet.Row(append([]Cell(nil), row...)); err != nil {
					t.Fatal(err)
				}
			}
			var out bytes.Buffer
			if err = w.Write(&out); err != nil {
				t.Fatal(err)
			}
			book, err := zip.NewReader(bytes.NewReader(out.Bytes()), int64(out.Len()))
			if err != nil {
				t.Fatal(err)
			}
			readXML := func(path string, value any) {
				t.Helper()
				file, err := book.Open(path)
				if err != nil {
					t.Fatal(err)
				}
				defer file.Close()
				if err = xml.NewDecoder(file).Decode(value); err != nil {
					t.Fatal(err)
				}
			}
			var styles struct {
				Formats []struct {
					ID   int    `xml:"numFmtId,attr"`
					Code string `xml:"formatCode,attr"`
				} `xml:"numFmts>numFmt"`
				Cells []struct {
					ID int `xml:"numFmtId,attr"`
				} `xml:"cellXfs>xf"`
			}
			readXML("xl/styles.xml", &styles)
			formats := map[int]string{}
			for _, f := range styles.Formats {
				formats[f.ID] = f.Code
			}
			var data struct {
				Rows []struct {
					Cells []struct {
						Style int    `xml:"s,attr"`
						Value string `xml:"v"`
						Text  string `xml:"is>t"`
					} `xml:"c"`
				} `xml:"sheetData>row"`
			}
			readXML("xl/worksheets/sheet1.xml", &data)
			for i, row := range rows {
				actual := data.Rows[sheet.headerRow+i].Cells
				for j, want := range row {
					got := actual[j]
					if !want.Number {
						if got.Text != want.Value || got.Value != "" {
							t.Fatalf("text changed: %+v", got)
						}
						continue
					}
					if got.Value != want.Value {
						t.Fatalf("amount/count changed: got %s, want %s", got.Value, want.Value)
					}
					format := "#,##0.000000"
					if j == 0 {
						format = "#,##0"
					}
					if code := formats[styles.Cells[got.Style].ID]; code != format {
						t.Errorf("row %d column %d value %s: format %q, want %q", i, j, got.Value, code, format)
					}
				}
			}
		})
	}
}
