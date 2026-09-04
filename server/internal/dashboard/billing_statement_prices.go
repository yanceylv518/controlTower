package dashboard

import (
	"archive/zip"
	"context"
	"encoding/xml"
	"fmt"
	"io"
	"math/big"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"

	"controltower/server/internal/billing"
)

// Daily files are immutable snapshots of the prices calculated from NewAPI
// logs (including expressions). Never substitute the current price schedule.
type statementPriceKey struct {
	Day, Model string
	Channel    int64
}
type statementPriceTuple [4]string
type statementPrices map[statementPriceKey]map[statementPriceTuple]bool

var statementPriceCache = struct {
	sync.Mutex
	items map[string]statementPrices
}{items: make(map[string]statementPrices)}

func loadStatementPrices(ctx context.Context, job billing.Job, store BillingStatementResultStore, roots ...string) (statementPrices, error) {
	files, err := store.ListBillingStatementUserFiles(ctx, job.ID)
	if err != nil {
		return nil, err
	}
	root := billing.DefaultBillingFileRoot
	if len(roots) > 0 && roots[0] != "" {
		root = roots[0]
	}
	root, err = filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	out := statementPrices{}
	for _, file := range files {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		path := filepath.Join(root, filepath.FromSlash(file.RelativePath))
		rel, err := filepath.Rel(root, path)
		if err != nil || rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			return nil, fmt.Errorf("invalid billing detail path")
		}
		info, err := os.Stat(path)
		if os.IsNotExist(err) {
			continue
		} // Explicitly label unavailable historical prices.
		if err != nil {
			return nil, err
		}
		cacheKey := fmt.Sprintf("%s|%d|%d", path, info.Size(), info.ModTime().UnixNano())
		statementPriceCache.Lock()
		prices, found := statementPriceCache.items[cacheKey]
		statementPriceCache.Unlock()
		if !found {
			prices, err = readStatementFilePrices(ctx, path)
			if err != nil {
				return nil, fmt.Errorf("read billing detail prices: %w", err)
			}
			statementPriceCache.Lock()
			if len(statementPriceCache.items) >= 128 {
				clear(statementPriceCache.items)
			}
			statementPriceCache.items[cacheKey] = prices
			statementPriceCache.Unlock()
		}
		for key, tuples := range prices {
			key.Day = file.BillDay.In(billing.BusinessLocation).Format("2006-01-02")
			if job.JobType == "user_statement" {
				key.Channel = 0
			}
			if out[key] == nil {
				out[key] = map[statementPriceTuple]bool{}
			}
			for tuple := range tuples {
				out[key][tuple] = true
			}
		}
	}
	return out, nil
}

func (prices statementPrices) price(job billing.Job, row billing.StatementAggregateRow) billing.Price {
	key := statementPriceKey{Day: row.Day.In(billing.BusinessLocation).Format("2006-01-02"), Model: row.ModelName}
	if job.JobType == "upstream_statement" {
		key.Channel = row.ChannelID
	}
	tuples := make([]statementPriceTuple, 0, len(prices[key]))
	for tuple := range prices[key] {
		tuples = append(tuples, tuple)
	}
	sort.Slice(tuples, func(i, j int) bool { return strings.Join(tuples[i][:], "|") < strings.Join(tuples[j][:], "|") })
	values := statementPriceTuple{}
	for i := range values {
		parts := []string{}
		for _, tuple := range tuples {
			parts = append(parts, tuple[i])
		}
		values[i] = strings.Join(parts, " / ")
		if len(parts) == 0 {
			values[i] = "明细价格不可用"
		}
	}
	return billing.Price{Input: values[0], Output: values[1], Cache: values[2], CacheWrite: values[3]}
}

// CT's writer uses inline strings and numeric cells; stream rows rather than
// loading a potentially million-order workbook into memory.
func readStatementFilePrices(ctx context.Context, path string) (statementPrices, error) {
	z, err := zip.OpenReader(path)
	if err != nil {
		return nil, err
	}
	defer z.Close()
	out := statementPrices{}
	for _, file := range z.File {
		if !strings.HasPrefix(file.Name, "xl/worksheets/sheet") || !strings.HasSuffix(file.Name, ".xml") {
			continue
		}
		in, err := file.Open()
		if err != nil {
			return nil, err
		}
		err = readStatementPriceSheet(ctx, in, out)
		in.Close()
		if err != nil {
			return nil, err
		}
	}
	return out, nil
}

func readStatementPriceSheet(ctx context.Context, in io.Reader, out statementPrices) error {
	decoder := xml.NewDecoder(in)
	headers := map[string]int{}
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		token, err := decoder.Token()
		if err == io.EOF {
			return nil
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
				Ref   string `xml:"r,attr"`
				Value string `xml:"v"`
				Text  string `xml:"is>t"`
			} `xml:"c"`
		}
		if err = decoder.DecodeElement(&row, &start); err != nil {
			return err
		}
		values := map[int]string{}
		for _, cell := range row.Cells {
			index := 0
			for _, c := range cell.Ref {
				if c < 'A' || c > 'Z' {
					break
				}
				index = index*26 + int(c-'A') + 1
			}
			value := cell.Value
			if cell.Text != "" {
				value = cell.Text
			}
			values[index] = value
		}
		if len(headers) == 0 {
			candidate := map[string]int{}
			for index, value := range values {
				candidate[value] = index
			}
			if candidate["模型"] != 0 && candidate["输入单价"] != 0 {
				headers = candidate
			}
			continue
		}
		model := values[headers["模型"]]
		if model == "" {
			continue
		}
		channel := values[headers["渠道"]]
		if i := strings.LastIndex(channel, "(#"); i >= 0 {
			channel = strings.TrimSuffix(channel[i+2:], ")")
		}
		channelID, _ := strconv.ParseInt(channel, 10, 64)
		key := statementPriceKey{Model: model, Channel: channelID}
		tuple := statementPriceTuple{}
		for i, header := range []string{"输入单价", "输出单价", "缓存读取单价", "普通缓存写入单价"} {
			index := headers[header]
			if i == 3 && index == 0 {
				index = headers["缓存写入单价"]
			}
			value := values[index]
			if n, ok := new(big.Rat).SetString(value); ok {
				value = n.FloatString(6)
			}
			if value == "" {
				value = "未记录"
				if i == 3 && index == 0 {
					value = "未使用"
				}
			}
			tuple[i] = value
		}
		if out[key] == nil {
			out[key] = map[statementPriceTuple]bool{}
		}
		out[key][tuple] = true
	}
}
