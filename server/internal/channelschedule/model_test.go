package channelschedule

import (
	"testing"
	"time"
)

func beijingTime(t *testing.T, value string) time.Time {
	t.Helper()
	at, err := time.ParseInLocation("2006-01-02 15:04:05", value, Beijing)
	if err != nil {
		t.Fatal(err)
	}
	return at.UTC()
}

func TestSchedulePositionAndPreview(t *testing.T) {
	for _, tc := range []struct {
		name               string
		days               []int
		windows            []Window
		at, boundary, next string
		allowed            bool
		nextStatus         int
	}{
		{"before start", []int{1, 2, 3, 4, 5, 6, 7}, []Window{{"00:00", "08:00"}}, "2026-10-12 23:59:59", "2026-10-12 08:00:00", "2026-10-13 00:00:00", false, 1},
		{"exact start", []int{1, 2, 3, 4, 5, 6, 7}, []Window{{"00:00", "08:00"}}, "2026-10-13 00:00:00", "2026-10-13 00:00:00", "2026-10-13 08:00:00", true, 2},
		{"exact end", []int{1, 2, 3, 4, 5, 6, 7}, []Window{{"00:00", "08:00"}}, "2026-10-13 08:00:00", "2026-10-13 08:00:00", "2026-10-14 00:00:00", false, 1},
		{"overnight start day", []int{1}, []Window{{"22:00", "08:00"}}, "2026-10-13 07:59:59", "2026-10-12 22:00:00", "2026-10-13 08:00:00", true, 2},
		{"overnight end excluded", []int{1}, []Window{{"22:00", "08:00"}}, "2026-10-13 08:00:00", "2026-10-13 08:00:00", "2026-10-19 22:00:00", false, 1},
		{"cross week", []int{7}, []Window{{"22:00", "08:00"}}, "2026-10-12 00:00:00", "2026-10-11 22:00:00", "2026-10-12 08:00:00", true, 2},
		{"adjacent windows", []int{2}, []Window{{"00:00", "04:00"}, {"04:00", "08:00"}}, "2026-10-13 04:00:00", "2026-10-13 00:00:00", "2026-10-13 08:00:00", true, 2},
		{"overlapping windows", []int{2}, []Window{{"00:00", "05:00"}, {"04:00", "08:00"}}, "2026-10-13 05:00:00", "2026-10-13 00:00:00", "2026-10-13 08:00:00", true, 2},
		{"day gap", []int{5}, []Window{{"22:00", "06:00"}}, "2026-10-13 12:00:00", "2026-10-10 06:00:00", "2026-10-16 22:00:00", false, 1},
		{"last minute", []int{2}, []Window{{"23:59", "00:00"}}, "2026-10-13 23:59:59", "2026-10-13 23:59:00", "2026-10-14 00:00:00", true, 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			config := Config{Enabled: true, Days: tc.days, Windows: tc.windows}
			if err := config.Normalize(); err != nil {
				t.Fatal(err)
			}
			now := beijingTime(t, tc.at)
			p := config.Position(now)
			if p.Allowed != tc.allowed || !p.Boundary.Equal(beijingTime(t, tc.boundary)) || !p.NextAt.Equal(beijingTime(t, tc.next)) || p.NextStatus != tc.nextStatus {
				t.Fatalf("position: %+v", p)
			}
			preview := config.Preview(now)
			if preview.Allowed != p.Allowed || preview.NextAt == nil || !preview.NextAt.Equal(p.NextAt) || preview.NextStatus != p.NextStatus {
				t.Fatalf("preview: %+v", preview)
			}
			if got := config.Position(now.In(time.FixedZone("different", -7*3600))); got != p {
				t.Fatalf("host timezone changed position: %+v", got)
			}
		})
	}
}

