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
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

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

// Only a numeric zero paired with an explicitly zero token count is a
// placeholder. Actual free usage (or missing usage evidence) must stay visible.
const unusedStatementPrice = "\x00unused-zero"

// Completed statements are immutable, so the resolved prices of a whole
// statement are cached under a signature of every detail file it lists. A
// per-file cache with a small hard cap would be cleared on every preview of a
// statement listing more files than the cap, re-parsing everything each time.
var statementPriceCache = struct {
	sync.Mutex
	items map[string]statementPrices
}{items: make(map[string]statementPrices)}

const (
	statementPriceCacheLimit = 32
	statementPriceWorkers    = 4
)

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
	type source struct{ path, day string }
	sources := make([]source, 0, len(files))
	var signature strings.Builder
	fmt.Fprintf(&signature, "%s|%s\n", job.ID, job.JobType)
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
		sources = append(sources, source{path: path, day: file.BillDay.In(billing.BusinessLocation).Format("2006-01-02")})
		fmt.Fprintf(&signature, "%s|%d|%d\n", path, info.Size(), info.ModTime().UnixNano())
	}
	cacheKey := signature.String()
	statementPriceCache.Lock()
	cached, found := statementPriceCache.items[cacheKey]
	statementPriceCache.Unlock()
	if found {
		return cached, nil
	}

	// Detail files are parsed with encoding/xml, which costs on the order of
	// ten seconds per hundred thousand rows; parse the statement's files
	// concurrently so first-view latency is bounded by the largest file.
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	out := statementPrices{}
	var (
		mu       sync.Mutex
		wg       sync.WaitGroup
		once     sync.Once
		firstErr error
	)
	sem := make(chan struct{}, statementPriceWorkers)
	for _, src := range sources {
		select {
		case sem <- struct{}{}:
		case <-ctx.Done():
		}
		if ctx.Err() != nil {
			break
		}
		wg.Add(1)
		go func(src source) {
			defer wg.Done()
			defer func() { <-sem }()
			prices, declaredDay, err := readStatementFilePrices(ctx, src.path)
			if err != nil {
				once.Do(func() { firstErr = fmt.Errorf("read billing detail prices %s: %w", filepath.Base(src.path), err) })
				cancel()
				return
			}
			mu.Lock()
			defer mu.Unlock()
			day := src.day
			if declaredDay != "" {
				day = declaredDay
			}
			for key, tuples := range prices {
				key.Day = day
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
		}(src)
	}
	wg.Wait()
	if firstErr != nil {
		return nil, firstErr
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	statementPriceCache.Lock()
	if len(statementPriceCache.items) >= statementPriceCacheLimit {
		clear(statementPriceCache.items)
	}
	statementPriceCache.items[cacheKey] = out
	statementPriceCache.Unlock()
	return out, nil
}

// price lists the distinct unit prices actually charged for a day/model
// (per channel for upstream statements), column by column. Discard only zeros
// proven unused by the corresponding detail token count, never real free prices.
func (prices statementPrices) price(job billing.Job, row billing.StatementAggregateRow) billing.Price {
	key := statementPriceKey{Day: row.Day.In(billing.BusinessLocation).Format("2006-01-02"), Model: row.ModelName}
	if job.JobType == "upstream_statement" {
		key.Channel = row.ChannelID
	}
	var columns [4][]string
	for tuple := range prices[key] {
		for i, value := range tuple {
			if value != "" && !slices.Contains(columns[i], value) {
				columns[i] = append(columns[i], value)
			}
		}
	}
	values := statementPriceTuple{}
	for i, column := range columns {
		hasPrice := false
		for _, value := range column {
			if _, ok := new(big.Rat).SetString(value); ok {
				hasPrice = true
			}
		}
		filtered := make([]string, 0, len(column))
		for _, value := range column {
			if hasPrice && (value == unusedStatementPrice || value == "未使用") {
				continue
			}
			if value == unusedStatementPrice {
				value = "0.000000"
			}
			if !slices.Contains(filtered, value) {
				filtered = append(filtered, value)
			}
		}
		column = filtered
		sort.Slice(column, func(a, b int) bool {
			x, okX := new(big.Rat).SetString(column[a])
			y, okY := new(big.Rat).SetString(column[b])
			if okX && okY {
				return x.Cmp(y) < 0
			}
			if okX != okY {
				return okX
			}
			return column[a] < column[b]
		})
		if len(column) == 0 {
			values[i] = "明细价格不可用"
			continue
		}
		values[i] = strings.Join(column, " / ")
	}
	return billing.Price{Input: values[0], Output: values[1], Cache: values[2], CacheWrite: values[3]}
}

// CT's writer uses inline strings and numeric cells; stream rows rather than
// loading a potentially million-order workbook into memory.
// readStatementFilePrices also returns the bill day the workbook declares in
// its own header ("账单日期"). File registrations written before the calendar
// date fix (f7a8fa4) can carry a bill_day shifted by one day, so the day the
// file states about itself is the reliable join key.
func readStatementFilePrices(ctx context.Context, path string) (statementPrices, string, error) {
	z, err := zip.OpenReader(path)
	if err != nil {
		return nil, "", err
	}
	defer z.Close()
	out := statementPrices{}
	day := ""
	for _, file := range z.File {
		if !strings.HasPrefix(file.Name, "xl/worksheets/sheet") || !strings.HasSuffix(file.Name, ".xml") {
			continue
		}
		in, err := file.Open()
		if err != nil {
			return nil, "", err
		}
		sheetDay, err := readStatementPriceSheet(ctx, in, out)
		in.Close()
		if err != nil {
			return nil, "", err
		}
		if day == "" {
			day = sheetDay
		}
	}
	return out, day, nil
}

func readStatementPriceSheet(ctx context.Context, in io.Reader, out statementPrices) (string, error) {
	decoder := xml.NewDecoder(in)
	headers := map[string]int{}
	day := ""
	for {
		if err := ctx.Err(); err != nil {
			return day, err
		}
		token, err := decoder.Token()
		if err == io.EOF {
			return day, nil
		}
		if err != nil {
			return day, err
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
			return day, err
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
			if values[1] == "账单日期" {
				if _, parseErr := time.ParseInLocation("2006-01-02", values[2], billing.BusinessLocation); parseErr == nil {
					day = values[2]
				}
			}
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
				tokenHeader := []string{"输入 Token", "输出 Token", "缓存读取 Token", "普通缓存写入 Token"}[i]
				tokenIndex := headers[tokenHeader]
				if i == 3 && tokenIndex == 0 {
					tokenIndex = headers["缓存写入 Token"]
				}
				// Missing/invalid token cells are not evidence of unused usage.
				if tokens, valid := new(big.Rat).SetString(values[tokenIndex]); n.Sign() == 0 && valid && tokens.Sign() == 0 {
					value = unusedStatementPrice
				}
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
