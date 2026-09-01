package billing

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"math/big"
	"strings"
	"time"
)

const BillingPageSize = 2000

var ErrVerificationAlreadyExists = errors.New("billing verification already exists")
var ErrStatementDuplicate = errors.New("billing statement already exists")
var ErrStatementQueueFull = errors.New("billing statement queue is full")

// BusinessLocation is explicit because production containers normally run
// in UTC while new-api billing periods are defined in China Standard Time.
var BusinessLocation = time.FixedZone("Asia/Shanghai", 8*60*60)

type PagedLogRecord struct {
	ID, CreatedUnix, UserID, TokenID, ChannelID, CacheTokens, Quota int64
	CacheWriteTokens, CacheWrite5mTokens, CacheWrite1hTokens        int64
	ContextTokens                                                   int64
	UsageSemantic                                                   string
	// Per-request ratios are copied from new-api logs.other. They preserve the
	// pricing that was actually used for this request; an empty value means the
	// legacy log did not record that ratio.
	ModelPrice, ModelRatio, CompletionRatio, CacheRatio, CacheCreationRatio  string
	CacheCreationRatio5m, CacheCreationRatio1h, GroupRatio                   string
	ImageRatio                                                               string
	BillingMode, ExprBase64, MatchedTier, RequestRules                       string
	ImageInputTokens, ImageOutputTokens, AudioInputTokens, AudioOutputTokens int64
	RequestID, UpstreamRequestID, Username, TokenName, ModelName, GroupName  string
	ChannelName                                                              string
	// SourcePromptTokens preserves logs.prompt_tokens before the billing
	// source separates cache reads/writes from ordinary input tokens.
	SourcePromptTokens, PromptTokens, CompletionTokens sql.NullInt64
}

type LogCursor struct{ CreatedUnix, ID int64 }

type PageSource interface {
	LogsPage(context.Context, string, time.Time, time.Time, LogCursor, int) ([]PagedLogRecord, error)
}
type UserPageSource interface {
	DetailedLogsPage(context.Context, string, int64, time.Time, time.Time, LogCursor, int) ([]PagedLogRecord, error)
}
type UpstreamPageSource interface {
	DetailedChannelsLogsPage(context.Context, string, []int64, time.Time, time.Time, LogCursor, int) ([]PagedLogRecord, error)
}
type statementChannelStore interface {
	BillingStatementChannelIDs(context.Context, string) (map[int64]bool, error)
}
type SnapshotSource interface {
	RatioSnapshot(context.Context, string) (string, error)
	Balances(context.Context, string) (map[int64]int64, error)
}
type BillingIndexSource interface {
	ValidateBillingIndexes(context.Context, string, bool) error
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
	ID                string    `json:"id"`
	BillNo            string    `json:"bill_no,omitempty"`
	RequestKey        string    `json:"-"`
	InstanceID        string    `json:"instance_id"`
	JobType           string    `json:"job_type"`
	UserID            int64     `json:"user_id"`
	ExcludeZeroOutput bool      `json:"exclude_zero_output"`
	UserName          string    `json:"user_name,omitempty"`
	UpstreamID        int64     `json:"upstream_id,omitempty"`
	UpstreamName      string    `json:"upstream_name,omitempty"`
	From              time.Time `json:"range_from"`
	To                time.Time `json:"range_to"`
	Status            string    `json:"status"`
	TotalSteps        int       `json:"total_steps"`
	CompletedSteps    int       `json:"completed_steps"`
	AbnormalRows      int64     `json:"abnormal_rows"`
	MismatchRows      int64     `json:"mismatch_rows"`
	BilledRows        int64     `json:"billed_rows"`
	OutputDays        int64     `json:"output_days"`
	OutputLatestDay   string    `json:"output_latest_day,omitempty"`
	ErrorMessage      string    `json:"error_message,omitempty"`
	OutputPath        string    `json:"output_path,omitempty"`
	RequestedBy       string    `json:"requested_by"`
	CreatedAt         time.Time `json:"created_at"`
	UpdatedAt         time.Time `json:"updated_at"`
}

