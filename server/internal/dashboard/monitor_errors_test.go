package dashboard

import "testing"

func TestMonitorErrorCode(t *testing.T) {
	for _, tc := range []struct{ other, content, want string }{
		{`{"status_code":429}`, `{"code":500}`, "429"},
		{`{"error":{"code":"rate_limit_exceeded"}}`, "", "rate_limit_exceeded"},
		{"", "http status: 502", "502"},
		{"", "failed after 500 seconds", ""},
		{`{"cache_tokens":500}`, "", ""},
		{`{"code":"<script>"}`, "", ""},
		{`{"error_code":"timeout"}`, "", "timeout"},
	} {
		if got := monitorErrorCode(tc.other, tc.content); got != tc.want {
			t.Errorf("%q/%q got %q want %q", tc.other, tc.content, got, tc.want)
		}
	}
}
func TestMonitorErrorFilter(t *testing.T) {
	for _, tc := range []struct {
		kind, value string
		valid       bool
	}{
		{"instance_user", "12", true}, {"instance_channel", "0", false}, {"instance_model", "model:version", true}, {"instance_model", "", false}, {"instance_user", "1 OR 1=1", false}, {"instance_channel_model", "1", false},
	} {
		_, _, ok := monitorErrorFilter(tc.kind, tc.value)
		if ok != tc.valid {
			t.Errorf("%+v: %v", tc, ok)
		}
	}
}

func TestMonitorBucketBoundaries(t *testing.T) {
	for _, tc := range []struct{ timestamp, step, want int64 }{{60, 60, 60}, {119, 60, 60}, {120, 60, 120}, {299, 300, 0}, {300, 300, 300}, {599, 300, 300}} {
		if got := monitorBucketUnix(tc.timestamp, tc.step); got != tc.want {
			t.Fatalf("%+v got %d", tc, got)
		}
	}
}
