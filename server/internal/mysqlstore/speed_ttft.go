package mysqlstore

import (
	"controltower/internal/latencyhist"
	"controltower/internal/speedstats"
	"database/sql"
)

func speedTTFTColumns() []string {
	columns := make([]string, 0, latencyhist.BucketCountV2+2)
	for _, c := range ttft2BucketColumns {
		columns = append(columns, "speed_"+c)
	}
	return append(columns, "speed_retry_count", "speed_unknown_count")
}

func speedTTFTAssignments(merge bool) []string {
	var out []string
	for _, c := range speedTTFTColumns() {
		value := "VALUES(" + c + ")"
		if merge {
			value = "IF(" + c + " IS NULL AND VALUES(" + c + ") IS NULL,NULL,COALESCE(" + c + ",0)+COALESCE(VALUES(" + c + "),0))"
		}
		out = append(out, c+" = "+value)
	}
	return out
}

func speedTTFTArgs(s *speedstats.Stats) []any {
	args := make([]any, latencyhist.BucketCountV2+2)
	if s == nil {
		return args
	}
	for i, v := range s.Buckets {
		args[i] = v
	}
	args[latencyhist.BucketCountV2], args[latencyhist.BucketCountV2+1] = s.RetryCount, s.UnknownCount
	return args
}

func speedTTFTFromSQL(values [latencyhist.BucketCountV2 + 2]sql.NullInt64) *speedstats.Stats {
	s := speedstats.New()
	for _, v := range values {
		if !v.Valid {
			return nil
		}
	}
	for i := range s.Buckets {
		s.Buckets[i] = values[i].Int64
	}
	s.RetryCount, s.UnknownCount = values[latencyhist.BucketCountV2].Int64, values[latencyhist.BucketCountV2+1].Int64
	return s
}