type JobStep struct {
	JobID         string    `json:"job_id"`
	StepNo        int       `json:"step_no"`
	From          time.Time `json:"range_from"`
	To            time.Time `json:"range_to"`
	Status        string    `json:"status"`
	ProcessedRows int64     `json:"processed_rows"`
	AbnormalRows  int64     `json:"abnormal_rows"`
	Attempts      int       `json:"attempts"`
	ErrorMessage  string    `json:"error_message,omitempty"`
	Cursor        LogCursor `json:"-"`
}

type VerificationRow struct {
	UserID                                  int64
	Username, ModelName, GroupName          string
	SourceRows, NormalRows, AbnormalRows    int64
	SourceQuota, NormalQuota, AbnormalQuota int64
}

type VerificationResult struct {
	Day                   string `json:"day"`
	UserID                int64  `json:"user_id"`
	Username              string `json:"username"`
	ModelName             string `json:"model_name"`
	GroupName             string `json:"group_name"`
	SourceRows            int64  `json:"source_rows"`
	VerifiedNormalRows    int64  `json:"verified_normal_rows"`
	BilledNormalRows      int64  `json:"billed_normal_rows"`
	VerifiedAbnormalRows  int64  `json:"verified_abnormal_rows"`
	BilledAbnormalRows    int64  `json:"billed_abnormal_rows"`
	SourceQuota           int64  `json:"source_quota"`
	VerifiedNormalQuota   int64  `json:"verified_normal_quota"`
	BilledNormalQuota     int64  `json:"billed_normal_quota"`
	VerifiedAbnormalQuota int64  `json:"verified_abnormal_quota"`
	BilledAbnormalQuota   int64  `json:"billed_abnormal_quota"`
	Status                string `json:"status"`
}

type VerificationSummary struct {
	SourceRows            int64 `json:"source_rows"`
	VerifiedNormalRows    int64 `json:"verified_normal_rows"`
	BilledNormalRows      int64 `json:"billed_normal_rows"`
	VerifiedAbnormalRows  int64 `json:"verified_abnormal_rows"`
	BilledAbnormalRows    int64 `json:"billed_abnormal_rows"`
	SourceQuota           int64 `json:"source_quota"`
	VerifiedNormalQuota   int64 `json:"verified_normal_quota"`
	BilledNormalQuota     int64 `json:"billed_normal_quota"`
	VerifiedAbnormalQuota int64 `json:"verified_abnormal_quota"`
	BilledAbnormalQuota   int64 `json:"billed_abnormal_quota"`
	MatchedRows           int64 `json:"matched_rows"`
	MismatchedRows        int64 `json:"mismatched_rows"`
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
	TokenID                                                                                        int64     `json:"token_id"`
	TokenName                                                                                      string    `json:"token_name"`
	ChannelID                                                                                      int64     `json:"channel_id"`
	ChannelName                                                                                    string    `json:"channel_name"`
	ModelName                                                                                      string    `json:"model_name"`
	GroupName                                                                                      string    `json:"group_name"`
	PromptTokens, CompletionTokens                                                                 sql.NullInt64
	CacheTokens, CacheWriteTokens, CacheWrite5mTokens, CacheWrite1hTokens, Quota, MaxContextTokens int64
	InputPrice, OutputPrice, CachePrice, CacheWritePrice                                           string
	InputAmount, OutputAmount, CacheAmount, CacheWriteAmount, ReferenceAmount                      string
	ActualAmount                                                                                   string    `json:"actual_amount"`
	Reasons                                                                                        string    `json:"reasons"`
	DetectedAt                                                                                     time.Time `json:"detected_at"`
}

// ReconciliationOrder is billable, but requires an internal review because
// its logged charge cannot be reproduced or differs from the reconstructed one.
type ReconciliationOrder struct {
	JobID, InstanceID, RequestID, UpstreamRequestID                   string
	SourceLogID, CreatedUnix, UserID, TokenID, ChannelID              int64
	Username, TokenName, ChannelName, ModelName                       string
	PromptTokens, CompletionTokens, CacheReadTokens, CacheWriteTokens int64
	CalculatedQuota, LoggedQuota, QuotaDifference                     int64
	CalculatedAmount, LoggedAmount, AmountDifference, Reason          string
	DetectedAt                                                        time.Time
}

