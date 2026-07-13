package storage

type ChannelCommandQuery struct {
	InstanceID string
	Status     string
	Limit      int
	Offset     int
}

type OperationAuditQuery struct {
	InstanceID string
	Limit      int
	Offset     int
}

func NormalizeCommandPagination(limit, offset int) (int, int) {
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	if offset < 0 {
		offset = 0
	}
	return limit, offset
}
