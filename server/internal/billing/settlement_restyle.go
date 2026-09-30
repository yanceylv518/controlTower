package billing

import (
	"archive/zip"
	"context"
	"controltower/server/internal/xlsxwriter"
	"encoding/xml"
	"fmt"
	"io"
	"strings"
)

// RestyleSettlementDailyFile changes presentation only. It streams the saved
// numeric values, rather than re-reading orders or repricing an issued bill.
func RestyleSettlementDailyFile(ctx context.Context, out io.Writer, in io.ReaderAt, size int64, job Job) error {
	z, err := zip.NewReader(in, size)
	if err != nil {
		return err
	}
	var book struct {
		Sheets []struct {
			Name string `xml:"name,attr"`
		} `xml:"sheets>sheet"`
	}
	r, err := z.Open("xl/workbook.xml")
	if err != nil {
		return err
	}
	err = xml.NewDecoder(r).Decode(&book)
	r.Close()
	if err != nil {
		return err
	}
	wb := xlsxwriter.New()
	for i, meta := range book.Sheets {
		widths := []float64{48, 14, 17, 17, 17, 17, 18, 18, 18, 18, 20, 16, 20}
		if strings.HasPrefix(meta.Name, "账单明细") {
			widths = []float64{24, 48, 34, 30, 15, 15, 15, 15, 18, 18, 18, 18, 20, 16, 20}
		}
		sheet, err := wb.AddReportSheet(meta.Name, meta.Name, SettlementSheetMetadata(job), widths)
		if err != nil {
			return err
		}
		input, err := z.Open(fmt.Sprintf("xl/worksheets/sheet%d.xml", i+1))
		if err != nil {
			return err
		}
		err = copySettlementRows(ctx, sheet, input, job.JobType == "user_statement")
		input.Close()
		if err != nil {
			return err
		}
	}
	return wb.Write(out)
}
func copySettlementRows(ctx context.Context, sheet *xlsxwriter.Sheet, input io.Reader, hideChannel bool) error {
	decoder := xml.NewDecoder(input)
	started := false
	var order []int
	discountColumn := -1
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		token, err := decoder.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}
		start, ok := token.(xml.StartElement)
		if !ok || start.Name.Local != "row" {
			continue
		}
		var row struct {
			Cells []struct {
				Type    string `xml:"t,attr"`
				Value   string `xml:"v"`
				Text    string `xml:"is>t"`
				Formula string `xml:"f"`
			} `xml:"c"`
		}
		if err = decoder.DecodeElement(&row, &start); err != nil {
			return err
		}
		if len(row.Cells) == 0 {
			continue
		}
		if !started {
			first := row.Cells[0].Text
			if first != "统计对象" && first != "时间" {
				continue
			}
			started = true
			headers := make([]string, len(row.Cells))
			for i, c := range row.Cells {
				headers[i] = c.Text
				if c.Text == "折扣" {
					discountColumn = i
				}
			}
			order = settlementDisplayOrder(headers, hideChannel)
		}
		cells := make([]xlsxwriter.Cell, len(row.Cells))
		for i, c := range row.Cells {
			value := c.Text
			number := c.Type == "" && c.Value != ""
			if number {
				value = c.Value
			}
			cells[i] = xlsxwriter.Cell{Value: value, Number: number, Formula: c.Formula}
		}
		if discountColumn >= 0 && discountColumn < len(cells) {
			value := strings.TrimSpace(cells[discountColumn].Value)
			if value == "" || value == "—" {
				cells[discountColumn] = xlsxwriter.Cell{Value: "原价"}
			}
		}
		ordered := make([]xlsxwriter.Cell, 0, len(order))
		for _, i := range order {
			if i >= len(cells) {
				return fmt.Errorf("settlement row columns mismatch")
			}
			ordered = append(ordered, cells[i])
		}
		if err = sheet.Row(ordered); err != nil {
			return err
		}
	}
	if !started {
		return fmt.Errorf("settlement file header missing")
	}
	return nil
}

// Use saved headers so previously generated files can adopt the column layout
// without recalculating or changing any recorded amounts.
func settlementDisplayOrder(headers []string, hideChannel bool) []int {
	media := []int{}
	for i, h := range headers {
		if h == "图像输入 Token" || h == "图像输出 Token" || h == "音频输入 Token" || h == "音频输出 Token" {
			media = append(media, i)
		}
	}
	out := []int{}
	inserted := false
	for i, h := range headers {
		if hideChannel && (h == "渠道" || h == "渠道 ID") {
			continue
		}
		isMedia := false
		for _, m := range media {
			if i == m {
				isMedia = true
			}
		}
		if isMedia {
			continue
		}
		if !inserted && strings.HasPrefix(h, "原价金额") {
			out = append(out, media...)
			inserted = true
		}
		out = append(out, i)
	}
	if !inserted {
		out = append(out, media...)
	}
	return out
}
