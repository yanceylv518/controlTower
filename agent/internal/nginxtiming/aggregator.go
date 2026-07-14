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
	mu          sync.Mutex
	slowSeconds float64
	current     *bucketState
	queue       []pendingBucket
	warnedFull  bool
}

func NewAggregator(slowSeconds float64) *Aggregator {
	return &Aggregator{slowSeconds: slowSeconds}
}

func (a *Aggregator) Add(entry Entry) {
	a.mu.Lock()
	defer a.mu.Unlock()
	bucketAt := entry.OccurredAt.UTC().Truncate(time.Minute)
	if a.current == nil || !a.current.bucket.BucketAt.Equal(bucketAt) {
		if a.current != nil {
			a.enqueue(a.closeCurrent())
		}
		a.current = &bucketState{bucket: Bucket{BucketAt: bucketAt}}
	}
	s := a.current
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
	if a.current != nil && a.current.bucket.BucketAt.Before(now.UTC().Truncate(time.Minute)) {
		a.enqueue(a.closeCurrent())
		a.current = nil
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

func (a *Aggregator) closeCurrent() pendingBucket {
	s := a.current
	s.bucket.RTP50, s.bucket.RTP95, s.bucket.RTMax = stats(s.rt)
	s.bucket.UHTP50, s.bucket.UHTP95, s.bucket.UHTMax = stats(s.uht)
	s.bucket.TransferP50, s.bucket.TransferP95, s.bucket.TransferMax = stats(s.transfer)
	return pendingBucket{bucket: s.bucket, samples: append([]SlowSample(nil), s.samples...)}
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
