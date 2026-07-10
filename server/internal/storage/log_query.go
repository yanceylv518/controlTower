package storage

import (
	"sort"
	"time"
)

const MaxLogQueryLimit = 200

type LogQuery struct {
	InstanceID string
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

func FilterLogEvents(events []LogEvent, query LogQuery) []LogEvent {
	var filtered []LogEvent
	for _, event := range events {
		if !matchesLogQuery(event, query) {
			continue
		}
		filtered = append(filtered, event)
	}

	sort.Slice(filtered, func(i, j int) bool {
		if filtered[i].CreatedAt.Equal(filtered[j].CreatedAt) {
			return filtered[i].SourceLogID > filtered[j].SourceLogID
		}
		return filtered[i].CreatedAt.After(filtered[j].CreatedAt)
	})

	limit := query.Limit
	if limit <= 0 || limit > MaxLogQueryLimit {
		limit = MaxLogQueryLimit
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
	return append([]LogEvent(nil), filtered[offset:end]...)
}

func matchesLogQuery(event LogEvent, query LogQuery) bool {
	if query.InstanceID != "" && event.InstanceID != query.InstanceID {
		return false
	}
	if query.UserID > 0 && event.UserID != query.UserID {
		return false
	}
	if query.ChannelID > 0 && event.ChannelID != query.ChannelID {
		return false
	}
	if query.ModelName != "" && event.ModelName != query.ModelName {
		return false
	}
	if query.LogType != "" && event.LogType != query.LogType {
		return false
	}
	if query.RequestID != "" && event.RequestID != query.RequestID {
		return false
	}
	if !query.StartTime.IsZero() && event.CreatedAt.Before(query.StartTime) {
		return false
	}
	if !query.EndTime.IsZero() && event.CreatedAt.After(query.EndTime) {
		return false
	}
	return true
}
