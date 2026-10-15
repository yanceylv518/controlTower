package tuning

import (
	"errors"
	"testing"
	"time"
)

func TestHistoryCleanupCadence(t *testing.T) {
	for _, tc := range []struct {
		pending bool
		err     error
		want    time.Duration
	}{
		{true, nil, 10 * time.Second}, {false, nil, time.Minute}, {true, errors.New("slow DB"), time.Minute},
	} {
		if got := historyCleanupInterval(tc.pending, tc.err); got != tc.want {
			t.Fatalf("got %s want %s", got, tc.want)
		}
	}
}
