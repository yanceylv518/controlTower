package storage

type AlertQuery struct {
	InstanceID string
	Status     string
	Severity   string
	ActiveOnly bool
	Limit      int
	Offset     int
}

const MaxAlertQueryLimit = 200

func NormalizeAlertPagination(limit int, offset int) (int, int) {
	if limit <= 0 || limit > MaxAlertQueryLimit {
		limit = MaxAlertQueryLimit
	}
	if offset < 0 {
		offset = 0
	}
	return limit, offset
}
