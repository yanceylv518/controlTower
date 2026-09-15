package voicealert

import (
	"context"
	"errors"
	"log"
	"sync"
	"time"
)

type Repository interface {
	Configs(context.Context) (map[string]Config, error)
	Targets(context.Context) ([]Target, error)
	Snapshot(context.Context, Target, time.Time) ([]int64, time.Time, error)
	Claim(context.Context, Target, time.Time, int64, int64, string, time.Time) (string, error)
	Finish(context.Context, string, Result, time.Time) error
}
type Caller interface {
	Ready() bool
	Call(context.Context, Config, Target, string, string) Result
}
type TargetStatus struct {
	Site      string    `json:"site"`
	UserID    int64     `json:"user_id"`
	State     string    `json:"state"`
	WindowEnd time.Time `json:"window_end"`
	MinTPM    int64     `json:"min_tpm"`
	MaxTPM    int64     `json:"max_tpm"`
	Direction string    `json:"direction"`
}
type Runner struct {
	Store                Repository
	Caller               Caller
	NotificationsAllowed func() (bool, error)
	wake                 chan struct{}
	mu                   sync.Mutex
	status               []TargetStatus
}

func NewRunner(s Repository, c Caller) *Runner {
	return &Runner{Store: s, Caller: c, wake: make(chan struct{}, 1)}
}
func (r *Runner) Notify() {
	select {
	case r.wake <- struct{}{}:
	default:
	}
}
func (r *Runner) Status() []TargetStatus {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]TargetStatus{}, r.status...)
}
func (r *Runner) Run(ctx context.Context) {
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()
	for {
		if err := r.Once(ctx); err != nil && ctx.Err() == nil {
			log.Printf("voice alert evaluation failed (database/configuration); inspect configuration and database health")
		}
		select {
		case <-ctx.Done():
			return
		case <-r.wake:
		case <-ticker.C:
		}
	}
}
func (r *Runner) Once(ctx context.Context) error {
	configs, err := r.Store.Configs(ctx)
	if err != nil {
		return err
	}
	statuses := []TargetStatus{}
	defer func() { r.mu.Lock(); r.status = statuses; r.mu.Unlock() }()
	active := false
	for _, c := range configs {
		if c.Enabled && len(c.Recipients) > 0 {
			active = true
			break
		}
	}
	if !active {
		return nil
	}
	directoryCtx, cancelDirectory := context.WithTimeout(ctx, 15*time.Second)
	targets, err := r.Store.Targets(directoryCtx)
	cancelDirectory()
	if err != nil {
		return err
	}
	if r.NotificationsAllowed != nil {
		allowed, e := r.NotificationsAllowed()
		if e != nil {
			return e
		}
		if !allowed {
			for _, t := range targets {
				if c, ok := configs[t.Site]; !ok || !c.Enabled {
					continue
				}
				statuses = append(statuses, TargetStatus{Site: t.Site, UserID: t.UserID, State: "notifications_disabled"})
			}
			return nil
		}
	}
	for _, t := range targets {
		c, ok := configs[t.Site]
		if !ok || !c.Enabled {
			continue
		}
		subscribed := false
		for _, recipient := range c.Recipients {
			if recipient.Matches(t) {
				subscribed = true
				break
			}
		}
		if !subscribed {
			continue
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		state := TargetStatus{Site: t.Site, UserID: t.UserID}
		queryCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		values, end, e := r.Store.Snapshot(queryCtx, t, time.Now().UTC())
		cancel()
		state.WindowEnd = end
		if e != nil {
			state.State = "coverage_pending"
			if !errors.Is(e, ErrCoverage) {
				state.State = "query_failed"
			}
			statuses = append(statuses, state)
			continue
		}
		hit, low, high, direction := Evaluate(values, c)
		state.MinTPM = low
		state.MaxTPM = high
		state.Direction = direction
		state.State = "normal"
		if !r.Caller.Ready() {
			state.State = "credentials_missing"
			statuses = append(statuses, state)
			continue
		}
		if hit {
			state.State = "cooldown_or_phone_limit"
			for _, recipient := range c.Recipients {
				if !recipient.Matches(t) {
					continue
				}
				callTarget := t
				callTarget.Phone = recipient.Phone
				claimCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
				id, e := r.Store.Claim(claimCtx, callTarget, end, low, high, direction, time.Now().UTC())
				cancel()
				if e != nil {
					return e
				}
				if id == "" {
					continue
				}
				result := r.Caller.Call(ctx, c, callTarget, id, direction)
				state.State = result.Status
				// Persist even if shutdown cancelled the call. Pending/unknown is also
				// already durable if the process is killed before this update.
				finishCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				e = r.Store.Finish(finishCtx, id, result, time.Now().UTC())
				cancel()
				if e != nil {
					return e
				}
			}
		}
		statuses = append(statuses, state)
	}
	return nil
}
