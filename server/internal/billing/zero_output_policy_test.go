package billing

import "testing"

func TestMonthlyZeroOutputPolicy(t *testing.T) {
	for _, tt := range []struct {
		total, known, excluded int64
		want                   string
	}{
		{2, 2, 0, "included"}, {2, 2, 2, "excluded"}, {2, 2, 1, "mixed"},
		{0, 0, 0, "unknown"}, {2, 1, 1, "unknown"},
	} {
		if got := MonthlyZeroOutputPolicy(tt.total, tt.known, tt.excluded); got != tt.want {
			t.Fatalf("%+v: got %s", tt, got)
		}
	}
}
