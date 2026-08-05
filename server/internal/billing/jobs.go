package billing

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"fmt"
	"math/big"
	"strings"
	"time"
)

const BillingPageSize = 2000

// BusinessLocation is explicit because production containers normally run
// in UTC while new-api billing periods are defined in China Standard Time.
var BusinessLocation = time.FixedZone("Asia/Shanghai", 8*60*60)

type PagedLogRecord struct {
	ID, CreatedUnix, UserID, ChannelID, CacheTokens, Quota       int64
	CacheWriteTokens, CacheWrite5mTokens, CacheWrite1hTokens     int64
	ContextTokens                                                int64
	UsageSemantic                                                string
	RequestID, UpstreamRequestID, Username, ModelName, GroupName string
	ChannelName                                                  string
	// SourcePromptTokens preserves logs.prompt_tokens before the billing
	// source separates cache reads/writes from ordinary input tokens.
	SourcePromptTokens, PromptTokens, CompletionTokens sql.NullInt64
}

type LogCursor struct{ CreatedUnix, ID int64 }

type PageSource interface {
	LogsPage(context.Context, string, time.Time, time.Time, LogCursor, int) ([]PagedLogRecord, error)
}
type SnapshotSource interface {
	RatioSnapshot(context.Context, string) (string, error)
	Balances(context.Context, string) (map[int64]int64, error)
}
type SnapshotStore interface {
	PutBillingRatioSnapshot(context.Context, string, time.Time, string) error
	PutBillingBalanceSnapshots(context.Context, string, time.Time, map[int64]int64) error
}

type UserSetting struct {
	InstanceID       string    `json:"instance_id"`
	UserID           int64     `json:"user_id"`
	UseTieredPricing bool      `json:"use_tiered_pricing"`
	UpdatedAt        time.Time `json:"updated_at"`
	UpdatedBy        string    `json:"updated_by"`
}

type Job struct {
	ID             string    `json:"id"`
	RequestKey     string    `json:"-"`
	InstanceID     string    `json:"instance_id"`
	JobType        string    `json:"job_type"`
	UserID         int64     `json:"user_id"`
	From           time.Time `json:"range_from"`
	To             time.Time `json:"range_to"`
	Status         string    `json:"status"`
	TotalSteps     int       `json:"total_steps"`
	CompletedSteps int       `json:"completed_steps"`
	AbnormalRows   int64     `json:"abnormal_rows"`
	ErrorMessage   string    `json:"error_message,omitempty"`
	OutputPath     string    `json:"output_path,omitempty"`
	RequestedBy    string    `json:"requested_by"`
	CreatedAt      time.Time `json:"created_at"`
	UpdatedAt      time.Time `json:"updated_at"`
}

type JobStep struct {
	JobID    string
	StepNo   int
	From, To time.Time
	Cursor   LogCursor
}
type ChannelDailyRow struct {
	InstanceID                                                       string
	ChannelID                                                        int64
	ChannelName, ModelName, GroupName                                string
	TierFrom                                                         int64
	Day                                                              time.Time
	RequestCount, PromptTokens, CompletionTokens, CacheTokens, Quota int64
	CacheWriteTokens, CacheWrite5mTokens, CacheWrite1hTokens         int64
	UpdatedAt                                                        time.Time
}

type AnomalyOrder struct {
	InstanceID                                                                                     string    `json:"instance_id"`
	SourceLogID                                                                                    int64     `json:"source_log_id"`
	JobID                                                                                          string    `json:"job_id"`
	CreatedAt                                                                                      time.Time `json:"created_at"`
	RequestID                                                                                      string    `json:"request_id"`
	UpstreamRequestID                                                                              string    `json:"upstream_request_id"`
	UserID                                                                                         int64     `json:"user_id"`
	Username                                                                                       string    `json:"username"`
	ChannelID                                                                                      int64     `json:"channel_id"`
	ChannelName                                                                                    string    `json:"channel_name"`
	ModelName                                                                                      string    `json:"model_name"`
	GroupName                                                                                      string    `json:"group_name"`
	PromptTokens, CompletionTokens                                                                 sql.NullInt64
	CacheTokens, CacheWriteTokens, CacheWrite5mTokens, CacheWrite1hTokens, Quota, MaxContextTokens int64
	InputPrice, OutputPrice, CachePrice, CacheWritePrice                                           string
	InputAmount, OutputAmount, CacheAmount, CacheWriteAmount, ReferenceAmount                      string
	Reasons                                                                                        string    `json:"reasons"`
	DetectedAt                                                                                     time.Time `json:"detected_at"`
}

