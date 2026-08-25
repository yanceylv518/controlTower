package xlsxwriter

import (
	"archive/zip"
	"fmt"
	"html"
	"io"
	"os"
	"strconv"
	"strings"
)

type Cell struct {
	Value   string
	Number  bool
	Style   int
	Formula string
}
type Sheet struct {
	name   string
	file   *os.File
	row    int
	closed bool
}
type Workbook struct{ sheets []*Sheet }

func New() *Workbook { return &Workbook{} }
func (w *Workbook) AddSheet(name string, widths []float64) (*Sheet, error) {
	f, e := os.CreateTemp("", "ct-xlsx-*.xml")
	if e != nil {
		return nil, e
	}
	s := &Sheet{name: name, file: f}
	w.sheets = append(w.sheets, s)
	io.WriteString(f, `<?xml version="1.0" encoding="UTF-8" standalone="yes"?><worksheet xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main"><sheetViews><sheetView showGridLines="0" workbookViewId="0"><pane ySplit="4" topLeftCell="A5" activePane="bottomLeft" state="frozen"/></sheetView></sheetViews><cols>`)
	for i, v := range widths {
		fmt.Fprintf(f, `<col min="%d" max="%d" width="%.1f" customWidth="1"/>`, i+1, i+1, v)
	}
	io.WriteString(f, `</cols><sheetData>`)
	return s, nil
}
func (s *Sheet) Row(cells []Cell) error {
	s.row++
	fmt.Fprintf(s.file, `<row r="%d">`, s.row)
	for i, c := range cells {
		ref := column(i+1) + strconv.Itoa(s.row)
		style := ""
		if c.Style > 0 {
			style = ` s="` + strconv.Itoa(c.Style) + `"`
		}
		if c.Formula != "" {
			fmt.Fprintf(s.file, `<c r="%s"%s><f>%s</f><v>%s</v></c>`, ref, style, html.EscapeString(c.Formula), html.EscapeString(c.Value))
		} else if c.Number && c.Value != "" {
			fmt.Fprintf(s.file, `<c r="%s"%s><v>%s</v></c>`, ref, style, html.EscapeString(c.Value))
		} else {
			fmt.Fprintf(s.file, `<c r="%s" t="inlineStr"%s><is><t xml:space="preserve">%s</t></is></c>`, ref, style, html.EscapeString(c.Value))
		}
	}
	_, e := io.WriteString(s.file, `</row>`)
	return e
}
func (s *Sheet) Close() error {
	if s.closed {
		return nil
	}
	s.closed = true
	_, e := io.WriteString(s.file, `</sheetData></worksheet>`)
	if e != nil {
		return e
	}
	return s.file.Close()
}
func (w *Workbook) Write(out io.Writer) error {
	for _, s := range w.sheets {
		if e := s.Close(); e != nil {
			return e
		}
		defer os.Remove(s.file.Name())
	}
	z := zip.NewWriter(out)
	add := func(name, body string) error {
		f, e := z.Create(name)
		if e == nil {
			_, e = io.WriteString(f, body)
		}
		return e
	}
	if e := add("[Content_Types].xml", contentTypes(len(w.sheets))); e != nil {
		return e
	}
	add("_rels/.rels", `<?xml version="1.0" encoding="UTF-8"?><Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships"><Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/officeDocument" Target="xl/workbook.xml"/></Relationships>`)
	add("xl/workbook.xml", workbookXML(w.sheets))
	add("xl/_rels/workbook.xml.rels", workbookRels(len(w.sheets)))
	add("xl/styles.xml", stylesXML)
	for i, s := range w.sheets {
		src, e := os.Open(s.file.Name())
		if e != nil {
			return e
		}
		dst, e := z.Create(fmt.Sprintf("xl/worksheets/sheet%d.xml", i+1))
		if e == nil {
			_, e = io.Copy(dst, src)
		}
		src.Close()
		if e != nil {
			return e
		}
	}
	return z.Close()
}
func column(n int) string {
	var b strings.Builder
	for n > 0 {
		n--
		b.WriteByte(byte('A' + n%26))
		n /= 26
	}
	r := []byte(b.String())
	for i, j := 0, len(r)-1; i < j; i, j = i+1, j-1 {
		r[i], r[j] = r[j], r[i]
	}
	return string(r)
}
func contentTypes(n int) string {
	var b strings.Builder
	b.WriteString(`<?xml version="1.0" encoding="UTF-8"?><Types xmlns="http://schemas.openxmlformats.org/package/2006/content-types"><Default Extension="rels" ContentType="application/vnd.openxmlformats-package.relationships+xml"/><Default Extension="xml" ContentType="application/xml"/><Override PartName="/xl/workbook.xml" ContentType="application/vnd.openxmlformats-officedocument.spreadsheetml.sheet.main+xml"/><Override PartName="/xl/styles.xml" ContentType="application/vnd.openxmlformats-officedocument.spreadsheetml.styles+xml"/>`)
	for i := 1; i <= n; i++ {
		fmt.Fprintf(&b, `<Override PartName="/xl/worksheets/sheet%d.xml" ContentType="application/vnd.openxmlformats-officedocument.spreadsheetml.worksheet+xml"/>`, i)
	}
	b.WriteString(`</Types>`)
	return b.String()
}
func workbookXML(ss []*Sheet) string {
	var b strings.Builder
	b.WriteString(`<?xml version="1.0" encoding="UTF-8"?><workbook xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main" xmlns:r="http://schemas.openxmlformats.org/officeDocument/2006/relationships"><sheets>`)
	for i, s := range ss {
		fmt.Fprintf(&b, `<sheet name="%s" sheetId="%d" r:id="rId%d"/>`, html.EscapeString(s.name), i+1, i+1)
	}
	b.WriteString(`</sheets></workbook>`)
	return b.String()
}
func workbookRels(n int) string {
	var b strings.Builder
	b.WriteString(`<?xml version="1.0" encoding="UTF-8"?><Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships">`)
	for i := 1; i <= n; i++ {
		fmt.Fprintf(&b, `<Relationship Id="rId%d" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/worksheet" Target="worksheets/sheet%d.xml"/>`, i, i)
	}
	fmt.Fprintf(&b, `<Relationship Id="rId%d" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/styles" Target="styles.xml"/></Relationships>`, n+1)
	return b.String()
}

