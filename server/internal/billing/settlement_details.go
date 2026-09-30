package billing

import (
	"archive/zip"
	"context"
	"controltower/server/internal/xlsxwriter"
	"encoding/json"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

const SettlementDetailPartRows = 50_000

// These are the exported bill columns, not a second copy of source-order data.
type SettlementDetailRow struct {
	UnitPrice   string `json:"unit_price"`
	ImageInput  string `json:"image_input_tokens"`
	ImageOutput string `json:"image_output_tokens"`
	AudioInput  string `json:"audio_input_tokens"`
	AudioOutput string `json:"audio_output_tokens"`

	BeforeAmount string `json:"before_amount"`
	Discount     string `json:"discount"`
	Time         string `json:"time"`
	RequestID    string `json:"request_id"`
	Model        string `json:"model"`
	Token        string `json:"token"`
	TokenID      string `json:"token_id"`
	Input        string `json:"input"`
	Output       string `json:"output"`
	CacheRead    string `json:"cache_read"`
	CacheWrite   string `json:"cache_write"`
	Amount       string `json:"amount"`
}
type SettlementDetailPart struct {
	Name   string          `json:"name"`
	Rows   int             `json:"rows"`
	First  string          `json:"first"`
	Last   string          `json:"last"`
	Models map[string]bool `json:"models"`
	Tokens map[string]bool `json:"tokens"`
}
type SettlementDetailManifest struct {
	Version  int                    `json:"version"`
	Total    int64                  `json:"total"`
	Currency string                 `json:"currency"`
	Parts    []SettlementDetailPart `json:"parts"`
	Models   []string               `json:"models"`
	Tokens   []string               `json:"tokens"`
}
type SettlementDetailFilter struct {
	From  string `json:"from"`
	To    string `json:"to"`
	Model string `json:"model"`
	Token string `json:"token"`
}

func (v SettlementDetailRow) TokenKey() string {
	if v.TokenID != "" && v.TokenID != "0" {
		return v.Token + " (#" + v.TokenID + ")"
	}
	return v.Token
}
func (f SettlementDetailFilter) Match(v SettlementDetailRow) bool {
	return (f.From == "" || v.Time >= f.From) && (f.To == "" || v.Time < f.To) && (f.Model == "" || v.Model == f.Model) && (f.Token == "" || v.TokenKey() == f.Token)
}
func (f SettlementDetailFilter) includes(p SettlementDetailPart) bool {
	return (f.From == "" || p.Last >= f.From) && (f.To == "" || p.First < f.To) && (f.Model == "" || p.Models[f.Model]) && (f.Token == "" || p.Tokens[f.Token])
}
func WriteSettlementDetailArchive(ctx context.Context, out io.Writer, currency string, iterate func(func(SettlementDetailRow) error) error, progress func(int64)) error {
	z := zip.NewWriter(out)
	m := SettlementDetailManifest{Version: 1, Currency: currency}
	models, tokens := map[string]bool{}, map[string]bool{}
	var encoder *json.Encoder
	var part *SettlementDetailPart
	bytes := 0
	err := iterate(func(v SettlementDetailRow) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		if part == nil || part.Rows >= SettlementDetailPartRows || bytes >= 16*1024*1024 {
			m.Parts = append(m.Parts, SettlementDetailPart{Name: fmt.Sprintf("part-%05d.jsonl", len(m.Parts)+1), Models: map[string]bool{}, Tokens: map[string]bool{}})
			part = &m.Parts[len(m.Parts)-1]
			entry, e := z.Create(part.Name)
			if e != nil {
				return e
			}
			encoder = json.NewEncoder(entry)
			bytes = 0
		}
		if err := encoder.Encode(v); err != nil {
			return err
		}
		bytes += len(v.Time) + len(v.RequestID) + len(v.Model) + len(v.Token) + len(v.Amount) + 160
		if part.Rows == 0 || v.Time < part.First {
			part.First = v.Time
		}
		if v.Time > part.Last {
			part.Last = v.Time
		}
		part.Rows++
		m.Total++
		part.Models[v.Model] = true
		part.Tokens[v.TokenKey()] = true
		models[v.Model] = true
		tokens[v.TokenKey()] = true
		if progress != nil && m.Total%256 == 0 {
			progress(m.Total)
		}
		return nil
	})
	if err != nil {
		z.Close()
		return err
	}
	for v := range models {
		m.Models = append(m.Models, v)
	}
	sort.Strings(m.Models)
	for v := range tokens {
		m.Tokens = append(m.Tokens, v)
	}
	sort.Strings(m.Tokens)
	entry, err := z.Create("manifest.json")
	if err != nil {
		z.Close()
		return err
	}
	if err = json.NewEncoder(entry).Encode(m); err != nil {
		z.Close()
		return err
	}
	if progress != nil {
		progress(m.Total)
	}
	return z.Close()
}
func WriteSettlementSavedDetails(ctx context.Context, path string, job Job, iterate func(func(RequestDetail) error) error) error {
	display, rate, err := SettlementDisplay(job.MoneySnapshot)
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".ct-detail-*.zip")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	defer tmp.Close()
	err = WriteSettlementDetailArchive(ctx, tmp, SettlementCurrencyLabel(display), func(visit func(SettlementDetailRow) error) error {
		return iterate(func(v RequestDetail) error {
			before, discount := ChargeOriginal(v.Charge)
			return visit(SettlementDetailRow{UnitPrice: UnitPriceLabel(v.Charge.UnitPrices, rate), ImageInput: strconv.FormatInt(v.ImageInputTokens, 10), ImageOutput: strconv.FormatInt(v.ImageOutputTokens, 10), AudioInput: strconv.FormatInt(v.AudioInputTokens, 10), AudioOutput: strconv.FormatInt(v.AudioOutputTokens, 10), BeforeAmount: DisplaySettlementAmount(before, rate), Discount: discount, Time: time.Unix(v.CreatedUnix, 0).In(BusinessLocation).Format("2006-01-02 15:04:05"), RequestID: v.RequestID, Model: v.ModelName, Token: v.TokenName, TokenID: strconv.FormatInt(v.TokenID, 10), Input: strconv.FormatInt(v.PromptTokens, 10), Output: strconv.FormatInt(v.CompletionTokens, 10), CacheRead: strconv.FormatInt(v.CacheReadTokens, 10), CacheWrite: strconv.FormatInt(v.CacheWriteTokens, 10), Amount: DisplaySettlementAmount(v.Charge.Total, rate)})
		})
	}, nil)
	if err != nil {
		return err
	}
	if err = tmp.Sync(); err != nil {
		return err
	}
	if err = tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}