type JobStore interface {
	CreateBillingJob(context.Context, Job, []JobStep) error
	ClaimBillingStep(context.Context) (Job, JobStep, bool, error)
	BillingJob(context.Context, string) (Job, error)
	ListBillingUserSettings(context.Context, string) (map[int64]UserSetting, error)
	ListBillingModelMetadata(context.Context, string) ([]ModelMetadata, error)
	ListBillingPrices(context.Context, string) ([]PriceRecord, error)
	ListBillingGroupRatios(context.Context, string) ([]GroupRatio, error)
	AppendBillingHour(context.Context, Job, JobStep, []DailyRow, []ChannelDailyRow, []AnomalyOrder, LogCursor, int64) error
	CompleteBillingStep(context.Context, Job, JobStep, int64, int64) error
	FailBillingStep(context.Context, Job, JobStep, error) error
	FinalizeBillingJob(context.Context, Job) error
}

func NewJob(instanceID string, from, to time.Time, requestedBy string) (Job, []JobStep, error) {
	if strings.TrimSpace(instanceID) == "" || !to.After(from) || to.Sub(from) > 60*24*time.Hour {
		return Job{}, nil, fmt.Errorf("invalid billing range")
	}
	raw := make([]byte, 16)
	_, _ = rand.Read(raw)
	now := time.Now().UTC()
	job := Job{ID: hex.EncodeToString(raw), InstanceID: instanceID, JobType: "generate", From: from, To: to, Status: "pending", RequestedBy: requestedBy, CreatedAt: now, UpdatedAt: now}
	steps := []JobStep{}
	n := 0
	for start := from; start.Before(to); {
		end := start.Truncate(time.Hour).Add(time.Hour)
		if !end.After(start) {
			end = start.Add(time.Hour)
		}
		if end.After(to) {
			end = to
		}
		steps = append(steps, JobStep{JobID: job.ID, StepNo: n, From: start, To: end})
		n++
		start = end
	}
	job.TotalSteps = len(steps)
	return job, steps, nil
}

type JobRunner struct {
	Source PageSource
	Store  JobStore
	Poll   time.Duration
}

func (r JobRunner) Run(ctx context.Context) error {
	if r.Poll <= 0 {
		r.Poll = time.Second
	}
	for {
		worked, err := r.RunOnce(ctx)
		if err != nil {
			return err
		}
		if worked {
			continue
		}
		t := time.NewTimer(r.Poll)
		select {
		case <-ctx.Done():
			t.Stop()
			return ctx.Err()
		case <-t.C:
		}
	}
}

func (r JobRunner) RunOnce(ctx context.Context) (bool, error) {
	job, step, ok, err := r.Store.ClaimBillingStep(ctx)
	if err != nil || !ok {
		return ok, err
	}
	if err = r.processStep(ctx, job, step); err != nil {
		_ = r.Store.FailBillingStep(ctx, job, step, err)
		return true, nil
	}
	return true, nil
}

