package nginxtiming

import (
	"log"
	"math"
	"sort"
	"sync"
	"time"
)

const maxValuesPerBucket = 10000
const maxSamplesPerBucket = 5
const maxQueuedBuckets = 720
const maxOpenBuckets = 5
const bucketCloseGrace = 5 * time.Second

type Bucket struct {
	BucketAt          time.Time `json:"bucket_at"`
	RequestCount      int64     `json:"request_count"`
	UpstreamCount     int64     `json:"upstream_count"`
	Status4xx         int64     `json:"status_4xx"`
	Status5xx         int64     `json:"status_5xx"`
	Status504         int64     `json:"status_504"`
	RTP50             float64   `json:"rt_p50"`
	RTP95             float64   `json:"rt_p95"`
	RTMax             float64   `json:"rt_max"`
	UHTP50            float64   `json:"uht_p50"`
	UHTP95            float64   `json:"uht_p95"`
	UHTMax            float64   `json:"uht_max"`
	TransferP50       float64   `json:"transfer_p50"`
	TransferP95       float64   `json:"transfer_p95"`
	TransferMax       float64   `json:"transfer_max"`
	BytesTotal        int64     `json:"bytes_total"`
	SlowCount         int64     `json:"slow_count"`
	SlowTTFTCount     int64     `json:"slow_ttft_count"`
	SlowTransferCount int64     `json:"slow_transfer_count"`
}

type SlowSample struct {
	OccurredAt time.Time `json:"occurred_at"`
	Path       string    `json:"path"`
	Status     int       `json:"status"`
	RT         float64   `json:"rt"`
	UHT        float64   `json:"uht"`
	URT        float64   `json:"urt"`
	Bytes      int64     `json:"bytes"`
}

type pendingBucket struct {
	bucket  Bucket
	samples []SlowSample
}

type bucketState struct {
	bucket    Bucket
	rt        []float64
	uht       []float64
	transfer  []float64
	samples   []SlowSample
	discarded int64
}

type Aggregator struct {
	mu            sync.Mutex
	slowSeconds   float64
	open          map[time.Time]*bucketState
	closedThrough time.Time
	forcedClosed  map[time.Time]struct{}
	forcedOrder   []time.Time
	queue         []pendingBucket
	warnedFull    bool
}

func NewAggregator(slowSeconds float64) *Aggregator {
	return &Aggregator{slowSeconds: slowSeconds, open: make(map[time.Time]*bucketState), forcedClosed: make(map[time.Time]struct{})}
}

func (a *Aggregator) Add(entry Entry) {
	a.mu.Lock()
	defer a.mu.Unlock()
	bucketAt := entry.OccurredAt.UTC().Truncate(time.Minute)
	if !a.closedThrough.IsZero() && !bucketAt.After(a.closedThrough) {
		return
	}
	if _, closed := a.forcedClosed[bucketAt]; closed {
		return
	}
	s := a.open[bucketAt]
	if s == nil {
		if len(a.open) >= maxOpenBuckets {
			a.closeOldest()
		}
		s = &bucketState{bucket: Bucket{BucketAt: bucketAt}}
		a.open[bucketAt] = s
	}
	s.bucket.RequestCount++
	s.bucket.BytesTotal += entry.Bytes
	if entry.Status >= 400 && entry.Status < 500 {
		s.bucket.Status4xx++
	}
	if entry.Status == 504 {
		s.bucket.Status504++
	} else if entry.Status >= 500 && entry.Status < 600 {
		s.bucket.Status5xx++
	}
	appendValue := func(values *[]float64, value float64) {
		if len(*values) < maxValuesPerBucket {
			*values = append(*values, value)
		} else {
			s.discarded++
		}
	}
	appendValue(&s.rt, entry.RT)
	if entry.HasUpstream {
		s.bucket.UpstreamCount++
		appendValue(&s.uht, entry.UHT)
		appendValue(&s.transfer, math.Max(0, entry.URT-entry.UHT))
	}
	if entry.RT >= a.slowSeconds {
		s.bucket.SlowCount++
		if entry.HasUpstream && entry.UHT >= entry.RT/2 {
			s.bucket.SlowTTFTCount++
		} else {
			s.bucket.SlowTransferCount++
		}
		s.samples = append(s.samples, SlowSample{OccurredAt: entry.OccurredAt.UTC(), Path: entry.Path, Status: entry.Status, RT: entry.RT, UHT: entry.UHT, URT: entry.URT, Bytes: entry.Bytes})
		sort.Slice(s.samples, func(i, j int) bool { return s.samples[i].RT > s.samples[j].RT })
		if len(s.samples) > maxSamplesPerBucket {
			s.samples = s.samples[:maxSamplesPerBucket]
		}
	}
}