type SettlementDetailReader struct {
	zip      *zip.ReadCloser
	Manifest SettlementDetailManifest
}

func OpenSettlementDetails(path string) (*SettlementDetailReader, error) {
	z, err := zip.OpenReader(path)
	if err != nil {
		return nil, err
	}
	r, err := z.Open("manifest.json")
	if err != nil {
		z.Close()
		return nil, err
	}
	d := &SettlementDetailReader{zip: z}
	err = json.NewDecoder(r).Decode(&d.Manifest)
	r.Close()
	if err != nil || d.Manifest.Version != 1 {
		z.Close()
		return nil, fmt.Errorf("invalid detail manifest: %v", err)
	}
	return d, nil
}
func (d *SettlementDetailReader) Close() error { return d.zip.Close() }

var errDetailPageFull = errors.New("detail page full")

// Cursor is a physical row offset. Skipped shards advance it without decompressing.
func (d *SettlementDetailReader) Visit(ctx context.Context, f SettlementDetailFilter, cursor int64, visit func(SettlementDetailRow, int64) error, progress func(int64)) error {
	var offset int64
	for _, p := range d.Manifest.Parts {
		if err := ctx.Err(); err != nil {
			return err
		}
		if offset+int64(p.Rows) <= cursor || !f.includes(p) {
			offset += int64(p.Rows)
			if progress != nil {
				progress(offset)
			}
			continue
		}
		file, err := d.zip.Open(p.Name)
		if err != nil {
			return err
		}
		dec := json.NewDecoder(file)
		for i := 0; i < p.Rows; i++ {
			if err = ctx.Err(); err != nil {
				file.Close()
				return err
			}
			var v SettlementDetailRow
			if err = dec.Decode(&v); err != nil {
				file.Close()
				return err
			}
			v.BeforeAmount, v.Discount = DefaultSettlementPrice(v.BeforeAmount, v.Discount, v.Amount)
			for _, value := range []*string{&v.ImageInput, &v.ImageOutput, &v.AudioInput, &v.AudioOutput} {
				if strings.TrimSpace(*value) == "" {
					*value = "0"
				}
			}
			offset++
			if offset > cursor && f.Match(v) {
				if err = visit(v, offset); err != nil {
					file.Close()
					return err
				}
			}
			if progress != nil && offset%256 == 0 {
				progress(offset)
			}
		}
		file.Close()
		if progress != nil {
			progress(offset)
		}
	}
	return nil
}
func (d *SettlementDetailReader) Page(ctx context.Context, f SettlementDetailFilter, cursor int64, limit int) ([]SettlementDetailRow, int64, error) {
	if limit < 1 || limit > 200 {
		return nil, 0, fmt.Errorf("invalid detail page size")
	}
	rows := []SettlementDetailRow{}
	next := int64(0)
	last := cursor
	err := d.Visit(ctx, f, cursor, func(v SettlementDetailRow, offset int64) error {
		if len(rows) == limit {
			next = last
			return errDetailPageFull
		}
		rows = append(rows, v)
		last = offset
		return nil
	}, nil)
	if errors.Is(err, errDetailPageFull) {
		err = nil
	}
	return rows, next, err
}
func detailHeaders(currency string) []string {
	return []string{"时间", "请求 ID", "模型", "令牌", "令牌 ID", "普通输入 Token", "普通输出 Token", "缓存读取", "缓存写入", "图像输入 Token", "图像输出 Token", "音频输入 Token", "音频输出 Token", UnitPriceHeader(currency), "原价金额 " + currency, "折扣", "折后金额 " + currency}
}
func detailCells(v SettlementDetailRow) []xlsxwriter.Cell {
	v.BeforeAmount, v.Discount = DefaultSettlementPrice(v.BeforeAmount, v.Discount, v.Amount)
	values := []string{v.Time, v.RequestID, v.Model, v.Token, v.TokenID, v.Input, v.Output, v.CacheRead, v.CacheWrite, v.ImageInput, v.ImageOutput, v.AudioInput, v.AudioOutput, unitPriceOrUnknown(v.UnitPrice), v.BeforeAmount, DiscountLabel(v.Discount), v.Amount}
	out := make([]xlsxwriter.Cell, len(values))
	for i, x := range values {
		if i == 14 && x == "" {
			x = "—"
		}
		if i >= 9 && i < 13 && x == "" {
			x = "0"
		}
		out[i] = xlsxwriter.Cell{Value: x, Number: i >= 5 && i != 13 && i != 15 && x != "" && x != "—" && x != "未记录"}
	}
	return out
}
func (d *SettlementDetailReader) Export(ctx context.Context, out io.Writer, job Job, f SettlementDetailFilter, progress func(int64)) (int64, error) {
	z := zip.NewWriter(out)
	var wb *xlsxwriter.Workbook
	var sheet *xlsxwriter.Sheet
	count, part := 0, 0
	var matched int64
	defer func() {
		if wb != nil {
			wb.Discard()
		}
	}()
	flush := func() error {
		if wb == nil {
			return nil
		}
		entry, e := z.CreateHeader(&zip.FileHeader{Name: fmt.Sprintf("日账单明细-%03d.xlsx", part), Method: zip.Store})
		if e != nil {
			return e
		}
		book := wb
		wb = nil
		defer book.Discard()
		return book.Write(entry)
	}
	next := func() error {
		if e := flush(); e != nil {
			return e
		}
		part++
		count = 0
		wb = xlsxwriter.New()
		var e error
		sheet, e = wb.AddReportSheet("请求明细", "日账单明细", SettlementSheetMetadata(job), []float64{24, 48, 34, 30, 16, 16, 16, 16, 16, 18, 18, 18, 18, 48, 20, 16, 20})
		if e != nil {
			return e
		}
		cells := []xlsxwriter.Cell{}
		for _, h := range detailHeaders(d.Manifest.Currency) {
			cells = append(cells, xlsxwriter.Cell{Value: h})
		}
		return sheet.Row(cells)
	}
	if err := next(); err != nil {
		z.Close()
		return 0, err
	}
	err := d.Visit(ctx, f, 0, func(v SettlementDetailRow, _ int64) error {
		if count >= SettlementDetailPartRows {
			if e := next(); e != nil {
				return e
			}
		}
		count++
		matched++
		return sheet.Row(detailCells(v))
	}, progress)
	if err != nil {
		if wb != nil {
			wb.Discard()
		}
		z.Close()
		return 0, err
	}
	if err = flush(); err != nil {
		z.Close()
		return 0, err
	}
	return matched, z.Close()
}