func (r JobRunner) processStep(ctx context.Context, job Job, step JobStep) error {
	prices, err := r.Store.ListBillingPrices(ctx, job.InstanceID)
	if err != nil {
		return err
	}
	ratios, err := r.Store.ListBillingGroupRatios(ctx, job.InstanceID)
	if err != nil {
		return err
	}
	metadata, err := r.Store.ListBillingModelMetadata(ctx, job.InstanceID)
	if err != nil {
		return err
	}
	settings, err := r.Store.ListBillingUserSettings(ctx, job.InstanceID)
	if err != nil {
		return err
	}
	priceByModel := map[string][]Price{}
	for _, p := range prices {
		priceByModel[p.ModelName] = append(priceByModel[p.ModelName], p.Price)
	}
	ratioByGroup := map[string]string{}
	for _, v := range ratios {
		ratioByGroup[v.GroupName] = v.Ratio
	}
	maxByModel := map[string]int64{}
	for _, m := range metadata {
		maxByModel[m.ModelName] = m.MaxContextTokens
	}
	cursor := step.Cursor
	var processed, abnormal int64
	for {
		logs, e := r.Source.LogsPage(ctx, job.InstanceID, step.From, step.To, cursor, BillingPageSize)
		if e != nil {
			return e
		}
		if len(logs) == 0 {
			break
		}
		type key struct {
			user         int64
			model, group string
			tier         int64
		}
		acc := map[key]DailyRow{}
		type channelKey struct {
			id           int64
			model, group string
			tier         int64
		}
		channelAcc := map[channelKey]ChannelDailyRow{}
		anomalies := []AnomalyOrder{}
		for _, log := range logs {
			setting, configured := settings[log.UserID]
			useTiers := !configured || setting.UseTieredPricing
			reasons := anomalyReasons(log, maxByModel[log.ModelName])
			if len(reasons) > 0 {
				item := AnomalyOrder{InstanceID: job.InstanceID, SourceLogID: log.ID, JobID: job.ID, CreatedAt: time.Unix(log.CreatedUnix, 0), RequestID: log.RequestID, UpstreamRequestID: log.UpstreamRequestID, UserID: log.UserID, Username: log.Username, ChannelID: log.ChannelID, ChannelName: log.ChannelName, ModelName: log.ModelName, GroupName: log.GroupName, PromptTokens: sourcePromptTokens(log), CompletionTokens: log.CompletionTokens, CacheTokens: log.CacheTokens, CacheWriteTokens: log.CacheWriteTokens, CacheWrite5mTokens: log.CacheWrite5mTokens, CacheWrite1hTokens: log.CacheWrite1hTokens, Quota: log.Quota, MaxContextTokens: maxByModel[log.ModelName], Reasons: strings.Join(reasons, ","), DetectedAt: time.Now().UTC()}
				fillAnomalyAmounts(&item, log, priceByModel[log.ModelName], ratioByGroup[log.GroupName], step.From, useTiers)
				anomalies = append(anomalies, item)
				continue
			}
			tier := int64(0)
			if useTiers {
				if p, found := SelectPrice(priceByModel[log.ModelName], step.From, log.ContextTokens); found {
					tier = p.TierFrom
				}
			}
			k := key{log.UserID, log.ModelName, log.GroupName, tier}
			row := acc[k]
			row.InstanceID = job.InstanceID
			row.UserID = log.UserID
			row.Username = log.Username
			row.ModelName = log.ModelName
			row.GroupName = log.GroupName
			row.TierFrom = tier
			row.Day = dateOnly(step.From)
			row.RequestCount++
			row.PromptTokens += log.PromptTokens.Int64
			row.CompletionTokens += log.CompletionTokens.Int64
			row.CacheTokens += log.CacheTokens
			row.CacheWriteTokens += log.CacheWriteTokens
			row.CacheWrite5mTokens += log.CacheWrite5mTokens
			row.CacheWrite1hTokens += log.CacheWrite1hTokens
			row.Quota += log.Quota
			row.UpdatedAt = time.Now().UTC()
			acc[k] = row
			ck := channelKey{log.ChannelID, log.ModelName, log.GroupName, tier}
			cr := channelAcc[ck]
			cr.InstanceID = job.InstanceID
			cr.ChannelID = log.ChannelID
			cr.ChannelName = log.ChannelName
			cr.ModelName = log.ModelName
			cr.GroupName = log.GroupName
			cr.TierFrom = tier
			cr.Day = dateOnly(step.From)
			cr.RequestCount++
			cr.PromptTokens += log.PromptTokens.Int64
			cr.CompletionTokens += log.CompletionTokens.Int64
			cr.CacheTokens += log.CacheTokens
			cr.CacheWriteTokens += log.CacheWriteTokens
			cr.CacheWrite5mTokens += log.CacheWrite5mTokens
			cr.CacheWrite1hTokens += log.CacheWrite1hTokens
			cr.Quota += log.Quota
			cr.UpdatedAt = time.Now().UTC()
			channelAcc[ck] = cr
		}
		rows := make([]DailyRow, 0, len(acc))
		for _, row := range acc {
			rows = append(rows, row)
		}
		channelRows := make([]ChannelDailyRow, 0, len(channelAcc))
		for _, row := range channelAcc {
			channelRows = append(channelRows, row)
		}
		last := logs[len(logs)-1]
		cursor = LogCursor{last.CreatedUnix, last.ID}
		processed += int64(len(logs))
		abnormal += int64(len(anomalies))
		if e = r.Store.AppendBillingHour(ctx, job, step, rows, channelRows, anomalies, cursor, int64(len(logs))); e != nil {
			return e
		}
		if len(logs) < BillingPageSize {
			break
		}
	}
	if err = r.Store.CompleteBillingStep(ctx, job, step, processed, abnormal); err != nil {
		return err
	}
	latest, err := r.Store.BillingJob(ctx, job.ID)
	if err == nil && latest.CompletedSteps >= latest.TotalSteps {
		if source, ok := r.Source.(SnapshotSource); ok {
			if store, ok := r.Store.(SnapshotStore); ok {
				raw, snapshotErr := source.RatioSnapshot(ctx, job.InstanceID)
				if snapshotErr != nil {
					return snapshotErr
				}
				balances, balanceErr := source.Balances(ctx, job.InstanceID)
				if balanceErr != nil {
					return balanceErr
				}
				for day := dateOnly(job.From); day.Before(job.To); day = day.AddDate(0, 0, 1) {
					if snapshotErr = store.PutBillingRatioSnapshot(ctx, job.InstanceID, day, raw); snapshotErr != nil {
						return snapshotErr
					}
					if snapshotErr = store.PutBillingBalanceSnapshots(ctx, job.InstanceID, day, balances); snapshotErr != nil {
						return snapshotErr
					}
				}
			}
		}
		return r.Store.FinalizeBillingJob(ctx, latest)
	}
	return err
}

