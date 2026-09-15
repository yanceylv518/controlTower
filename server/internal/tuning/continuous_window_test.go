package tuning

import (
	"fmt"
	"testing"
	"time"
)

// Filter the same minute bucket timestamps as the SQL store. The regular
// continuousFake returns all metrics and cannot catch window boundary bugs.
type minuteWindowStore struct {
	*continuousFake
	buckets             map[time.Time]int64
	start, end, rateNow time.Time
	rateQueries         int
}

func (f *minuteWindowStore) QueryMetrics(_ string, start, end time.Time) ([]ChannelMetric, error) {
	f.metricsQueries++
	f.start, f.end = start, end
	var n int64
	for at, samples := range f.buckets {
		if !at.Before(start) && at.Before(end) {
			n += samples
		}
	}
	var out []ChannelMetric
	for _, id := range []int64{1, 2} {
		out = append(out, ChannelMetric{ChannelID: id, RequestCount: n, SpeedSamples: n,
			TTFTP50: 1, TTFTP90: 2, TTFTP95: 3, SpeedTTFTP50: 1, SpeedTTFTP90: 2, SpeedTTFTP95: 3})
	}
	return out, nil
}

func (f *minuteWindowStore) QueryCurrentChannelRates(_ string, now time.Time) ([]ChannelMetric, error) {
	f.rateQueries++
	f.rateNow = now
	return []ChannelMetric{{ChannelID: 1, RequestCount: 99, TPM: 1234}}, nil
}

func TestContinuousEvaluationUsesCompleteMinuteWindows(t *testing.T) {
	for name, anchor := range map[string]time.Time{
		"utc":      time.Date(2026, 9, 15, 7, 9, 0, 0, time.UTC),
		"cst":      time.Date(2026, 9, 15, 15, 9, 0, 0, time.FixedZone("CST", 8*3600)),
		"midnight": time.Date(2026, 9, 16, 0, 0, 0, 0, time.UTC),
	} {
		t.Run(name, func(t *testing.T) { checkCompleteMinuteWindows(t, anchor) })
	}
}

func checkCompleteMinuteWindows(t *testing.T, anchor time.Time) {
	for _, minutes := range []int{1, 5, 15} {
		t.Run(fmt.Sprintf("%d_minutes", minutes), func(t *testing.T) {
			f := &minuteWindowStore{continuousFake: &continuousFake{bases: []ChannelBaseValue{
				{ChannelID: 1, ModelName: "m", Models: []string{"m"}, BaseWeight: 100, CurrentWeight: 100, MaxRPM: 90},
				{ChannelID: 2, ModelName: "m", Models: []string{"m"}, BaseWeight: 100, CurrentWeight: 100},
			}}, buckets: map[time.Time]int64{}}
			// Prior completed minutes have six samples each. The latest minute
			// has just one, and must only participate after it has ended.
			for i := -minutes - 1; i <= 2; i++ {
				f.buckets[anchor.Add(time.Duration(i)*time.Minute)] = 6
			}
			f.buckets[anchor.Add(time.Minute)] = 1
			p := DefaultPolicy()
			p.Continuous.WindowMinutes = minutes
			p.Continuous.MinSamples = 5
			p.DispatchModes = map[string]string{"m": "observe"}
			e := NewEngine(f)
			// Include exact boundaries and fractional seconds. Within a minute
			// all ticks use the same buckets.
			for index, offset := range []time.Duration{0, 20 * time.Second, 59*time.Second + 999*time.Millisecond, time.Minute + 20*time.Second, 2 * time.Minute} {
				now := anchor.Add(offset)
				e.evaluateContinuous("i", PolicyRecord{InstanceID: "i", Policy: p}, now, f)
				end := now.Truncate(time.Minute)
				start := end.Add(-time.Duration(minutes) * time.Minute)
				if !f.start.Equal(start) || !f.end.Equal(end) {
					t.Fatalf("tick %s queried [%s,%s), want [%s,%s)", now, f.start, f.end, start, end)
				}
				want := int64(minutes * 6)
				if offset >= 2*time.Minute {
					want -= 5
				}
				s := f.states[1]
				if s.SpeedSamples != want || s.LastObservedRequests != want || s.MetricReady != (want >= 5) {
					t.Fatalf("tick %s: samples=%d requests=%d ready=%v, want %d", now, s.SpeedSamples, s.LastObservedRequests, s.MetricReady, want)
				}
				if !f.rateNow.Equal(now) || !s.UpdatedAt.Equal(now) || s.MetricRPM != 99 || s.MetricTPM != 1234 || !s.CapacityLimited {
					t.Fatalf("live rates or evaluation clock changed: %+v rateNow=%s", s, f.rateNow)
				}
				if f.metricsQueries != index+1 || f.rateQueries != index+1 {
					t.Fatal("extra queries")
				}
			}
		})
	}
}