// Convert only saved native v3 worksheets; values are already in bill currency.
func ConvertSettlementDetails(ctx context.Context, out io.Writer, path string, currency string, progress func(int64)) error {
	z, err := zip.OpenReader(path)
	if err != nil {
		return err
	}
	defer z.Close()
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
	found := false
	return WriteSettlementDetailArchive(ctx, out, currency, func(visit func(SettlementDetailRow) error) error {
		for i, s := range book.Sheets {
			if !strings.HasPrefix(s.Name, "账单明细") {
				continue
			}
			found = true
			r, e := z.Open(fmt.Sprintf("xl/worksheets/sheet%d.xml", i+1))
			if e != nil {
				return e
			}
			dec := xml.NewDecoder(r)
			started := false
			mediaFirst := false
			priceColumn := -1
			for {
				if e = ctx.Err(); e != nil {
					r.Close()
					return e
				}
				token, e := dec.Token()
				if e == io.EOF {
					break
				}
				if e != nil {
					r.Close()
					return e
				}
				start, ok := token.(xml.StartElement)
				if !ok || start.Name.Local != "row" {
					continue
				}
				var row struct {
					Cells []struct {
						Text  string `xml:"is>t"`
						Value string `xml:"v"`
					} `xml:"c"`
				}
				if e = dec.DecodeElement(&row, &start); e != nil {
					r.Close()
					return e
				}
				if len(row.Cells) == 0 {
					continue
				}
				values := []string{}
				for _, c := range row.Cells {
					v := c.Text
					if c.Value != "" {
						v = c.Value
					}
					values = append(values, v)
				}
				if !started {
					if values[0] == "时间" {
						started = true
						mediaFirst = len(values) > 8 && values[8] == "图像输入 Token"
						for i, label := range values {
							if strings.HasPrefix(label, "模型单价") {
								priceColumn = i
							}
						}
					}
					continue
				}
				unitPrice := ""
				if priceColumn >= 0 {
					if priceColumn >= len(values) {
						return fmt.Errorf("missing unit price cell")
					}
					unitPrice = values[priceColumn]
					values = append(values[:priceColumn], values[priceColumn+1:]...)
				}
				if len(values) != 9 && len(values) != 11 && len(values) != 15 {
					r.Close()
					return fmt.Errorf("unexpected saved detail columns")
				}
				// Normalize both historical media-last and current media-before-price layouts.
				if len(values) == 15 && mediaFirst {
					values = append(append(append([]string{}, values[:8]...), values[12:15]...), values[8:12]...)
				}
				v := SettlementDetailRow{UnitPrice: unitPrice, Time: values[0], RequestID: values[1], Model: values[2], Token: values[3], Input: values[4], Output: values[5], CacheRead: values[6], CacheWrite: values[7], Amount: values[8]}
				if len(values) >= 11 {
					v.BeforeAmount = values[8]
					if v.BeforeAmount == "—" {
						v.BeforeAmount = ""
					}
					v.Amount = values[10]
					if values[9] == "原价" {
						v.Discount = "1"
					} else if strings.HasSuffix(values[9], " 折") {
						if rate, e := strconv.ParseFloat(strings.TrimSuffix(values[9], " 折"), 64); e == nil {
							v.Discount = strconv.FormatFloat(rate/10, 'f', 6, 64)
						}
					}
				}
				if len(values) == 15 {
					v.ImageInput = values[11]
					v.ImageOutput = values[12]
					v.AudioInput = values[13]
					v.AudioOutput = values[14]
				}
				if e = visit(v); e != nil {
					r.Close()
					return e
				}
			}
			r.Close()
			if !started {
				return fmt.Errorf("saved detail header missing")
			}
		}
		if !found {
			return fmt.Errorf("saved details unavailable")
		}
		return nil
	}, progress)
}
