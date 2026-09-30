package xlsxwriter

import (
	"archive/zip"
	"fmt"
	"html"
	"io"
	"math/big"
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

// WrappedTextStyle keeps multiline historical rules readable in exported files.
const WrappedTextStyle = 11

type Sheet struct {
	name           string
	file           *os.File
	row            int
	closed         bool
	merges         []string
	report         bool
	columns        int
	headerRow      int
	reconciliation bool
	moneyColumns   map[int]bool
	priceColumns   map[int]bool
	widths         []float64
	totals         map[int]*big.Rat
	unknownTotals  map[int]bool
	writingTotal   bool
	filterEnd      int
}
type Workbook struct{ sheets []*Sheet }

func New() *Workbook { return &Workbook{} }
func (w *Workbook) AddSheet(name string, widths []float64) (*Sheet, error) {
	return w.addSheet(name, widths, false)
}
func (w *Workbook) addSheet(name string, widths []float64, report bool) (*Sheet, error) {
	return w.addSheetAt(name, widths, report, 4)
}
func (w *Workbook) addSheetAt(name string, widths []float64, report bool, headerRow int) (*Sheet, error) {
	f, e := os.CreateTemp("", "ct-xlsx-*.xml")
	if e != nil {
		return nil, e
	}
	s := &Sheet{name: name, file: f, report: report, columns: len(widths), headerRow: headerRow, reconciliation: headerRow == 5, moneyColumns: map[int]bool{}, priceColumns: map[int]bool{}, widths: widths, totals: map[int]*big.Rat{}, unknownTotals: map[int]bool{}}
	w.sheets = append(w.sheets, s)
	io.WriteString(f, `<?xml version="1.0" encoding="UTF-8" standalone="yes"?><worksheet xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main">`)
	if report {
		io.WriteString(f, `<sheetPr><pageSetUpPr fitToPage="1"/></sheetPr>`)
	}
	fmt.Fprintf(f, `<sheetViews><sheetView showGridLines="0" workbookViewId="0"><pane ySplit="%d" topLeftCell="A%d" activePane="bottomLeft" state="frozen"/></sheetView></sheetViews><cols>`, headerRow, headerRow+1)
	for i, v := range widths {
		fmt.Fprintf(f, `<col min="%d" max="%d" width="%.1f" customWidth="1"/>`, i+1, i+1, v)
	}
	io.WriteString(f, `</cols><sheetData>`)
	return s, nil
}
func (s *Sheet) Row(cells []Cell) error {
	s.row++
	if s.report {
		height := 30
		if s.reconciliation {
			if s.row == 1 {
				height = 32
			} else if s.row < s.headerRow {
				height = 25
			} else if s.row == s.headerRow {
				height = 34
			}
		} else if s.row == 1 {
			height = 32
		} else if s.row == 2 {
			height = 25
		} else if s.row == 3 {
			height = 8
		} else if s.row == 4 {
			height = 34
		}
		if s.row > s.headerRow {
			for i := range s.priceColumns {
				if i >= len(cells) || i >= len(s.widths) {
					continue
				}
				units := 0
				for _, r := range cells[i].Value {
					if r > 127 {
						units += 2
					} else {
						units++
					}
				}
				width := max(1, int(s.widths[i])-2)
				height = max(height, min(409, ((units+width-1)/width)*16+8))
			}
		}
		fmt.Fprintf(s.file, `<row r="%d" ht="%d" customHeight="1">`, s.row, height)
		for i := range cells {
			if s.row == s.headerRow {
				if strings.HasPrefix(cells[i].Value, "模型单价") {
					s.priceColumns[i] = true
				}
				if strings.Contains(cells[i].Value, "金额") {
					s.moneyColumns[i] = true
				}
				cells[i].Style = 14
				if s.reconciliation && !strings.HasPrefix(cells[i].Value, "模型单价") && (cells[i].Value == "请求数" || strings.Contains(cells[i].Value, "Token") || strings.Contains(cells[i].Value, "金额")) {
					s.totals[i] = new(big.Rat)
				}
			} else if s.row > s.headerRow && !s.writingTotal {
				if total, ok := s.totals[i]; ok {
					if value, valid := new(big.Rat).SetString(cells[i].Value); valid && cells[i].Number {
						total.Add(total, value)
					} else {
						s.unknownTotals[i] = true
					}
				}
				if cells[i].Number {
					if i == len(cells)-1 || (s.reconciliation && s.moneyColumns[i]) {
						cells[i].Style = 17
					} else {
						cells[i].Style = 16
					}
				} else {
					cells[i].Style = 15
					if s.moneyColumns[i] {
						cells[i].Style = 17
					}
				}
			}
		}
	} else {
		fmt.Fprintf(s.file, `<row r="%d">`, s.row)
	}
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
func (s *Sheet) Title(value string, columns int) error {
	cells := make([]Cell, columns)
	for i := range cells {
		cells[i].Style = 6
	}
	cells[0].Value = value
	s.merges = append(s.merges, "A1:"+column(columns)+"2")
	if err := s.Row(cells); err != nil {
		return err
	}
	if err := s.Row(nil); err != nil {
		return err
	}
	return s.Row(nil)
}
func (s *Sheet) Close() error {
	if s.closed {
		return nil
	}
	if s.reconciliation {
		s.filterEnd = s.row
		cells := make([]Cell, s.columns)
		for i := range cells {
			cells[i].Style = 18
		}
		cells[0].Value = "合计"
		for i, total := range s.totals {
			if s.unknownTotals[i] {
				cells[i].Value = "—"
				if s.moneyColumns[i] {
					cells[i].Style = 20
				}
				continue
			}
			digits, style := 0, 19
			if s.moneyColumns[i] {
				digits, style = 6, 20
			}
			cells[i] = Cell{Value: total.FloatString(digits), Number: true, Style: style}
			if s.row > s.headerRow {
				cells[i].Formula = fmt.Sprintf("SUM(%s%d:%s%d)", column(i+1), s.headerRow+1, column(i+1), s.row)
			}
		}
		s.writingTotal = true
		if err := s.Row(cells); err != nil {
			return err
		}
	}
	s.closed = true
	_, e := io.WriteString(s.file, `</sheetData>`)
	if e != nil {
		return e
	}
	if s.report && s.row >= s.headerRow {
		fmt.Fprintf(s.file, `<autoFilter ref="A%d:%s%d"/>`, s.headerRow, column(s.columns), func() int {
			if s.filterEnd > 0 {
				return s.filterEnd
			}
			return s.row
		}())
	}
	if len(s.merges) > 0 {
		fmt.Fprintf(s.file, `<mergeCells count="%d">`, len(s.merges))
		for _, ref := range s.merges {
			fmt.Fprintf(s.file, `<mergeCell ref="%s"/>`, ref)
		}
		io.WriteString(s.file, `</mergeCells>`)
	}
	if s.report {
		io.WriteString(s.file, `<pageMargins left="0.25" right="0.25" top="0.4" bottom="0.4" header="0.2" footer="0.2"/><pageSetup paperSize="8" orientation="landscape" fitToWidth="1" fitToHeight="0"/>`)
	}
	_, e = io.WriteString(s.file, `</worksheet>`)
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
	b.WriteString(`</sheets>`)
	hasReport := false
	for _, s := range ss {
		if s.report {
			hasReport = true
		}
	}
	if hasReport {
		b.WriteString(`<definedNames>`)
		for i, s := range ss {
			if s.report {
				fmt.Fprintf(&b, `<definedName name="_xlnm.Print_Titles" localSheetId="%d">%s!$1:$%d</definedName>`, i, html.EscapeString("'"+strings.ReplaceAll(s.name, "'", "''")+"'"), s.headerRow)
			}
		}
		b.WriteString(`</definedNames>`)
	}
	b.WriteString(`</workbook>`)
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

const stylesXML = `<?xml version="1.0" encoding="UTF-8"?><styleSheet xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main"><numFmts count="4"><numFmt numFmtId="167" formatCode="#,##0.000000"/><numFmt numFmtId="166" formatCode="#,##0.00####"/><numFmt numFmtId="164" formatCode="#,##0"/><numFmt numFmtId="165" formatCode="0.000000"/></numFmts><fonts count="5"><font><sz val="11"/><name val="Microsoft YaHei"/></font><font><b/><color rgb="FFFFFFFF"/><sz val="11"/><name val="Microsoft YaHei"/></font><font><b/><sz val="14"/><name val="Microsoft YaHei"/></font><font><b/><sz val="11"/><name val="Microsoft YaHei"/></font><font><b/><color rgb="FFFF0000"/><sz val="12"/><name val="Microsoft YaHei"/></font></fonts><fills count="7"><fill><patternFill patternType="none"/></fill><fill><patternFill patternType="gray125"/></fill><fill><patternFill patternType="solid"><fgColor rgb="FF5B9BD5"/></patternFill></fill><fill><patternFill patternType="solid"><fgColor rgb="FFDDEBF7"/></patternFill></fill><fill><patternFill patternType="solid"><fgColor rgb="FFFFF2CC"/></patternFill></fill><fill><patternFill patternType="solid"><fgColor rgb="FFFFD966"/></patternFill></fill><fill><patternFill patternType="solid"><fgColor rgb="FF244A64"/></patternFill></fill></fills><borders count="3"><border/><border><left style="thin"/><right style="thin"/><top style="thin"/><bottom style="thin"/></border><border><bottom style="hair"><color rgb="FFDCE5ED"/></bottom></border></borders><cellXfs count="21"><xf numFmtId="0" fontId="0" fillId="0" borderId="0"/><xf numFmtId="0" fontId="1" fillId="2" borderId="1" applyAlignment="1"><alignment horizontal="center" vertical="center" wrapText="1"/></xf><xf numFmtId="0" fontId="2" fillId="3" borderId="0"/><xf numFmtId="164" fontId="0" fillId="0" borderId="1"/><xf numFmtId="165" fontId="0" fillId="0" borderId="1"/><xf numFmtId="0" fontId="0" fillId="0" borderId="1"/><xf numFmtId="0" fontId="2" fillId="3" borderId="1" applyAlignment="1"><alignment horizontal="center" vertical="center"/></xf><xf numFmtId="0" fontId="3" fillId="4" borderId="1" applyAlignment="1"><alignment horizontal="left" vertical="center"/></xf><xf numFmtId="164" fontId="3" fillId="4" borderId="1" applyAlignment="1"><alignment horizontal="right" vertical="center"/></xf><xf numFmtId="165" fontId="3" fillId="4" borderId="1" applyAlignment="1"><alignment horizontal="right" vertical="center"/></xf><xf numFmtId="165" fontId="4" fillId="5" borderId="1" applyAlignment="1"><alignment horizontal="right" vertical="center"/></xf><xf numFmtId="0" fontId="0" fillId="0" borderId="1" applyAlignment="1"><alignment horizontal="left" vertical="top" wrapText="1"/></xf><xf numFmtId="0" fontId="2" fillId="0" borderId="0" applyAlignment="1"><alignment horizontal="left" vertical="center"/></xf><xf numFmtId="0" fontId="0" fillId="0" borderId="0" applyAlignment="1"><alignment horizontal="left" vertical="center"/></xf><xf numFmtId="0" fontId="1" fillId="6" borderId="0" applyAlignment="1"><alignment horizontal="center" vertical="center" wrapText="1"/></xf><xf numFmtId="0" fontId="0" fillId="0" borderId="2" applyAlignment="1"><alignment horizontal="left" vertical="center" wrapText="1"/></xf><xf numFmtId="164" fontId="0" fillId="0" borderId="2" applyAlignment="1"><alignment horizontal="right" vertical="center"/></xf><xf numFmtId="167" fontId="0" fillId="0" borderId="2" applyNumberFormat="1" applyAlignment="1"><alignment horizontal="right" vertical="center"/></xf><xf numFmtId="0" fontId="3" fillId="3" borderId="2" applyAlignment="1"><alignment vertical="center"/></xf><xf numFmtId="164" fontId="3" fillId="3" borderId="2" applyAlignment="1"><alignment horizontal="right" vertical="center"/></xf><xf numFmtId="167" fontId="3" fillId="3" borderId="2" applyAlignment="1"><alignment horizontal="right" vertical="center"/></xf></cellXfs></styleSheet>`

// AddReportSheet keeps statement exports consistent without changing legacy sheets.
func (w *Workbook) AddReportSheet(name, title, metadata string, widths []float64) (*Sheet, error) {
	s, err := w.addSheet(name, widths, true)
	if err != nil {
		return nil, err
	}
	s.report = true
	s.columns = len(widths)
	s.merges = append(s.merges, "A1:"+column(len(widths))+"1", "A2:"+column(len(widths))+"2")
	if err = s.Row([]Cell{{Value: title, Style: 12}}); err != nil {
		return nil, err
	}
	if err = s.Row([]Cell{{Value: metadata, Style: 13}}); err != nil {
		return nil, err
	}
	if err = s.Row(nil); err != nil {
		return nil, err
	}
	return s, nil
}

// Discard releases temporary sheets when an export is cancelled or fails.
func (w *Workbook) Discard() {
	for _, s := range w.sheets {
		_ = s.file.Close()
		_ = os.Remove(s.file.Name())
	}
}

// AddReconciliationSheet matches the monthly customer statement's four-line masthead.
func (w *Workbook) AddReconciliationSheet(name, title, customer, issuer, period, site string, widths []float64) (*Sheet, error) {
	s, e := w.addSheetAt(name, widths, true, 5)
	if e != nil {
		return nil, e
	}
	last := column(len(widths))
	s.merges = append(s.merges, "A1:"+last+"1", "A2:"+last+"2", "A3:"+last+"3", "A4:B4", "C4:"+last+"4")
	for _, row := range [][]Cell{{{Value: title, Style: 12}}, {{Value: "客户(甲方)：" + customer, Style: 13}}, {{Value: "出账方(乙方)：" + issuer, Style: 13}}, {{Value: "账期：" + period, Style: 13}, {}, {Value: "服务站点：" + site, Style: 13}}} {
		if e = s.Row(row); e != nil {
			return nil, e
		}
	}
	return s, nil
}
