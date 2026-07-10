package aggregator

import "controltower/server/internal/storage"

type Scheduler struct {
	store MetricStore
}

type RunResult struct {
	Events    int
	Metrics1m int
	Metrics5m int
}

func NewScheduler(store MetricStore) Scheduler {
	return Scheduler{store: store}
}

func (s Scheduler) RunOnce(events []storage.LogEvent) (RunResult, error) {
	result := RunResult{Events: len(events)}
	if len(events) == 0 {
		return result, nil
	}

	metrics1m := Aggregate1m(events)
	metrics5m := Rollup5m(metrics1m)
	if err := s.store.Upsert1m(metrics1m); err != nil {
		return result, err
	}
	if err := s.store.Upsert5m(metrics5m); err != nil {
		return result, err
	}
	result.Metrics1m = len(metrics1m)
	result.Metrics5m = len(metrics5m)
	return result, nil
}

