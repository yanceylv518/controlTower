package storage

const MaxNotificationQueryLimit = 200

type NotificationDeliveryQuery struct {
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