const stylesXML = `<?xml version="1.0" encoding="UTF-8"?><styleSheet xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main"><numFmts count="2"><numFmt numFmtId="164" formatCode="#,##0"/><numFmt numFmtId="165" formatCode="0.000000"/></numFmts><fonts count="3"><font><sz val="11"/><name val="Microsoft YaHei"/></font><font><b/><color rgb="FFFFFFFF"/><sz val="11"/><name val="Microsoft YaHei"/></font><font><b/><sz val="14"/><name val="Microsoft YaHei"/></font></fonts><fills count="4"><fill><patternFill patternType="none"/></fill><fill><patternFill patternType="gray125"/></fill><fill><patternFill patternType="solid"><fgColor rgb="FF5B9BD5"/></patternFill></fill><fill><patternFill patternType="solid"><fgColor rgb="FFDDEBF7"/></patternFill></fill></fills><borders count="2"><border/><border><left style="thin"/><right style="thin"/><top style="thin"/><bottom style="thin"/></border></borders><cellXfs count="6"><xf numFmtId="0" fontId="0" fillId="0" borderId="0"/><xf numFmtId="0" fontId="1" fillId="2" borderId="1" applyAlignment="1"><alignment horizontal="center" vertical="center" wrapText="1"/></xf><xf numFmtId="0" fontId="2" fillId="3" borderId="0"/><xf numFmtId="164" fontId="0" fillId="0" borderId="1"/><xf numFmtId="165" fontId="0" fillId="0" borderId="1"/><xf numFmtId="0" fontId="0" fillId="0" borderId="1"/></cellXfs></styleSheet>`