func TestScheduleFullWeekAndCrossWeekPreview(t *testing.T) {
	cfg := Config{Days: []int{1, 2, 3, 4, 5, 6, 7}, Windows: []Window{{"00:00", "12:00"}, {"12:00", "00:00"}}}
	if err := cfg.Normalize(); err != nil {
		t.Fatal(err)
	}
	preview := cfg.Preview(beijingTime(t, "2026-10-13 12:00:00"))
	if !preview.Allowed || preview.NextAt != nil || len(preview.Windows) != 1 || preview.Windows[0] != (WeeklyWindow{0, 10080}) {
		t.Fatalf("full week: %+v", preview)
	}
	cfg.Days = []int{7}
	cfg.Windows = []Window{{"22:00", "08:00"}}
	preview = cfg.Preview(beijingTime(t, "2026-10-12 04:00:00"))
	if len(preview.Windows) != 1 || preview.Windows[0] != (WeeklyWindow{9960, 10560}) {
		t.Fatalf("cross week: %+v", preview)
	}
}

func TestScheduleRejectsInvalidConfiguration(t *testing.T) {
	for _, tc := range []struct {
		name   string
		config Config
	}{
		{"no days", Config{Windows: []Window{{"00:00", "08:00"}}}},
		{"duplicate days", Config{Days: []int{1, 1}, Windows: []Window{{"00:00", "08:00"}}}},
		{"invalid weekday", Config{Days: []int{0}, Windows: []Window{{"00:00", "08:00"}}}},
		{"no windows", Config{Days: []int{1}}},
		{"same endpoints", Config{Days: []int{1}, Windows: []Window{{"08:00", "08:00"}}}},
		{"invalid clock", Config{Days: []int{1}, Windows: []Window{{"24:00", "08:00"}}}},
		{"noncanonical clock", Config{Days: []int{1}, Windows: []Window{{"+1:00", "08:00"}}}},
		{"invalid minute", Config{Days: []int{1}, Windows: []Window{{"01:60", "08:00"}}}},
		{"negative revision", Config{Days: []int{1}, Windows: []Window{{"00:00", "08:00"}}, Revision: -1}},
		{"too many windows", Config{Days: []int{1}, Windows: make([]Window, 9)}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := tc.config.Normalize(); err == nil {
				t.Fatal("accepted invalid config")
			}
		})
	}
	config := Config{Days: []int{7, 1}, Windows: []Window{{" 01:00 ", "08:00"}}}
	if err := config.Normalize(); err != nil || config.Days[0] != 1 || config.Windows[0].Start != "01:00" {
		t.Fatalf("normalization: %+v %v", config, err)
	}
}

func TestPendingOpenDoesNotUndoManualOverrides(t *testing.T) {
	now := beijingTime(t, "2026-10-13 04:00:00")
	plan := Schedule{Config: Config{Enabled: true, Days: []int{1, 2, 3, 4, 5, 6, 7}, Windows: []Window{{"00:00", "08:00"}}, Revision: 2}, EffectiveAt: now.Add(-6 * time.Hour)}
	if !plan.PendingOpen(now) {
		t.Fatal("unprocessed opening not pending")
	}
	plan.Execution = &Execution{Revision: 2, Boundary: plan.Position(now).Boundary, Status: "succeeded"}
	if plan.PendingOpen(now) {
		t.Fatal("manual closure after success must not wait for a repeated opening")
	}
	for _, terminal := range []string{"unchanged", "failed", "skipped", "unknown", "superseded"} {
		plan.Execution.Status = terminal
		if plan.PendingOpen(now) {
			t.Fatalf("terminal %s is pending", terminal)
		}
	}
	for _, pending := range []string{"pending", "running", "retry", "waiting_recovery", "waiting_control"} {
		plan.Execution.Status = pending
		if !plan.PendingOpen(now) {
			t.Fatalf("active %s is not pending", pending)
		}
	}
	plan.Execution = nil
	plan.EffectiveAt = now
	if plan.PendingOpen(now) {
		t.Fatal("next-boundary configuration must not promise immediate opening")
	}
	plan.AlignNow = true
	if !plan.PendingOpen(now) {
		t.Fatal("explicit alignment is pending")
	}
	plan.Enabled = false
	if plan.PendingOpen(now) {
		t.Fatal("disabled plan is pending")
	}
}
