// Package errorstats defines the bounded, optional error-log statistics stream.
// Counts are log records (including retry attempts), never final request counts.
package errorstats

import (
	"context"
	"fmt"
	"regexp"
	"time"
)

const MaxRows = 2000

var validCode = regexp.MustCompile(`^(http:[1-5][0-9]{2}|business:[A-Za-z0-9_.-]{1,64}|unknown|zero_output)$`)

type Row struct {
	Minute    time.Time `json:"minute"`
	UserID    int64     `json:"user_id"`
	ChannelID int64     `json:"channel_id"`
	Model     string    `json:"model"`
	Code      string    `json:"code"`
	Count     int64     `json:"count"`
}
type Batch struct {
	InstanceID   string     `json:"instance_id"`
	ID           string     `json:"batch_id"`
	StartedAt    time.Time  `json:"started_at"`
	ObservedAt   time.Time  `json:"observed_at"`
	LastLossAt   *time.Time `json:"last_loss_at,omitempty"`
	CoveredUntil *time.Time `json:"covered_until,omitempty"`
	Dropped      int64      `json:"dropped"`
	Rows         []Row      `json:"rows"`
}

func (b Batch) Validate(now time.Time) error {
	if b.InstanceID == "" || len(b.InstanceID) > 128 || b.ID == "" || len(b.ID) > 128 || b.StartedAt.IsZero() || b.ObservedAt.Before(b.StartedAt) || b.ObservedAt.After(now.Add(5*time.Minute)) || b.Dropped < 0 || len(b.Rows) > MaxRows {
		return fmt.Errorf("invalid error statistics batch")
	}
	for _, r := range b.Rows {
		if r.Minute.Before(b.StartedAt.Truncate(time.Minute)) || r.Minute.After(b.ObservedAt) || !r.Minute.Equal(r.Minute.Truncate(time.Minute)) || r.UserID < 0 || r.ChannelID < 0 || len(r.Model) > 200 || !validCode.MatchString(r.Code) || r.Count <= 0 || r.Count > 10_000_000 {
			return fmt.Errorf("invalid error statistics row")
		}
	}
	if b.CoveredUntil != nil && (b.CoveredUntil.Before(b.StartedAt) || b.CoveredUntil.After(b.ObservedAt)) {
		return fmt.Errorf("invalid statistics coverage")
	}
	if b.LastLossAt != nil && (b.LastLossAt.Before(b.StartedAt) || b.LastLossAt.After(b.ObservedAt)) {
		return fmt.Errorf("invalid statistics loss marker")
	}
	return nil
}

type Sink interface {
	SaveErrorStatistics(context.Context, Batch) error
}
type Query struct {
	Timeline                         bool
	BucketSeconds                    int64
	Details                          bool
	InstanceID, Dimension, Key, Code string
	Since, Until                     time.Time
	Channels                         bool
}
type Count struct {
	Key   string `json:"key"`
	Count int64  `json:"count"`
}
type Point struct {
	Time  time.Time `json:"time"`
	Count int64     `json:"count"`
}
type Bucket struct {
	Time   time.Time        `json:"time"`
	Counts map[string]int64 `json:"counts"`
}
type Result struct {
	Buckets      []Bucket   `json:"buckets"`
	LastLossAt   *time.Time `json:"last_loss_at,omitempty"`
	CoveredUntil *time.Time `json:"covered_until"`
	Since        time.Time  `json:"since"`
	Until        time.Time  `json:"until"`
	TotalErrors  int64      `json:"total_errors"`
	ZeroOutputs  int64      `json:"zero_outputs"`
	StartedAt    *time.Time `json:"started_at"`
	ObservedAt   *time.Time `json:"observed_at"`
	Dropped      int64      `json:"dropped"`
	Codes        []Count    `json:"codes"`
	Trend        []Point    `json:"trend"`
	Channels     []Count    `json:"channels"`
	Models       []Count    `json:"models"`
	Truncated    bool       `json:"truncated"`
}
type Source interface {
	QueryErrorStatistics(context.Context, Query) (Result, error)
}
