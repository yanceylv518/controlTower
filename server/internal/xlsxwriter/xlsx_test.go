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