func (a *Aggregator) Flush(now time.Time) {
	a.mu.Lock()
	defer a.mu.Unlock()
	cutoff := now.UTC().Add(-time.Minute - bucketCloseGrace)
	var due []time.Time
	for bucketAt := range a.open {
		if !bucketAt.After(cutoff) {
			due = append(due, bucketAt)
		}
	}
	sort.Slice(due, func(i, j int) bool { return due[i].Before(due[j]) })
	for _, bucketAt := range due {
		a.closeBucket(bucketAt)
	}
	closedThrough := cutoff.Truncate(time.Minute)
	if closedThrough.After(a.closedThrough) {
		a.closedThrough = closedThrough
		kept := a.forcedOrder[:0]
		for _, bucketAt := range a.forcedOrder {
			if bucketAt.After(a.closedThrough) {
				kept = append(kept, bucketAt)
			} else {
				delete(a.forcedClosed, bucketAt)
			}
		}
		a.forcedOrder = kept
	}
}

func (a *Aggregator) Snapshot() ([]Bucket, []SlowSample) {
	a.mu.Lock()
	defer a.mu.Unlock()
	var buckets []Bucket
	var samples []SlowSample
	for _, item := range a.queue {
		buckets = append(buckets, item.bucket)
		samples = append(samples, item.samples...)
	}
	return buckets, samples
}

func (a *Aggregator) Ack(bucketCount int) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if bucketCount > len(a.queue) {
		bucketCount = len(a.queue)
	}
	a.queue = a.queue[bucketCount:]
}

func (a *Aggregator) closeBucket(bucketAt time.Time) {
	s := a.open[bucketAt]
	if s == nil {
		return
	}
	delete(a.open, bucketAt)
	s.bucket.RTP50, s.bucket.RTP95, s.bucket.RTMax = stats(s.rt)
	s.bucket.UHTP50, s.bucket.UHTP95, s.bucket.UHTMax = stats(s.uht)
	s.bucket.TransferP50, s.bucket.TransferP95, s.bucket.TransferMax = stats(s.transfer)
	a.enqueue(pendingBucket{bucket: s.bucket, samples: append([]SlowSample(nil), s.samples...)})
}

func (a *Aggregator) closeOldest() {
	var oldest time.Time
	for bucketAt := range a.open {
		if oldest.IsZero() || bucketAt.Before(oldest) {
			oldest = bucketAt
		}
	}
	if !oldest.IsZero() {
		a.closeBucket(oldest)
		a.forcedClosed[oldest] = struct{}{}
		a.forcedOrder = append(a.forcedOrder, oldest)
		if len(a.forcedOrder) > maxQueuedBuckets {
			delete(a.forcedClosed, a.forcedOrder[0])
			a.forcedOrder = a.forcedOrder[1:]
		}
	}
}

func (a *Aggregator) enqueue(item pendingBucket) {
	if len(a.queue) >= maxQueuedBuckets {
		a.queue = a.queue[1:]
		if !a.warnedFull {
			log.Printf("WARN nginx timing queue full; dropping oldest buckets")
			a.warnedFull = true
		}
	}
	a.queue = append(a.queue, item)
}

func stats(values []float64) (float64, float64, float64) {
	if len(values) == 0 {
		return 0, 0, 0
	}
	copyValues := append([]float64(nil), values...)
	sort.Float64s(copyValues)
	quantile := func(p float64) float64 {
		index := int(math.Ceil(float64(len(copyValues))*p)) - 1
		if index < 0 {
			index = 0
		}
		return copyValues[index]
	}
	return quantile(.5), quantile(.95), copyValues[len(copyValues)-1]
}
