package billing

import (
	"fmt"
	"math/big"
	"strconv"
)

const PricingSourceNewAPI = "newapi"
const PricingSourceRecalculate = "recalculate"

// MultimediaUsage preserves the input/output direction of source usage.
// It is descriptive only: these counts never replace the logged charge.
type MultimediaUsage struct {
	ImageInputTokens  int64 `json:"image_input_tokens"`
	ImageOutputTokens int64 `json:"image_output_tokens"`
	AudioInputTokens  int64 `json:"audio_input_tokens"`
	AudioOutputTokens int64 `json:"audio_output_tokens"`
}

func (u *MultimediaUsage) Add(v MultimediaUsage) {
	u.ImageInputTokens += v.ImageInputTokens
	u.ImageOutputTokens += v.ImageOutputTokens
	u.AudioInputTokens += v.AudioInputTokens
	u.AudioOutputTokens += v.AudioOutputTokens
}

func (j Job) UsesNewAPICharge() bool { return j.PricingSource == PricingSourceNewAPI }

// StatementLogCharge does not evaluate prices, expressions or ratios in source
// mode. QuotaPerUnit only converts NewAPI's integer quota into currency units.
func StatementLogCharge(job Job, log PagedLogRecord, quotaPerUnit string) (LogChargeVerification, string, error) {
	if !job.UsesNewAPICharge() {
		v, reason := VerifyLogChargeReason(log, quotaPerUnit)
		return v, reason, nil
	}
	qpu, err := decimalRat(quotaPerUnit)
	if err != nil || qpu.Sign() <= 0 {
		return LogChargeVerification{}, "", fmt.Errorf("invalid QuotaPerUnit")
	}
	amount := new(big.Rat).Quo(big.NewRat(log.Quota, 1), qpu)
	return LogChargeVerification{
		Charge:          LogCharge{Mode: "newapi", Total: amount.FloatString(12)},
		CalculatedQuota: log.Quota, LoggedQuota: log.Quota,
	}, "", nil
}

// Display usage is separate from the historical pricing lanes. The source
// adapter already subtracts image input and cache from PromptTokens; audio is
// still in that lane. Keep pricing unchanged when the user requests a rebuild.
func statementDisplayUsage(log PagedLogRecord) (int64, int64, MultimediaUsage) {
	u := MultimediaUsage{max(0, log.ImageInputTokens), max(0, log.ImageOutputTokens), max(0, log.AudioInputTokens), max(0, log.AudioOutputTokens)}
	return max(0, nullableInt64(log.PromptTokens)-u.AudioInputTokens),
		max(0, max(0, nullableInt64(log.CompletionTokens)-u.ImageOutputTokens)-u.AudioOutputTokens), u
}

func detailCSVHeaders(job Job, headers []string) []string {
	if job.UsageVersion == 0 {
		return headers
	}
	for i, v := range headers {
		if v == "输入 Token" {
			headers[i] = "普通输入 Token"
		}
		if v == "输出 Token" {
			headers[i] = "普通输出 Token"
		}
	}
	return append(headers, "图像输入 Token", "图像输出 Token", "音频输入 Token", "音频输出 Token")
}
func detailCSVCells(job Job, media *MultimediaUsage, cells []string) []string {
	if job.UsageVersion == 0 {
		return cells
	}
	if media == nil {
		return append(cells, "", "", "", "")
	}
	return append(cells, strconv.FormatInt(media.ImageInputTokens, 10), strconv.FormatInt(media.ImageOutputTokens, 10), strconv.FormatInt(media.AudioInputTokens, 10), strconv.FormatInt(media.AudioOutputTokens, 10))
}