type RequestDetail struct {
	InstanceID, JobID, RequestID, UpstreamRequestID, Username, TokenName, ChannelName, ModelName string
	SourceLogID, CreatedUnix, UserID, TokenID, ChannelID                                         int64
	BillDay                                                                                      time.Time
	PromptTokens, CompletionTokens                                                               int64
	CacheReadTokens, CacheWriteTokens                                                            int64
	CacheWrite5mTokens, CacheWrite1hTokens                                                       int64
	Charge                                                                                       LogCharge
	CalculatedQuota, LoggedQuota                                                                 int64
}

type UserDailyFile struct {
	JobID, InstanceID, RelativePath, SHA256 string
	BillDay                                 time.Time
	UserID, FileSize                        int64
	CreatedAt                               time.Time
}

type ChannelDailyFile struct {
	JobID, InstanceID, RelativePath, SHA256 string
	BillDay                                 time.Time
	ChannelID, FileSize                     int64
	CreatedAt                               time.Time
}

type DailyOverview struct {
	InstanceID   string    `json:"instance_id"`
	Day          time.Time `json:"day"`
	UserCount    int64     `json:"user_count"`
	RequestCount int64     `json:"request_count"`
	AnomalyRows  int64     `json:"anomaly_rows"`
	FileCount    int64     `json:"file_count"`
	Amount       string    `json:"amount"`
	ActivatedAt  time.Time `json:"activated_at"`
}

type UserBillDay struct {
	InstanceID       string    `json:"instance_id"`
	JobID            string    `json:"job_id"`
	Day              time.Time `json:"day"`
	UserID           int64     `json:"user_id"`
	Username         string    `json:"username"`
	ModelName        string    `json:"model_name"`
	RequestCount     int64     `json:"request_count"`
	PromptTokens     int64     `json:"prompt_tokens"`
	CompletionTokens int64     `json:"completion_tokens"`
	CacheReadTokens  int64     `json:"cache_read_tokens"`
	CacheWriteTokens int64     `json:"cache_write_tokens"`
	AnomalyRows      int64     `json:"anomaly_rows"`
	Amount           string    `json:"amount"`
	AnomalyAmount    string    `json:"anomaly_amount"`
	ActivatedAt      time.Time `json:"activated_at"`
}

type UserTokenBillDay struct {
	InstanceID       string    `json:"instance_id"`
	JobID            string    `json:"job_id"`
	Day              time.Time `json:"day"`
	UserID           int64     `json:"user_id"`
	Username         string    `json:"username"`
	TokenID          int64     `json:"token_id"`
	TokenName        string    `json:"token_name"`
	ModelName        string    `json:"model_name"`
	RequestCount     int64     `json:"request_count"`
	PromptTokens     int64     `json:"prompt_tokens"`
	CompletionTokens int64     `json:"completion_tokens"`
	CacheReadTokens  int64     `json:"cache_read_tokens"`
	CacheWriteTokens int64     `json:"cache_write_tokens"`
	AnomalyRows      int64     `json:"anomaly_rows"`
	Amount           string    `json:"amount"`
	AnomalyAmount    string    `json:"anomaly_amount"`
	ActivatedAt      time.Time `json:"activated_at"`
}

type JobStore interface {
	CreateBillingJob(context.Context, Job, []JobStep) error
	ClaimBillingStep(context.Context) (Job, JobStep, bool, error)
	BillingJob(context.Context, string) (Job, error)
	ListBillingUserSettings(context.Context, string) (map[int64]UserSetting, error)
	ListBillingModelMetadata(context.Context, string) ([]ModelMetadata, error)
	ListBillingPrices(context.Context, string) ([]PriceRecord, error)
	ListBillingGroupRatios(context.Context, string) ([]GroupRatio, error)
	AppendBillingHour(context.Context, Job, JobStep, []DailyRow, []TokenDailyRow, []ChannelDailyRow, []RequestDetail, []AnomalyOrder, []ReconciliationOrder, LogCursor, int64) error
	CompleteBillingStep(context.Context, Job, JobStep, int64, int64) error
	FailBillingStep(context.Context, Job, JobStep, error) error
	FinalizeBillingJob(context.Context, Job) error
	ClaimBillingPublish(context.Context) (Job, bool, error)
	FailBillingPublish(context.Context, Job, error) error
}

