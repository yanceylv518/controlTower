package aggregator

import "sync"

type MetricStore interface {
	Upsert1m(metrics []Metric) error
	Upsert5m(metrics []Metric) error
}

type MemoryMetricStore struct {
	mu        sync.Mutex
	metrics1m map[string]Metric
	metrics5m map[string]Metric
}

func NewMemoryMetricStore() *MemoryMetricStore {
	return &MemoryMetricStore{
		metrics1m: make(map[string]Metric),
		metrics5m: make(map[string]Metric),
	}
}

func (s *MemoryMetricStore) Upsert1m(metrics []Metric) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, metric := range metrics {
		s.metrics1m[metricKey(metric)] = metric
	}
	return nil
}

func (s *MemoryMetricStore) Upsert5m(metrics []Metric) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, metric := range metrics {
		s.metrics5m[metricKey(metric)] = metric
	}
	return nil
}

func (s *MemoryMetricStore) Recent1mMetrics() []Metric {
	s.mu.Lock()
	defer s.mu.Unlock()
	return mapValues(s.metrics1m)
}

func (s *MemoryMetricStore) Recent5mMetrics() []Metric {
	s.mu.Lock()
	defer s.mu.Unlock()
	return mapValues(s.metrics5m)
}

func metricKey(metric Metric) string {
	return metric.InstanceID + "|" + metric.BucketTime.Format("2006-01-02T15:04:05Z07:00") + "|" + metric.DimensionType + "|" + metric.DimensionKey
}

func mapValues(values map[string]Metric) []Metric {
	result := make([]Metric, 0, len(values))
	for _, value := range values {
		result = append(result, value)
	}
	return result
}