func fillAnomalyAmounts(out *AnomalyOrder, log PagedLogRecord, prices []Price, ratio string, at time.Time, useTiers bool) {
	// Unpriced anomalies are still valid anomaly records. MySQL DECIMAL columns
	// cannot accept an empty string, so keep every monetary field numeric even
	// when no matching model price exists.
	out.InputPrice = "0"
	out.OutputPrice = "0"
	out.CachePrice = "0"
	out.CacheWritePrice = "0"
	out.InputAmount = "0"
	out.OutputAmount = "0"
	out.CacheAmount = "0"
	out.CacheWriteAmount = "0"
	out.ReferenceAmount = "0"
	if ratio == "" {
		ratio = "1"
	}
	prompt, completion := int64(0), int64(0)
	if log.PromptTokens.Valid {
		prompt = log.PromptTokens.Int64
	}
	if log.CompletionTokens.Valid {
		completion = log.CompletionTokens.Int64
	}
	priceContext := prompt
	if !useTiers {
		priceContext = 0
	}
	price, ok := SelectPrice(prices, at, priceContext)
	if !ok {
		return
	}
	out.InputPrice, _ = multipliedDecimal(price.Input, ratio)
	out.OutputPrice, _ = multipliedDecimal(price.Output, ratio)
	out.CachePrice, _ = multipliedDecimal(price.Cache, ratio)
	out.CacheWritePrice, _ = multipliedDecimal(decimalOrZero(price.CacheWrite), ratio)
	zeroPrice := Price{Input: "0", Output: "0", Cache: "0", CacheWrite: "0"}
	inputPrice := zeroPrice
	inputPrice.Input = price.Input
	outputPrice := zeroPrice
	outputPrice.Output = price.Output
	cachePrice := zeroPrice
	cachePrice.Cache = price.Cache
	writePrice := zeroPrice
	writePrice.CacheWrite = price.CacheWrite
	input, _ := Amount(Usage{PromptTokens: prompt}, inputPrice, ratio)
	output, _ := Amount(Usage{CompletionTokens: completion}, outputPrice, ratio)
	cache, _ := Amount(Usage{CacheTokens: log.CacheTokens}, cachePrice, ratio)
	write, _ := Amount(Usage{CacheWriteTokens: log.CacheWriteTokens, CacheWrite5mTokens: log.CacheWrite5mTokens, CacheWrite1hTokens: log.CacheWrite1hTokens}, writePrice, ratio)
	out.InputAmount = FormatAmount(input, 6)
	out.OutputAmount = FormatAmount(output, 6)
	out.CacheAmount = FormatAmount(cache, 6)
	out.CacheWriteAmount = FormatAmount(write, 6)
	total := new(big.Rat).Add(input, output)
	total.Add(total, cache)
	total.Add(total, write)
	out.ReferenceAmount = FormatAmount(total, 6)
}

func anomalyReasons(log PagedLogRecord, maxContext int64) []string {
	r := []string{}
	inputTokens := sourcePromptTokens(log)
	if !inputTokens.Valid {
		r = append(r, "input_token_missing")
	} else if inputTokens.Int64 <= 0 {
		r = append(r, "input_token_zero")
	}
	if !log.CompletionTokens.Valid {
		r = append(r, "output_token_missing")
	} else if log.CompletionTokens.Int64 <= 0 {
		r = append(r, "output_token_zero")
	}
	contextTokens := log.ContextTokens
	if contextTokens == 0 && log.PromptTokens.Valid {
		contextTokens = log.PromptTokens.Int64
	}
	if log.PromptTokens.Valid && maxContext > 0 && contextTokens > maxContext {
		r = append(r, "context_limit_exceeded")
	}
	return r
}

func sourcePromptTokens(log PagedLogRecord) sql.NullInt64 {
	if log.SourcePromptTokens.Valid {
		return log.SourcePromptTokens
	}
	return log.PromptTokens
}
