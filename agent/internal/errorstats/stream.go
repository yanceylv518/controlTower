// Package errorstats keeps optional statistics failures outside the main report
// path. A single atomic snapshot contains both the source watermark and queue.
package errorstats

import (
	"context"
	"controltower/agent/internal/fileatomic"
	"controltower/agent/internal/logcollector"
	es "controltower/internal/errorstats"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

const maxPendingRows = 8000
const maxBatches = 64

type snapshot struct {
	Activated    bool
	InstanceID   string
	StartedAt    time.Time
	LastID       int64
	Dropped      int64
	CoveredUntil *time.Time
	LastLossAt   *time.Time
	// A loss notice is a separate durable priority slot, never evicted with data.
	LossNotice *es.Batch
	Queue      []es.Batch
}

// Activate uses the existing source upper-bound snapshot. Even old records with
// a missing/incorrect timestamp cannot become new statistics during backlog drain.
func (s *Stream) Activate(latestID int64, now time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.state.Activated {
		return nil
	}
	next := s.state
	next.Activated = true
	next.StartedAt = now.UTC()
	next.LastID = max(0, latestID)
	if err := s.save(next); err != nil {
		return err
	}
	s.state = next
	return nil
}

type Stream struct {
	mu             sync.Mutex
	flushMu        sync.Mutex
	path, instance string
	state          snapshot
}
type contextKey struct{}

func WithStream(ctx context.Context, s *Stream) context.Context {
	return context.WithValue(ctx, contextKey{}, s)
}
func FromContext(ctx context.Context) *Stream { s, _ := ctx.Value(contextKey{}).(*Stream); return s }
func Open(path, instance string, now time.Time) (*Stream, error) {
	s := &Stream{path: path, instance: instance}
	data, err := fileatomic.ReadFile(path)
	if err == nil {
		if err = json.Unmarshal(data, &s.state); err != nil {
			return nil, err
		}
		if s.state.InstanceID != instance || s.state.StartedAt.IsZero() || len(s.state.Queue) > maxBatches {
			return nil, fmt.Errorf("invalid statistics state or instance mismatch")
		}
		if s.state.Dropped > 0 && s.state.LastLossAt == nil {
			lost := now.UTC()
			s.state.LastLossAt = &lost
		}
		// Older snapshots may have recorded a loss only in a later data batch.
		// Republish the marker first on restart, including after an upgrade.
		if s.state.LastLossAt != nil && s.state.LossNotice == nil {
			if err = setLossNotice(&s.state, now); err != nil {
				return nil, err
			}
			if err = s.save(s.state); err != nil {
				return nil, err
			}
		}
		return s, nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	s.state.InstanceID = instance
	s.state.StartedAt = now.UTC()
	if err = s.save(s.state); err != nil {
		return nil, err
	}
	return s, nil
}
func (s *Stream) save(v snapshot) error {
	if err := os.MkdirAll(filepath.Dir(s.path), 0700); err != nil {
		return err
	}
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	return fileatomic.WriteFile(s.path, b, 0600)
}

// Record never scans historical logs. Replayed main-report batches are filtered
// using the watermark saved atomically with their statistics.
func (s *Stream) Record(events []logcollector.Event, now time.Time) error {
	return s.RecordCovered(events, now, nil)
}
func (s *Stream) RecordCovered(events []logcollector.Event, now time.Time, covered *time.Time) error {
	return s.record(events, now, covered, nil)
}

// RecordPass also checks whether the main cursor skipped data while statistics
// were unavailable or after a Server acknowledgement moved the main cursor.
func (s *Stream) RecordPass(events []logcollector.Event, now time.Time, covered *time.Time, sourceAfterID int64) error {
	return s.record(events, now, covered, &sourceAfterID)
}
func (s *Stream) record(events []logcollector.Event, now time.Time, covered *time.Time, sourceAfterID *int64) error {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.state.Activated {
		return nil
	}
	next := s.state
	next.Queue = append([]es.Batch(nil), next.Queue...)
	if sourceAfterID != nil && *sourceAfterID > next.LastID {
		lost := now.UTC()
		next.LastLossAt = &lost
		next.LastID = *sourceAfterID
	}
	if covered != nil && !covered.Before(next.StartedAt) && !covered.After(now) && (next.CoveredUntil == nil || covered.After(*next.CoveredUntil)) {
		value := covered.UTC()
		next.CoveredUntil = &value
	}
	type key struct {
		minute        time.Time
		user, channel int64
		model, code   string
	}
	counts := map[key]int64{}
	for _, e := range events {
		if e.SourceLogID <= s.state.LastID {
			continue
		}
		if e.SourceLogID > next.LastID {
			next.LastID = e.SourceLogID
		}
		if e.CreatedAt.Before(next.StartedAt) {
			continue
		}
		code := ""
		if e.LogType == "error" {
			e.ClassifyError()
			code = e.ErrorCode
		} else if e.LogType == "consume" && e.CompletionTokens == 0 {
			code = "zero_output"
		}
		if code == "" {
			continue
		}
		if len(e.ModelName) > 200 || e.CreatedAt.After(now) {
			next.Dropped++
			continue
		}
		k := key{e.CreatedAt.UTC().Truncate(time.Minute), max(0, e.UserID), max(0, e.ChannelID), e.ModelName, code}
		counts[k]++
	}
	if next.Dropped > s.state.Dropped {
		lost := now.UTC()
		next.LastLossAt = &lost
	}
	// A collector page may have up to 5000 distinct tuples. Split it into
	// bounded wire batches instead of dropping everything after tuple 2000.
	pending := []es.Row{}
	for k, n := range counts {
		pending = append(pending, es.Row{Minute: k.minute, UserID: k.user, ChannelID: k.channel, Model: k.model, Code: k.code, Count: n})
	}
	batches := []es.Batch{}
	for offset := 0; offset < len(pending) || len(batches) == 0; offset += es.MaxRows {
		id := make([]byte, 16)
		if _, err := rand.Read(id); err != nil {
			return err
		}
		rows := pending[offset:min(offset+es.MaxRows, len(pending))]
		batches = append(batches, es.Batch{InstanceID: s.instance, ID: hex.EncodeToString(id), StartedAt: next.StartedAt, ObservedAt: now.UTC(), Rows: rows})
	}
	batches[len(batches)-1].CoveredUntil = next.CoveredUntil
	next.Queue = append(next.Queue, batches...)
	rows := 0
	for _, b := range next.Queue {
		rows += len(b.Rows)
	}
	for len(next.Queue) > 1 && (rows > maxPendingRows || len(next.Queue) > maxBatches) {
		for _, r := range next.Queue[0].Rows {
			next.Dropped += r.Count
		}
		rows -= len(next.Queue[0].Rows)
		next.Queue = next.Queue[1:]
	}
	if next.Dropped > s.state.Dropped {
		lost := now.UTC()
		next.LastLossAt = &lost
	}
	for i := len(next.Queue) - len(batches); i < len(next.Queue); i++ {
		if i >= 0 {
			next.Queue[i].Dropped = next.Dropped
			next.Queue[i].LastLossAt = next.LastLossAt
		}
	}
	if next.LastLossAt != nil && (s.state.LastLossAt == nil || !next.LastLossAt.Equal(*s.state.LastLossAt) || next.Dropped != s.state.Dropped) {
		if err := setLossNotice(&next, now); err != nil {
			return err
		}
	}
	if err := s.save(next); err != nil {
		// The caller must not advance the source cursor until this snapshot is durable.
		return err
	}
	s.state = next
	return nil
}
func (s *Stream) Flush(ctx context.Context, send func(context.Context, es.Batch) error) error {
	s.flushMu.Lock()
	defer s.flushMu.Unlock()
	for i := 0; i < 8; i++ {
		s.mu.Lock()
		if len(s.state.Queue) == 0 && s.state.LossNotice == nil {
			s.mu.Unlock()
			return nil
		}
		var batch es.Batch
		notice := s.state.LossNotice != nil
		if notice {
			batch = *s.state.LossNotice
		} else {
			batch = s.state.Queue[0]
		}
		s.mu.Unlock()
		if err := send(ctx, batch); err != nil {
			return err
		}
		s.mu.Lock()
		// Record may have evicted this batch while the network call was in flight.
		if notice && s.state.LossNotice != nil && s.state.LossNotice.ID == batch.ID {
			next := s.state
			next.LossNotice = nil
			if err := s.save(next); err != nil {
				s.mu.Unlock()
				return err
			}
			s.state = next
		} else if !notice && len(s.state.Queue) > 0 && s.state.Queue[0].ID == batch.ID {
			next := s.state
			next.Queue = append([]es.Batch(nil), next.Queue[1:]...)
			if err := s.save(next); err != nil {
				s.mu.Unlock()
				return err
			}
			s.state = next
		}
		s.mu.Unlock()
	}
	return nil
}
func (s *Stream) Run(ctx context.Context, send func(context.Context, es.Batch) error, logf func(string, ...any)) {
	timer := time.NewTimer(0)
	defer timer.Stop()
	delay := 30 * time.Second
	for {
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
			pass, cancel := context.WithTimeout(ctx, 15*time.Second)
			err := s.Flush(pass, send)
			cancel()
			if err != nil {
				logf("optional error statistics upload delayed: %v", err)
				delay = min(5*time.Minute, delay*2)
			} else {
				delay = 30 * time.Second
			}
			timer.Reset(delay)
		}
	}
}

// Publish loss before any retained queued batch can advance Server coverage.
// Replacing an in-flight notice leaves the newer notice pending until acknowledged.
func setLossNotice(next *snapshot, now time.Time) error {
	id := make([]byte, 16)
	if _, err := rand.Read(id); err != nil {
		return err
	}
	next.LossNotice = &es.Batch{InstanceID: next.InstanceID, ID: hex.EncodeToString(id), StartedAt: next.StartedAt, ObservedAt: now.UTC(), LastLossAt: next.LastLossAt, Dropped: next.Dropped}
	return nil
}
