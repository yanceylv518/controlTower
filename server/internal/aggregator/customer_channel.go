package aggregator

import "time"

// Keep the raw-log ingest path as lean as the Agent's TPM-only aggregation.
type customerChannelBucket struct {
	instance      string
	bucket        time.Time
	user, channel int64
}
