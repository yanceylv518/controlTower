package storage

import "time"

type NginxTimingBucket struct {
	InstanceID                                                   string
	BucketAt                                                     time.Time
	RequestCount, UpstreamCount, Status4xx, Status5xx, Status504 int64
	RTP50, RTP95, RTMax, UHTP50, UHTP95, UHTMax                  float64
	TransferP50, TransferP95, TransferMax                        float64
	BytesTotal, SlowCount, SlowTTFTCount, SlowTransferCount      int64
}

type NginxSlowSample struct {
	ID           int64
	InstanceID   string
	OccurredAt   time.Time
	Path         string
	Status       int
	RT, UHT, URT float64
	Bytes        int64
}

type NginxTimingQuery struct {
	InstanceID string
	Since      time.Time
}
type NginxSlowSampleQuery struct {
	InstanceID string
	Since      time.Time
	Limit      int
}
