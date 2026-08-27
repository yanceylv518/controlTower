package logcollector

import (
	"encoding/json"
	"regexp"
	"strings"
	"time"
)

type Row struct {
	ID                int64
	CreatedAt         time.Time
	Type              int
	Content           string
	UserID            int64
	Username          string
	ChannelID         int64
	ModelName         string
	TokenID           int64
	TokenName         string
	PromptTokens      int64
	CompletionTokens  int64
	Quota             int64
	UseTime           float64
	IsStream          bool
	Group             string
	RequestID         string
	UpstreamRequestID string
	Other             string
}

type Event struct {
	SourceLogID       int64
	CreatedAt         time.Time
	LogType           string
	UserID            int64
	Username          string
	ChannelID         int64
	ModelName         string
	TokenID           int64
	TokenName         string
	PromptTokens      int64
	CompletionTokens  int64
	TotalTokens       int64
	Quota             int64
	UseTime           float64
	IsStream          bool
	Group             string
	RequestID         string
	UpstreamRequestID string
	ErrorSummary      string
	CacheTokens       *int64
	CacheFieldPresent bool
	CachePromptTokens *int64
	FirstResponseMs   *int64
}

func ConvertRow(row Row) (Event, bool, error) {
	logType, ok := mapLogType(row.Type)
	if !ok {
		return Event{}, false, nil
	}

	cacheTokens, cachePresent, _ := parseCacheTokens(row.Other)
	cachePromptTokens, _ := parseCachePromptTokens(row.Other, row.PromptTokens, cacheTokens)
	firstResponseMs, _ := parseFirstResponseMs(row.Other)

	return Event{
		SourceLogID:       row.ID,
		CreatedAt:         row.CreatedAt,
		LogType:           logType,
		UserID:            row.UserID,
		Username:          row.Username,
		ChannelID:         row.ChannelID,
		ModelName:         row.ModelName,
		TokenID:           row.TokenID,
		TokenName:         row.TokenName,
		PromptTokens:      row.PromptTokens,
		CompletionTokens:  row.CompletionTokens,
		TotalTokens:       row.PromptTokens + row.CompletionTokens,
		Quota:             row.Quota,
		UseTime:           row.UseTime,
		IsStream:          row.IsStream,
		Group:             row.Group,
		RequestID:         row.RequestID,
		UpstreamRequestID: row.UpstreamRequestID,
		ErrorSummary:      summarizeError(row.Content),
		CacheTokens:       cacheTokens,
		CacheFieldPresent: cachePresent,
		CachePromptTokens: cachePromptTokens,
		FirstResponseMs:   firstResponseMs,
	}, true, nil
}

func parseCachePromptTokens(other string, promptTokens int64, cacheTokens *int64) (*int64, error) {
	if strings.TrimSpace(other) == "" {
		return nil, nil
	}
	var data map[string]json.RawMessage
	if err := json.Unmarshal([]byte(other), &data); err != nil {
		return nil, err
	}
	if raw, ok := data["input_tokens_total"]; ok {
		var total int64
		if json.Unmarshal(raw, &total) == nil && total > 0 {
			return &total, nil
		}
	}
	var claude bool
	if raw, ok := data["claude"]; !ok || json.Unmarshal(raw, &claude) != nil || !claude {
		return nil, nil
	}
	total := promptTokens
	if cacheTokens != nil && *cacheTokens > 0 {
		total += *cacheTokens
	}
	if raw, ok := data["cache_creation_tokens"]; ok {
		var creation int64
		if json.Unmarshal(raw, &creation) == nil && creation > 0 {
			total += creation
		}
	}
	if total <= 0 {
		return nil, nil
	}
	return &total, nil
}

func parseFirstResponseMs(other string) (*int64, error) {
	if strings.TrimSpace(other) == "" {
		return nil, nil
	}
	var data map[string]json.RawMessage
	if err := json.Unmarshal([]byte(other), &data); err != nil {
		return nil, err
	}
	raw, ok := data["frt"]
	if !ok {
		return nil, nil
	}
	var value int64
	if err := json.Unmarshal(raw, &value); err != nil {
		return nil, err
	}
	if value <= 0 || value > 3_600_000 {
		return nil, nil
	}
	return &value, nil
}

func mapLogType(value int) (string, bool) {
	switch value {
	case 2:
		return "consume", true
	case 5:
		return "error", true
	default:
		return "", false
	}
}

func parseCacheTokens(other string) (*int64, bool, error) {
	if strings.TrimSpace(other) == "" {
		return nil, false, nil
	}

	var data map[string]json.RawMessage
	if err := json.Unmarshal([]byte(other), &data); err != nil {
		return nil, false, err
	}
	raw, ok := data["cache_tokens"]
	if !ok {
		return nil, false, nil
	}

	var value int64
	if err := json.Unmarshal(raw, &value); err != nil {
		return nil, false, err
	}
	return &value, true, nil
}

func summarizeError(content string) string {
	redacted := redactSensitive(content)
	if len(redacted) <= 300 {
		return redacted
	}
	return redacted[:300]
}

func redactSensitive(content string) string {
	patterns := []*regexp.Regexp{
		regexp.MustCompile(`(?i)authorization:\s*bearer\s+[^\s]+`),
		regexp.MustCompile(`(?i)(api[-_]?key|token|secret|password)[=:]\s*[^\s,;]+`),
		regexp.MustCompile(`sk-[A-Za-z0-9_-]+`),
	}
	result := content
	for _, pattern := range patterns {
		result = pattern.ReplaceAllString(result, "[REDACTED]")
	}
	return result
}
