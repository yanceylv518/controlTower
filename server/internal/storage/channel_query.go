package storage

import "time"

type ChannelSnapshotQuery struct {
	InstanceID string
	ChannelID  int64
	LatestOnly bool
	StartTime  time.Time
	EndTime    time.Time
	Limit      int
	Offset     int
}
