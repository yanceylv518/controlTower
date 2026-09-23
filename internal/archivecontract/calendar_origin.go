package archivecontract

import "time"

// CalendarOrigin is a read-only observation, not proof of historical coverage.
type CalendarOrigin struct {
	Date       string    `json:"date"`
	Source     string    `json:"source"`
	ObservedAt time.Time `json:"observed_at"`
}

func (o CalendarOrigin) Valid() bool {
	if o.ObservedAt.IsZero() {
		return false
	}
	if o.Source == "empty" {
		return o.Date == ""
	}
	_, err := time.Parse("2006-01-02", o.Date)
	return err == nil && (o.Source == "archive" || o.Source == "source")
}
