package storage

import (
	"context"
	"errors"
	"math"
	"strings"
)

var ErrRequestRulesConflict = errors.New("request monitor rules conflict")

type RequestMonitorRules struct {
	SiteID          string  `json:"site_id"`
	Version         int64   `json:"version"`
	WindowMinutes   int     `json:"window_minutes"`
	MinRequests     int64   `json:"min_requests"`
	MinSamples      int64   `json:"min_samples"`
	TTFTSeconds     float64 `json:"ttft_seconds"`
	DurationSeconds float64 `json:"duration_seconds"`
	ErrorPercent    float64 `json:"error_percent"`
}

func DefaultRequestMonitorRules() RequestMonitorRules {
	return RequestMonitorRules{WindowMinutes: 5, MinRequests: 100, MinSamples: 20, TTFTSeconds: 10, DurationSeconds: 60, ErrorPercent: 5}
}
func (c RequestMonitorRules) Valid() bool {
	return strings.TrimSpace(c.SiteID) != "" && len(c.SiteID) <= 191 && c.Version >= 0 && c.WindowMinutes >= 1 && c.WindowMinutes <= 30 && c.MinRequests >= 1 && c.MinRequests <= 10000000 && c.MinSamples >= 1 && c.MinSamples <= 10000000 && validRuleNumber(c.TTFTSeconds, 3600) && validRuleNumber(c.DurationSeconds, 3600) && validRuleNumber(c.ErrorPercent, 100)
}
func validRuleNumber(v, max float64) bool {
	return !math.IsNaN(v) && !math.IsInf(v, 0) && v > 0 && v <= max
}

type RequestMonitorRulesStore interface {
	LoadRequestMonitorRules(context.Context) (RequestMonitorRules, error)
	SaveRequestMonitorRules(context.Context, RequestMonitorRules, string) error
}
