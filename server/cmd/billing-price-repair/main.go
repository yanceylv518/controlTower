// billing-price-repair restores display metadata for a small, completed user day.
// It defaults to a read-only verification; amounts and usage are never updated.
package main

import (
	"archive/zip"
	"bytes"
	"context"
	"controltower/server/internal/billing"
	"controltower/server/internal/dashboard"
	"controltower/server/internal/mysqlstore"
	"database/sql"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"math/big"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"time"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

type groupKey struct {
	Token, Channel int64
	Model          string
}
type totals struct {
	Prices billing.UnitPrices
	Count  int64
	Amount *big.Rat
}
type savedGroup struct {
	Job            string
	Token, Channel int64
	Model          string
	Count          int64
	Amount         string
	Prices         json.RawMessage
}

func rowKey(r billing.SettlementDetailRow) string {
	return r.Time + "|" + r.RequestID + "|" + r.Model + "|" + r.TokenID
}
func sameNumber(a, b string) bool {
	x, ok := new(big.Rat).SetString(a)
	y, ok2 := new(big.Rat).SetString(b)
	return ok && ok2 && x.Cmp(y) == 0
}
func run() error {
	id := flag.String("job", "", "completed user daily job ID")
	apply := flag.Bool("apply", false, "apply verified price-only repair with backups")
	flag.Parse()
	if *id == "" {
		return fmt.Errorf("-job is required")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	db, err := mysqlstore.Open(os.Getenv("CT_DATABASE_DSN"))
	if err != nil {
		return err
	}
	defer db.Close()
	store := mysqlstore.New(db)
	job, err := store.BillingJob(ctx, *id)
	if err != nil {
		return err
	}
	if job.Status != "complete" || job.JobType != "user_statement" || job.BillPeriod != "daily" || job.DataSource != "source" || job.UsageVersion < 3 || job.MoneySnapshot == nil {
		return fmt.Errorf("requires completed source user day with saved currency snapshot")
	}
	files, err := store.ListBillingStatementUserFiles(ctx, job.ID)
	if err != nil {
		return err
	}
	path := ""
	root, err := filepath.Abs(billing.DefaultBillingFileRoot)
	if err != nil {
		return err
	}
	for _, f := range files {
		if f.UserID == job.UserID && f.BillDay.Format("2006-01-02") == job.From.In(billing.BusinessLocation).Format("2006-01-02") {
			path = filepath.Join(root, filepath.FromSlash(f.RelativePath)) + ".details.zip"
		}
	}
	rel, err := filepath.Rel(root, path)
	if err != nil || !filepath.IsLocal(rel) {
		return fmt.Errorf("invalid saved detail path")
	}
	original, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	z, err := zip.OpenReader(path)
	if err != nil {
		return err
	}
	manifestFile, err := z.Open("manifest.json")
	if err != nil {
		z.Close()
		return err
	}
	var manifest billing.SettlementDetailManifest
	err = json.NewDecoder(manifestFile).Decode(&manifest)
	manifestFile.Close()
	if err != nil {
		z.Close()
		return err
	}
	if manifest.Total == 0 || manifest.Total >= 1000 {
		z.Close()
		return fmt.Errorf("repair is limited to days with 1-999 rows")
	}
	saved := []billing.SettlementDetailRow{}
	for _, part := range manifest.Parts {
		f, e := z.Open(part.Name)
		if e != nil {
			z.Close()
			return e
		}
		d := json.NewDecoder(f)
		for {
			var v billing.SettlementDetailRow
			e = d.Decode(&v)
			if e == io.EOF {
				break
			}
			if e != nil {
				f.Close()
				z.Close()
				return e
			}
			saved = append(saved, v)
		}
		f.Close()
	}
	z.Close()
	source := dashboard.BillingReadonlySource{Handler: &dashboard.PassthroughHandler{Config: store, SecretKey: os.Getenv("CT_SECRET_KEY")}}
	logs, err := source.DetailedLogsPage(ctx, job.InstanceID, job.UserID, job.From, job.To, billing.LogCursor{}, 1000)
	if err != nil {
		return err
	}
	if len(logs) != len(saved) || int64(len(saved)) != manifest.Total {
		return fmt.Errorf("source and saved row counts differ")
	}
	display, rate, err := billing.SettlementDisplay(job.MoneySnapshot)
	if err != nil {
		return err
	}
	if billing.SettlementCurrencyLabel(display) != manifest.Currency {
		return fmt.Errorf("saved currency mismatch")
	}
	qpu, ok := new(big.Rat).SetString(job.MoneySnapshot.QuotaPerUnit)
	if !ok || qpu.Sign() <= 0 {
		return fmt.Errorf("invalid saved quota unit")
	}
	byKey := map[string]billing.SettlementDetailRow{}
	groups := map[groupKey]*totals{}
	labels := map[string]int{}
	for _, v := range logs {
		c, e := billing.CalculateLogCharge(v, job.MoneySnapshot.QuotaPerUnit)
		if e != nil {
			return fmt.Errorf("source log %d price evidence unavailable", v.ID)
		}
		prices := billing.SimpleUnitPrices(c)
		amount := new(big.Rat).Quo(big.NewRat(v.Quota, 1), qpu)
		row := billing.SettlementDetailRow{Time: time.Unix(v.CreatedUnix, 0).In(billing.BusinessLocation).Format("2006-01-02 15:04:05"), RequestID: v.RequestID, Model: v.ModelName, TokenID: strconv.FormatInt(v.TokenID, 10), Input: strconv.FormatInt(max(0, v.PromptTokens.Int64-v.AudioInputTokens), 10), Output: strconv.FormatInt(max(0, v.CompletionTokens.Int64-v.ImageOutputTokens-v.AudioOutputTokens), 10), CacheRead: strconv.FormatInt(v.CacheTokens, 10), CacheWrite: strconv.FormatInt(v.CacheWriteTokens, 10), Amount: billing.DisplaySettlementAmount(amount.FloatString(12), rate), UnitPrice: billing.UnitPriceLabel(prices, rate)}
		key := rowKey(row)
		if _, exists := byKey[key]; exists {
			return fmt.Errorf("ambiguous request identity")
		}
		byKey[key] = row
		gk := groupKey{v.TokenID, v.ChannelID, v.ModelName}
		g := groups[gk]
		if g == nil {
			g = &totals{Amount: new(big.Rat)}
			groups[gk] = g
		}
		g.Count++
		g.Amount.Add(g.Amount, amount)
		g.Prices.Merge(prices)
		labels[row.UnitPrice]++
	}
	repaired := append([]billing.SettlementDetailRow(nil), saved...)
	for i, row := range saved {
		source, ok := byKey[rowKey(row)]
		if !ok {
			return fmt.Errorf("saved request %d has no exact source match", i)
		}
		if !sameNumber(source.Amount, row.Amount) || source.Input != row.Input || source.Output != row.Output || source.CacheRead != row.CacheRead || source.CacheWrite != row.CacheWrite {
			return fmt.Errorf("saved request %d usage or amount differs from source", i)
		}
		if row.UnitPrice != "" && row.UnitPrice != "未记录" && row.UnitPrice != source.UnitPrice {
			return fmt.Errorf("existing price differs")
		}
		repaired[i].UnitPrice = source.UnitPrice
		delete(byKey, rowKey(row))
	}
	if len(byKey) != 0 {
		return fmt.Errorf("unmatched source requests")
	}
	tx, err := db.BeginTx(ctx, &sql.TxOptions{})
	if err != nil {
		return err
	}
	defer tx.Rollback()
	day := job.From.In(billing.BusinessLocation).Format("2006-01-02")
	rows, err := tx.QueryContext(ctx, `SELECT job_id,token_id,channel_id,model_name,request_count,CAST(total_amount AS CHAR),unit_prices FROM billing_compact_daily_totals WHERE bill_day=? AND user_id=? AND (job_id=? OR job_id IN (SELECT month_job_id FROM billing_month_daily_sources WHERE daily_job_id=?)) FOR UPDATE`, day, job.UserID, job.ID, job.ID)
	if err != nil {
		return err
	}
	old := []savedGroup{}
	seen := map[string]int{}
	for rows.Next() {
		var v savedGroup
		var raw []byte
		if err = rows.Scan(&v.Job, &v.Token, &v.Channel, &v.Model, &v.Count, &v.Amount, &raw); err != nil {
			rows.Close()
			return err
		}
		if raw != nil {
			v.Prices = append([]byte(nil), raw...)
		}
		g := groups[groupKey{v.Token, v.Channel, v.Model}]
		if g == nil || g.Count != v.Count || !sameNumber(g.Amount.FloatString(12), v.Amount) {
			rows.Close()
			return fmt.Errorf("aggregate snapshot differs; refusing repair")
		}
		old = append(old, v)
		seen[v.Job]++
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	if seen[job.ID] != len(groups) {
		return fmt.Errorf("daily aggregate coverage differs")
	}
	for _, n := range seen {
		if n != len(groups) {
			return fmt.Errorf("monthly aggregate coverage differs")
		}
	}
	if !*apply {
		return json.NewEncoder(os.Stdout).Encode(map[string]any{"verified_rows": len(saved), "aggregate_rows": len(old), "jobs": len(seen), "prices": labels, "mode": "dry_run"})
	}
	backup := filepath.Join("outputs", "billing-price-repair", job.ID, time.Now().Format("20060102-150405"))
	if err = os.MkdirAll(backup, 0700); err != nil {
		return err
	}
	if err = os.WriteFile(filepath.Join(backup, "details.zip"), original, 0600); err != nil {
		return err
	}
	raw, err := json.MarshalIndent(old, "", "  ")
	if err != nil {
		return err
	}
	if err = os.WriteFile(filepath.Join(backup, "aggregates.json"), raw, 0600); err != nil {
		return err
	}
	temp, err := os.CreateTemp(filepath.Dir(path), ".price-repair-*.zip")
	if err != nil {
		return err
	}
	defer os.Remove(temp.Name())
	err = writePrices(temp, original, repaired)
	closeErr := temp.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	// Round-trip verification before any canonical file or database write.
	check, err := billing.OpenSettlementDetails(temp.Name())
	if err != nil {
		return err
	}
	got := []billing.SettlementDetailRow{}
	err = check.Visit(ctx, billing.SettlementDetailFilter{}, 0, func(v billing.SettlementDetailRow, _ int64) error { got = append(got, v); return nil }, nil)
	check.Close()
	if err != nil {
		return err
	}
	if len(got) != len(saved) {
		return fmt.Errorf("round trip count differs")
	}
	for i := range got {
		if got[i].UnitPrice != repaired[i].UnitPrice {
			return fmt.Errorf("price round trip differs")
		}
		a, b := got[i], saved[i]
		a.UnitPrice = b.UnitPrice
		b.BeforeAmount, b.Discount = billing.DefaultSettlementPrice(b.BeforeAmount, b.Discount, b.Amount)
		for _, s := range []*string{&b.ImageInput, &b.ImageOutput, &b.AudioInput, &b.AudioOutput} {
			if *s == "" {
				*s = "0"
			}
		}
		if !reflect.DeepEqual(a, b) {
			return fmt.Errorf("non-price data changed in row %d", i)
		}
	}
	for _, v := range old {
		g := groups[groupKey{v.Token, v.Channel, v.Model}]
		raw, err = json.Marshal(g.Prices)
		if err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, `UPDATE billing_compact_daily_totals SET unit_prices=? WHERE job_id=? AND bill_day=? AND user_id=? AND token_id=? AND channel_id=? AND model_name=?`, string(raw), v.Job, day, job.UserID, v.Token, v.Channel, v.Model)
		if err != nil {
			return err
		}
	}
	// Replace atomically; on commit failure restore the original archive from backup.
	if err = os.Rename(temp.Name(), path); err != nil {
		return err
	}
	if err = tx.Commit(); err != nil {
		_ = os.WriteFile(path, original, 0600)
		return err
	}
	return json.NewEncoder(os.Stdout).Encode(map[string]any{"updated_rows": len(saved), "aggregate_rows": len(old), "jobs": len(seen), "prices": labels, "backup": backup, "mode": "applied"})
}

// Preserve the original JSON keys and exact non-price values, including fields
// absent in older archive editions. The manifest and shard order stay intact.
func writePrices(out io.Writer, original []byte, repaired []billing.SettlementDetailRow) error {
	in, err := zip.NewReader(bytes.NewReader(original), int64(len(original)))
	if err != nil {
		return err
	}
	z := zip.NewWriter(out)
	index := 0
	for _, file := range in.File {
		r, e := file.Open()
		if e != nil {
			return e
		}
		header := file.FileHeader
		w, e := z.CreateHeader(&header)
		if e != nil {
			r.Close()
			return e
		}
		if strings.HasSuffix(file.Name, ".jsonl") {
			d := json.NewDecoder(r)
			encoder := json.NewEncoder(w)
			for {
				var v map[string]json.RawMessage
				e = d.Decode(&v)
				if e == io.EOF {
					break
				}
				if e != nil {
					r.Close()
					return e
				}
				if index >= len(repaired) {
					r.Close()
					return fmt.Errorf("unexpected archive row")
				}
				price, e := json.Marshal(repaired[index].UnitPrice)
				if e != nil {
					r.Close()
					return e
				}
				v["unit_price"] = price
				if e = encoder.Encode(v); e != nil {
					r.Close()
					return e
				}
				index++
			}
		} else {
			if _, e = io.Copy(w, r); e != nil {
				r.Close()
				return e
			}
		}
		r.Close()
	}
	if index != len(repaired) {
		return fmt.Errorf("archive row count changed")
	}
	return z.Close()
}