type VerificationJobStore interface {
	VerificationSourceJob(context.Context, string) (Job, error)
	AppendBillingVerificationPage(context.Context, Job, JobStep, []VerificationRow, LogCursor, int64) error
	FinalizeBillingVerification(context.Context, Job, Job) error
}

type anomalyActualAmountStore interface {
	UpdateBillingAnomalyActualAmounts(context.Context, string, string) error
}

type JobUserSettingsSnapshotStore interface {
	BillingUserSettingsForJob(context.Context, string) (map[int64]UserSetting, error)
}

type JobFileGenerator interface {
	GenerateJobFiles(context.Context, Job) error
}

type UserDailyFileStore interface {
	ListBillingRequestDetailGroups(context.Context, string) ([]UserDailyFile, error)
	ListBillingRequestDetails(context.Context, string, time.Time, int64) ([]RequestDetail, error)
	PutBillingUserDailyFile(context.Context, UserDailyFile) error
	ListBillingChannelRequestDetailGroups(context.Context, string) ([]ChannelDailyFile, error)
	ListBillingChannelRequestDetails(context.Context, string, time.Time, int64) ([]RequestDetail, error)
	ListBillingChannelAnomalyDetails(context.Context, string, time.Time, int64) ([]AnomalyOrder, error)
	PutBillingChannelDailyFile(context.Context, ChannelDailyFile) error
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
		local := start.In(BusinessLocation)
		end := time.Date(local.Year(), local.Month(), local.Day()+1, 0, 0, 0, 0, BusinessLocation)
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

func NewVerificationJob(source Job, requestedBy string) (Job, []JobStep, error) {
	job, steps, err := NewJob(source.InstanceID, source.From, source.To, requestedBy)
	if err != nil {
		return Job{}, nil, err
	}
	job.JobType = "verify"
	return job, steps, nil
}

type JobRunner struct {
	Source    PageSource
	Store     JobStore
	Files     JobFileGenerator
	Spool     DetailPageSpool
	Poll      time.Duration
	PagePause time.Duration
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
	if err != nil {
		return false, err
	}
	if !ok {
		job, ok, err = r.Store.ClaimBillingPublish(ctx)
		if err != nil || !ok {
			return ok, err
		}
		if err = r.publishJob(ctx, job); err != nil {
			_ = r.Store.FailBillingPublish(ctx, job, err)
		}
		return true, nil
	}
	if err = r.processStep(ctx, job, step); err != nil {
		_ = r.Store.FailBillingStep(ctx, job, step, err)
		return true, nil
	}
	return true, nil
}

func (r JobRunner) processStep(ctx context.Context, job Job, step JobStep) error {
	if job.JobType == "verify" {
		return r.processVerificationStep(ctx, job, step)
	}
	if source, ok := r.Source.(BillingIndexSource); ok {
		if err := source.ValidateBillingIndexes(ctx, job.InstanceID, job.UserID > 0); err != nil {
			return err
		}
	}
	quotaPerUnit := defaultQuotaPerUnit
	if source, ok := r.Source.(SnapshotSource); ok {
		raw, snapshotErr := source.RatioSnapshot(ctx, job.InstanceID)
		if snapshotErr != nil {
			return snapshotErr
		}
		quotaPerUnit, snapshotErr = quotaPerUnitForReport(raw)
		if snapshotErr != nil {
			return snapshotErr
		}
	}
	metadata, err := r.Store.ListBillingModelMetadata(ctx, job.InstanceID)
	if err != nil {
		return err
	}
	maxByModel := map[string]int64{}
	for _, m := range metadata {
		maxByModel[m.ModelName] = m.MaxContextTokens
	}
	cursor := step.Cursor
	var processed, abnormal int64
	var upstreamChannelIDs []int64
	if job.JobType == "upstream_statement" {
		store, ok := r.Store.(statementChannelStore)
		if !ok {
			return fmt.Errorf("billing statement channel store unavailable")
		}
		allowed, channelErr := store.BillingStatementChannelIDs(ctx, job.ID)
		if channelErr != nil {
			return channelErr
		}
		for id := range allowed {
			upstreamChannelIDs = append(upstreamChannelIDs, id)
		}
	}
	for {
		logs, e := ReadPageWithRetry(ctx, fmt.Sprintf("site=%s page=generate", job.InstanceID), cursor, func() ([]PagedLogRecord, error) {
			if job.UserID > 0 {
				source, ok := r.Source.(UserPageSource)
				if !ok {
					return nil, fmt.Errorf("billing user page source unavailable")
				}
				return source.DetailedLogsPage(ctx, job.InstanceID, job.UserID, step.From, step.To, cursor, BillingPageSize)
			}
			if job.JobType == "upstream_statement" {
				source, ok := r.Source.(UpstreamPageSource)
				if !ok {
					return nil, fmt.Errorf("billing upstream page source unavailable")
				}
				return source.DetailedChannelsLogsPage(ctx, job.InstanceID, upstreamChannelIDs, step.From, step.To, cursor, BillingPageSize)
			}
			return r.Source.LogsPage(ctx, job.InstanceID, step.From, step.To, cursor, BillingPageSize)
		})
		if e != nil {
			return e
		}
		if len(logs) == 0 {
			break
		}
		pageLast := logs[len(logs)-1]
		pageRows := int64(len(logs))
		type key struct {
			user              int64
			day, model, group string
			tier              int64
		}
		acc := map[key]DailyRow{}
		type channelKey struct {
			id                int64
			day, model, group string
			tier              int64
		}
		channelAcc := map[channelKey]ChannelDailyRow{}
		type tokenKey struct {
			user, token, tier int64
			day, model, group string
		}
		tokenAcc := map[tokenKey]TokenDailyRow{}
		anomalies := []AnomalyOrder{}
		mismatches := []ReconciliationOrder{}
		requestDetails := []RequestDetail{}
		for _, log := range logs {
			billDay := dateOnly(time.Unix(log.CreatedUnix, 0))
			billDayKey := billDay.Format("2006-01-02")
			reasons := StatementAnomalyReasons(log, maxByModel[log.ModelName], job.ExcludeZeroOutput)
			verification, pricingReason := VerifyLogChargeReason(log, quotaPerUnit)
			if len(reasons) > 0 {
				item := AnomalyOrder{InstanceID: job.InstanceID, SourceLogID: log.ID, JobID: job.ID, CreatedAt: time.Unix(log.CreatedUnix, 0), RequestID: log.RequestID, UpstreamRequestID: log.UpstreamRequestID, UserID: log.UserID, Username: log.Username, TokenID: log.TokenID, TokenName: log.TokenName, ChannelID: log.ChannelID, ChannelName: log.ChannelName, ModelName: log.ModelName, GroupName: log.GroupName, PromptTokens: SourcePromptTokens(log), CompletionTokens: log.CompletionTokens, CacheTokens: log.CacheTokens, CacheWriteTokens: log.CacheWriteTokens, CacheWrite5mTokens: log.CacheWrite5mTokens, CacheWrite1hTokens: log.CacheWrite1hTokens, Quota: log.Quota, MaxContextTokens: maxByModel[log.ModelName], Reasons: strings.Join(reasons, ","), DetectedAt: time.Now().UTC()}
				fillAnomalyCharge(&item, verification.Charge)
				anomalies = append(anomalies, item)
				continue
			}
			if pricingReason != "" && (job.JobType == "user_statement" || job.JobType == "upstream_statement") {
				mismatches = append(mismatches, NewReconciliationOrder(job, log, verification, pricingReason, quotaPerUnit))
			}
			if pricingReason == PricingReasonIncomplete {
				verification = FallbackLogCharge(log, quotaPerUnit)
			}
			requestDetails = append(requestDetails, RequestDetail{
				InstanceID: job.InstanceID, JobID: job.ID, SourceLogID: log.ID, CreatedUnix: log.CreatedUnix, BillDay: billDay, RequestID: log.RequestID, UpstreamRequestID: log.UpstreamRequestID,
				UserID: log.UserID, Username: log.Username, TokenID: log.TokenID, TokenName: log.TokenName, ChannelID: log.ChannelID, ChannelName: log.ChannelName, ModelName: log.ModelName,
				PromptTokens: nullableInt64(log.PromptTokens), CompletionTokens: nullableInt64(log.CompletionTokens), CacheReadTokens: log.CacheTokens, CacheWriteTokens: log.CacheWriteTokens,
				CacheWrite5mTokens: log.CacheWrite5mTokens, CacheWrite1hTokens: log.CacheWrite1hTokens, Charge: verification.Charge, CalculatedQuota: verification.CalculatedQuota, LoggedQuota: log.Quota,
			})
			tier := int64(0)
			k := key{log.UserID, billDayKey, log.ModelName, log.GroupName, tier}
			row := acc[k]
			row.InstanceID = job.InstanceID
			row.UserID = log.UserID
			row.Username = log.Username
			row.ModelName = log.ModelName
			row.GroupName = log.GroupName
			row.TierFrom = tier
			row.Day = billDay
			row.RequestCount++
			row.PromptTokens += log.PromptTokens.Int64
			row.CompletionTokens += log.CompletionTokens.Int64
			row.CacheTokens += log.CacheTokens
			row.CacheWriteTokens += log.CacheWriteTokens
			row.CacheWrite5mTokens += log.CacheWrite5mTokens
			row.CacheWrite1hTokens += log.CacheWrite1hTokens
			row.Quota += verification.CalculatedQuota
			row.UpdatedAt = time.Now().UTC()
			acc[k] = row
			tk := tokenKey{log.UserID, log.TokenID, tier, billDayKey, log.ModelName, log.GroupName}
			tr := tokenAcc[tk]
			tr.InstanceID, tr.UserID, tr.Username = job.InstanceID, log.UserID, log.Username
			tr.TokenID, tr.TokenName = log.TokenID, log.TokenName
			tr.ModelName, tr.GroupName, tr.TierFrom, tr.Day = log.ModelName, log.GroupName, tier, billDay
			tr.RequestCount++
			tr.PromptTokens += log.PromptTokens.Int64
			tr.CompletionTokens += log.CompletionTokens.Int64
			tr.CacheTokens += log.CacheTokens
			tr.CacheWriteTokens += log.CacheWriteTokens
			tr.CacheWrite5mTokens += log.CacheWrite5mTokens
			tr.CacheWrite1hTokens += log.CacheWrite1hTokens
			tr.Quota += verification.CalculatedQuota
			tr.UpdatedAt = time.Now().UTC()
			tokenAcc[tk] = tr
			ck := channelKey{log.ChannelID, billDayKey, log.ModelName, log.GroupName, tier}
			cr := channelAcc[ck]
			cr.InstanceID = job.InstanceID
			cr.ChannelID = log.ChannelID
			cr.ChannelName = log.ChannelName
			cr.ModelName = log.ModelName
			cr.GroupName = log.GroupName
			cr.TierFrom = tier
			cr.Day = billDay
			cr.RequestCount++
			cr.PromptTokens += log.PromptTokens.Int64
			cr.CompletionTokens += log.CompletionTokens.Int64
			cr.CacheTokens += log.CacheTokens
			cr.CacheWriteTokens += log.CacheWriteTokens
			cr.CacheWrite5mTokens += log.CacheWrite5mTokens
			cr.CacheWrite1hTokens += log.CacheWrite1hTokens
			cr.Quota += verification.CalculatedQuota
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
		tokenRows := make([]TokenDailyRow, 0, len(tokenAcc))
		for _, row := range tokenAcc {
			tokenRows = append(tokenRows, row)
		}
		cursor = LogCursor{pageLast.CreatedUnix, pageLast.ID}
		processed += pageRows
		abnormal += int64(len(anomalies))
		if r.Spool != nil {
			if e = r.Spool.WritePage(ctx, job, step, cursor, requestDetails); e != nil {
				return e
			}
		}
		if e = r.Store.AppendBillingHour(ctx, job, step, rows, tokenRows, channelRows, requestDetails, anomalies, mismatches, cursor, pageRows); e != nil {
			return e
		}
		if len(logs) < BillingPageSize {
			break
		}
		if r.PagePause > 0 {
			timer := time.NewTimer(r.PagePause)
			select {
			case <-ctx.Done():
				timer.Stop()
				return ctx.Err()
			case <-timer.C:
			}
		}
	}
	if err = r.Store.CompleteBillingStep(ctx, job, step, processed, abnormal); err != nil {
		return err
	}
	return nil
}

func (r JobRunner) publishJob(ctx context.Context, job Job) error {
	if source, ok := r.Source.(SnapshotSource); ok {
		if store, ok := r.Store.(SnapshotStore); ok {
			raw, err := source.RatioSnapshot(ctx, job.InstanceID)
			if err != nil {
				return err
			}
			balances, err := source.Balances(ctx, job.InstanceID)
			if err != nil {
				return err
			}
			for day := dateOnly(job.From); day.Before(job.To); day = day.AddDate(0, 0, 1) {
				if err = store.PutBillingRatioSnapshot(ctx, job.InstanceID, day, raw); err != nil {
					return err
				}
				if err = store.PutBillingBalanceSnapshots(ctx, job.InstanceID, day, balances); err != nil {
					return err
				}
			}
			quotaPerUnit, err := quotaPerUnitForReport(raw)
			if err != nil {
				return err
			}
			if anomalyStore, ok := r.Store.(anomalyActualAmountStore); ok {
				if err = anomalyStore.UpdateBillingAnomalyActualAmounts(ctx, job.ID, quotaPerUnit); err != nil {
					return err
				}
			}
		}
	}
	if r.Files != nil {
		if err := r.Files.GenerateJobFiles(ctx, job); err != nil {
			return fmt.Errorf("generate billing files: %w", err)
		}
	}
	if err := r.Store.FinalizeBillingJob(ctx, job); err != nil {
		return err
	}
	if r.Spool != nil {
		if err := r.Spool.RemoveJob(ctx, job); err != nil {
			// The bill is already activated and all checksummed delivery files
			// exist. Staging cleanup is best effort and can be retried by the
			// retention worker without invalidating the bill.
			return nil
		}
	}
	return nil
}

func (r JobRunner) processVerificationStep(ctx context.Context, job Job, step JobStep) error {
	store, ok := r.Store.(VerificationJobStore)
	if !ok {
		return fmt.Errorf("billing verification store unavailable")
	}
	sourceJob, err := store.VerificationSourceJob(ctx, job.ID)
	if err != nil {
		return err
	}
	metadata, err := r.Store.ListBillingModelMetadata(ctx, job.InstanceID)
	if err != nil {
		return err
	}
	maxByModel := map[string]int64{}
	for _, m := range metadata {
		maxByModel[m.ModelName] = m.MaxContextTokens
	}
	cursor := step.Cursor
	var processed, abnormal int64
	for {
		logs, pageErr := ReadPageWithRetry(ctx, fmt.Sprintf("site=%s page=verify", job.InstanceID), cursor, func() ([]PagedLogRecord, error) {
			return r.Source.LogsPage(ctx, job.InstanceID, step.From, step.To, cursor, BillingPageSize)
		})
		if pageErr != nil {
			return pageErr
		}
		if len(logs) == 0 {
			break
		}
		type key struct {
			user         int64
			model, group string
		}
		acc := map[key]VerificationRow{}
		for _, log := range logs {
			k := key{log.UserID, log.ModelName, log.GroupName}
			v := acc[k]
			v.UserID = log.UserID
			v.Username, v.ModelName, v.GroupName = log.Username, log.ModelName, log.GroupName
			v.SourceRows++
			v.SourceQuota += log.Quota
			if len(AnomalyReasons(log, maxByModel[log.ModelName])) > 0 {
				v.AbnormalRows++
				v.AbnormalQuota += log.Quota
				abnormal++
			} else {
				v.NormalRows++
				v.NormalQuota += log.Quota
			}
			acc[k] = v
		}
		rows := make([]VerificationRow, 0, len(acc))
		for _, v := range acc {
			rows = append(rows, v)
		}
		last := logs[len(logs)-1]
		cursor = LogCursor{last.CreatedUnix, last.ID}
		processed += int64(len(logs))
		if err = store.AppendBillingVerificationPage(ctx, job, step, rows, cursor, int64(len(logs))); err != nil {
			return err
		}
		if len(logs) < BillingPageSize {
			break
		}
		if r.PagePause > 0 {
			timer := time.NewTimer(r.PagePause)
			select {
			case <-ctx.Done():
				timer.Stop()
				return ctx.Err()
			case <-timer.C:
			}
		}
	}
	if err = r.Store.CompleteBillingStep(ctx, job, step, processed, abnormal); err != nil {
		return err
	}
	latest, err := r.Store.BillingJob(ctx, job.ID)
	if err != nil {
		return err
	}
	if latest.CompletedSteps >= latest.TotalSteps {
		return store.FinalizeBillingVerification(ctx, latest, sourceJob)
	}
	return nil
}

func fillAnomalyCharge(out *AnomalyOrder, charge LogCharge) {
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
	out.ActualAmount = "0"
	if charge.Total == "" {
		return
	}
	out.InputPrice, out.OutputPrice, out.CachePrice = decimalOrZero(charge.InputPrice), decimalOrZero(charge.OutputPrice), decimalOrZero(charge.CacheReadPrice)
	out.CacheWritePrice = decimalOrZero(charge.CacheWritePrice)
	if out.CacheWritePrice == "0" {
		out.CacheWritePrice = decimalOrZero(charge.CacheWrite5mPrice)
	}
	if out.CacheWritePrice == "0" {
		out.CacheWritePrice = decimalOrZero(charge.CacheWrite1hPrice)
	}
	out.InputAmount, out.OutputAmount, out.CacheAmount = decimalOrZero(charge.InputAmount), decimalOrZero(charge.OutputAmount), decimalOrZero(charge.CacheReadAmount)
	write := new(big.Rat)
	for _, value := range []string{charge.CacheWriteAmount, charge.CacheWrite5mAmount, charge.CacheWrite1hAmount} {
		if parsed, err := decimalRat(decimalOrZero(value)); err == nil {
			write.Add(write, parsed)
		}
	}
	out.CacheWriteAmount = FormatAmount(write, 6)
	out.ReferenceAmount = charge.Total
}

// AnomalyReasons classifies a normalized source log using the same rules as
// billing generation. Callers such as exports must use this instead of
// reimplementing token validation.
func AnomalyReasons(log PagedLogRecord, maxContext int64) []string {
	r := []string{}
	if !log.CompletionTokens.Valid {
		r = append(r, "output_token_missing")
	}
	return r
}

// StatementAnomalyReasons applies the immutable filtering policy selected
// when a statement task was created. Zero-output rows remain normal billable
// orders unless that task explicitly requested their exclusion.
func StatementAnomalyReasons(log PagedLogRecord, maxContext int64, excludeZeroOutput bool) []string {
	r := AnomalyReasons(log, maxContext)
	if excludeZeroOutput && log.CompletionTokens.Valid && log.CompletionTokens.Int64 == 0 {
		r = append(r, "output_token_zero")
	}
	return r
}

// SourcePromptTokens returns logs.prompt_tokens before cache-lane
// normalization. It is the value used to determine whether input was present.
func SourcePromptTokens(log PagedLogRecord) sql.NullInt64 {
	if log.SourcePromptTokens.Valid {
		return log.SourcePromptTokens
	}
	return log.PromptTokens
}

// RequestContextTokens is the total input context used for model-limit checks
// and tier selection. PromptTokens is already normalized to ordinary input.
func RequestContextTokens(log PagedLogRecord) int64 {
	if log.ContextTokens != 0 {
		return log.ContextTokens
	}
	if log.PromptTokens.Valid {
		return log.PromptTokens.Int64
	}
	return 0
}

// FormatBusinessTime renders a source-log timestamp in the billing timezone.
func FormatBusinessTime(unix int64) string {
	return time.Unix(unix, 0).In(BusinessLocation).Format("2006-01-02 15:04:05")
}
