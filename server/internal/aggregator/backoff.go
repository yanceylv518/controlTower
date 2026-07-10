package aggregator

import "time"

type Backoff struct {
	Base time.Duration
	Max  time.Duration
}

func NewBackoff(base, max time.Duration) Backoff {
	if base <= 0 {
		base = time.Second
	}
	if max <= 0 || max < base {
		max = base
	}
	return Backoff{Base: base, Max: max}
}

func (b Backoff) Delay(failures int) time.Duration {
	if failures <= 0 {
		return 0
	}
	delay := b.Base
	for i := 1; i < failures; i++ {
		if delay >= b.Max/2 {
			return b.Max
		}
		delay *= 2
	}
	if delay > b.Max {
		return b.Max
	}
	return delay
}
