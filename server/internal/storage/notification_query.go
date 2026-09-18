package storage

import "time"

const MaxNotificationQueryLimit = 200

type NotificationDeliveryQuery struct {
	StartTime time.Time
	EndTime   time.Time
	Search    string
	ID        string
	SiteID    string
	AlertID   string
	ChannelID string
	Status    string
	Limit     int
	Offset    int
}

func NormalizeNotificationPagination(limit int, offset int) (int, int) {
	if limit <= 0 || limit > MaxNotificationQueryLimit {
		limit = MaxNotificationQueryLimit
	}
	if offset < 0 {
		offset = 0
	}
	return limit, offset
}
