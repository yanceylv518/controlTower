package archivecontract

import (
	"testing"
	"time"
)

func TestCalendarOriginValidation(t *testing.T) {
	for _, source := range []string{"archive", "source"} {
		if !(CalendarOrigin{Date: "2026-06-15", Source: source, ObservedAt: time.Now()}).Valid() {
			t.Fatal(source)
		}
	}
	if !(CalendarOrigin{Source: "empty", ObservedAt: time.Now()}).Valid() {
		t.Fatal("empty")
	}
	for _, o := range []CalendarOrigin{
		{Source: "archive", ObservedAt: time.Now()},
		{Source: "empty", Date: "2026-06-15", ObservedAt: time.Now()},
		{Source: "source", Date: "2026-02-30", ObservedAt: time.Now()},
		{Source: "source", Date: "2026-06-15"},
	} {
		if o.Valid() {
			t.Fatal(o)
		}
	}
}
