package samples

import (
	"controltower/agent/internal/logcollector"
	"controltower/agent/internal/reporter"
)

func Select(events []logcollector.Event, limit int, slowThresholdSeconds float64) []reporter.LogSamplePayload {
	if limit <= 0 || len(events) == 0 {
		return nil
	}
	samples := make([]reporter.LogSamplePayload, 0, limit)
	seen := map[string]struct{}{}
	add := func(kind string, event logcollector.Event) bool {
		key := kind + ":" + int64Key(event.SourceLogID)
		if _, ok := seen[key]; ok {
			return len(samples) >= limit
		}
		seen[key] = struct{}{}
		samples = append(samples, ToPayload(kind, event))
		return len(samples) >= limit
	}
	for _, event := range events {
		if event.LogType == "error" {
			if add("error", event) {
				return samples
			}
		}
	}
	for _, event := range events {
		if slowThresholdSeconds > 0 && event.UseTime >= slowThresholdSeconds {
			if add("slow", event) {
				return samples
			}
		}
	}
	return samples
}

func ToPayload(kind string, event logcollector.Event) reporter.LogSamplePayload {
	return reporter.LogSamplePayload{
		SampleKind:        kind,
		SourceLogID:       event.SourceLogID,
		CreatedAt:         event.CreatedAt,
		LogType:           event.LogType,
		UserID:            event.UserID,
		Username:          event.Username,
		ChannelID:         event.ChannelID,
		ModelName:         event.ModelName,
		TokenID:           event.TokenID,
		TokenName:         event.TokenName,
		PromptTokens:      event.PromptTokens,
		CompletionTokens:  event.CompletionTokens,
		TotalTokens:       event.TotalTokens,
		Quota:             event.Quota,
		UseTime:           event.UseTime,
		IsStream:          event.IsStream,
		Group:             event.Group,
		RequestID:         event.RequestID,
		UpstreamRequestID: event.UpstreamRequestID,
		ErrorSummary:      event.ErrorSummary,
		CacheTokens:       event.CacheTokens,
		CacheFieldPresent: event.CacheFieldPresent,
	}
}

func int64Key(value int64) string {
	if value == 0 {
		return "0"
	}
	negative := value < 0
	if negative {
		value = -value
	}
	var digits [20]byte
	i := len(digits)
	for value > 0 {
		i--
		digits[i] = byte('0' + value%10)
		value /= 10
	}
	if negative {
		i--
		digits[i] = '-'
	}
	return string(digits[i:])
}
