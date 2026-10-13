package tuning

import (
	"context"
	"sync"
	"time"
)

// A worker confirmation, not an inference from successful historical probes.
// Persisted snapshots also serve API-only processes; old confirmations expire.
type ProbeRuntime struct {
	WorkerID    string       `json:"-"`
	InstanceID  string       `json:"instance_id"`
	ChannelID   int64        `json:"channel_id"`
	Model       string       `json:"model"`
	Revision    int64        `json:"revision"`
	PolicyAt    time.Time    `json:"policy_at"`
	BaseAt      time.Time    `json:"base_at"`
	ConfirmedAt time.Time    `json:"confirmed_at"`
	Running     bool         `json:"running"`
	Pending     int          `json:"pending"`
	Accelerated int          `json:"accelerated"`
	NextAt      *time.Time   `json:"next_at,omitempty"`
	Last        *ProbeRecord `json:"last,omitempty"`
}
type ProbeRuntimeStore interface {
	StoreProbeRuntime(context.Context, []ProbeRuntime) error
	QueryProbeRuntime(context.Context, string) ([]ProbeRuntime, error)
}
type probeRuntimeBuffer struct {
	mu   sync.Mutex
	rows map[channelKey]ProbeRuntime
}

func newProbeRuntimeBuffer() *probeRuntimeBuffer {
	return &probeRuntimeBuffer{rows: map[channelKey]ProbeRuntime{}}
}
func (b *probeRuntimeBuffer) update(key channelKey, fn func(*ProbeRuntime)) {
	if b == nil {
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	v := b.rows[key]
	fn(&v)
	b.rows[key] = v
}
func (b *probeRuntimeBuffer) snapshots() []ProbeRuntime {
	if b == nil {
		return nil
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	out := make([]ProbeRuntime, 0, len(b.rows))
	for key, v := range b.rows {
		if v.ConfirmedAt.IsZero() {
			continue
		}
		// Stopped entries have no live producer and age out of memory. Durable
		// expired entries are cleaned independently by retention.
		if !v.Running && time.Since(v.ConfirmedAt) > time.Minute {
			delete(b.rows, key)
			continue
		}
		out = append(out, v)
	}
	return out
}
func (e *Engine) flushProbeRuntime(ctx context.Context) {
	s, ok := e.store.(ProbeRuntimeStore)
	if !ok {
		return
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	// Failures leave confirmations unchanged; the reader reports stale state.
	_ = s.StoreProbeRuntime(ctx, e.probeRuntime.snapshots())
}
