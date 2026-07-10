package storage

import (
	"sort"
	"time"
)

const MaxLogSampleQueryLimit = 200

type LogSampleQuery struct {
	InstanceID string
	SampleKind string
	UserID     int64
	ChannelID  int64
	ModelName  string
	LogType    string
	RequestID  string
	StartTime  time.Time
	EndTime    time.Time
	Limit      int
	Offset     int
}

func FilterLogSamples(samples []LogSample, query LogSampleQuery) []LogSample {
	var filtered []LogSample
	for _, sample := range samples {
		if !matchesLogSampleQuery(sample, query) {
			continue
		}
		filtered = append(filtered, sample)
	}

	sort.Slice(filtered, func(i, j int) bool {
		if filtered[i].CreatedAt.Equal(filtered[j].CreatedAt) {
			return filtered[i].SourceLogID > filtered[j].SourceLogID
		}
		return filtered[i].CreatedAt.After(filtered[j].CreatedAt)
	})

	limit := query.Limit
	if limit <= 0 || limit > MaxLogSampleQueryLimit {
		limit = MaxLogSampleQueryLimit
	}
	offset := query.Offset
	if offset < 0 {
		offset = 0
	}
	if offset >= len(filtered) {
		return nil
	}
	end := offset + limit
	if end > len(filtered) {
		end = len(filtered)
	}
	return append([]LogSample(nil), filtered[offset:end]...)
}

func matchesLogSampleQuery(sample LogSample, query LogSampleQuery) bool {
	if query.InstanceID != "" && sample.InstanceID != query.InstanceID {
		return false
	}
	if query.SampleKind != "" && sample.SampleKind != query.SampleKind {
		return false
	}
	if query.UserID > 0 && sample.UserID != query.UserID {
		return false
	}
	if query.ChannelID > 0 && sample.ChannelID != query.ChannelID {
		return false
	}
	if query.ModelName != "" && sample.ModelName != query.ModelName {
		return false
	}
	if query.LogType != "" && sample.LogType != query.LogType {
		return false
	}
	if query.RequestID != "" && sample.RequestID != query.RequestID {
		return false
	}
	if !query.StartTime.IsZero() && sample.CreatedAt.Before(query.StartTime) {
		return false
	}
	if !query.EndTime.IsZero() && sample.CreatedAt.After(query.EndTime) {
		return false
	}
	return true
}
