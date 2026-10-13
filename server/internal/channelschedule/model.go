package channelschedule

import (
	"errors"
	"fmt"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"
)

const MaxPerSite = 200
const MaxWindows = 8
const MaxAttempts = 5
const weekMinutes = 7 * 24 * 60

var Beijing = time.FixedZone("UTC+8", 8*60*60)
var ErrConflict = errors.New("channel schedule changed")
var ErrBusy = errors.New("channel control busy")
var ErrUnsupported = errors.New("direct channel control required")
var ErrSuperseded = errors.New("channel schedule execution superseded")
var ErrLimit = errors.New("channel schedule limit reached")
var ErrChannelNotFound = errors.New("schedule channel not found")

type Window struct {
	Start string `json:"start"`
	End   string `json:"end"`
}

type Config struct {
	Enabled           bool       `json:"enabled"`
	Days              []int      `json:"days"`
	Windows           []Window   `json:"windows"`
	Revision          int64      `json:"revision"`
	AlignNow          bool       `json:"align_now"`
	ExpectedUpdatedAt *time.Time `json:"expected_updated_at,omitempty"`
}

type Schedule struct {
	Config
	SiteID           string     `json:"site_id"`
	ChannelID        int64      `json:"channel_id"`
	EffectiveAt      time.Time  `json:"effective_at"`
	UpdatedAt        time.Time  `json:"updated_at"`
	UpdatedBy        string     `json:"updated_by"`
	ClosedBySchedule bool       `json:"closed_by_schedule"`
	Execution        *Execution `json:"execution,omitempty"`
	Allowed          bool       `json:"allowed"`
	NextAt           *time.Time `json:"next_at,omitempty"`
	NextStatus       int        `json:"next_status,omitempty"`
}

type Execution struct {
	ID           string     `json:"id"`
	SiteID       string     `json:"site_id"`
	ChannelID    int64      `json:"channel_id"`
	Revision     int64      `json:"revision"`
	Boundary     time.Time  `json:"boundary"`
	Target       int        `json:"target"`
	Status       string     `json:"status"`
	Attempts     int        `json:"attempts"`
	Error        string     `json:"error"`
	UpdatedAt    time.Time  `json:"updated_at"`
	ExpiresAt    *time.Time `json:"expires_at,omitempty"`
	BeforeStatus int        `json:"before_status"`
}

type Position struct {
	Allowed    bool
	Boundary   time.Time
	NextAt     time.Time
	NextStatus int
}

type WeeklyWindow struct {
	Start int `json:"start_minute"`
	End   int `json:"end_minute"`
}

type Preview struct {
	Allowed    bool           `json:"allowed"`
	NextAt     *time.Time     `json:"next_at,omitempty"`
	NextStatus int            `json:"next_status,omitempty"`
	Windows    []WeeklyWindow `json:"effective_windows"`
}

func (c Config) SameTiming(other Config) bool {
	return c.Enabled == other.Enabled && slices.Equal(c.Days, other.Days) && slices.Equal(c.Windows, other.Windows)
}

type Control struct{ Enabled, Allowed, Closed, Opening bool }

func (s Schedule) PendingOpen(now time.Time) bool {
	position := s.Position(now)
	if !s.Enabled || !position.Allowed {
		return false
	}
	boundary := position.Boundary
	if boundary.IsZero() || boundary.Before(s.EffectiveAt) {
		if !s.AlignNow {
			return false
		}
		boundary = s.EffectiveAt
	}
	return s.Execution == nil || s.Execution.Revision != s.Revision || !s.Execution.Boundary.Equal(boundary) || !Terminal(s.Execution.Status)
}

func Minute(value string) (int, error) {
	if len(value) != 5 || value[2] != ':' {
		return 0, errors.New("时间必须为 HH:MM")
	}
	h, eh := strconv.Atoi(value[:2])
	m, em := strconv.Atoi(value[3:])
	if eh != nil || em != nil || h < 0 || h > 23 || m < 0 || m > 59 || fmt.Sprintf("%02d:%02d", h, m) != value {
		return 0, errors.New("时间必须为有效的 HH:MM")
	}
	return h*60 + m, nil
}

func (c *Config) Normalize() error {
	if c.Revision < 0 || len(c.Days) < 1 || len(c.Days) > 7 || len(c.Windows) < 1 || len(c.Windows) > MaxWindows {
		return errors.New("请选择星期并设置 1–8 个时间窗")
	}
	seen := map[int]bool{}
	for _, day := range c.Days {
		if day < 1 || day > 7 || seen[day] {
			return errors.New("星期必须为不重复的 1–7")
		}
		seen[day] = true
	}
	sort.Ints(c.Days)
	for i := range c.Windows {
		w := &c.Windows[i]
		w.Start = strings.TrimSpace(w.Start)
		w.End = strings.TrimSpace(w.End)
		start, err := Minute(w.Start)
		if err != nil {
			return err
		}
		end, err := Minute(w.End)
		if err != nil {
			return err
		}
		if start == end {
			return errors.New("开始和结束时间不能相同")
		}
	}
	return nil
}

type interval struct{ start, end int }

// 跨天窗口归开始日；先合并整周区间，连续覆盖的窗口不会产生虚假的启停边界。
func (c Config) weekIntervals() []interval {
	var segments []interval
	for _, day := range c.Days {
		for _, w := range c.Windows {
			start, _ := Minute(w.Start)
			end, _ := Minute(w.End)
			if end < start {
				end += 1440
			}
			start += (day - 1) * 1440
			end += (day - 1) * 1440
			if end > weekMinutes {
				segments = append(segments, interval{0, end - weekMinutes})
				end = weekMinutes
			}
			segments = append(segments, interval{start, end})
		}
	}
	sort.Slice(segments, func(i, j int) bool { return segments[i].start < segments[j].start })
	merged := make([]interval, 0, len(segments))
	for _, v := range segments {
		if len(merged) == 0 || v.start > merged[len(merged)-1].end {
			merged = append(merged, v)
		} else if v.end > merged[len(merged)-1].end {
			merged[len(merged)-1].end = v.end
		}
	}
	return merged
}

func (c Config) Position(now time.Time) Position {
	merged := c.weekIntervals()
	local := now.In(Beijing)
	day := (int(local.Weekday()) + 6) % 7
	week := time.Date(local.Year(), local.Month(), local.Day(), 0, 0, 0, 0, Beijing).AddDate(0, 0, -day)
	minute := int(local.Sub(week) / time.Minute)
	p := Position{}
	for _, v := range merged {
		if minute >= v.start && minute < v.end {
			p.Allowed = true
		}
	}
	boundaries := map[int]int{}
	for _, v := range merged {
		boundaries[v.start] = 1
		boundaries[v.end%weekMinutes] = 2
	}
	if len(merged) > 0 && merged[0].start == 0 && merged[len(merged)-1].end == weekMinutes {
		delete(boundaries, 0)
	}
	for boundary, status := range boundaries {
		for offset := -1; offset <= 1; offset++ {
			at := week.Add(time.Duration(boundary+offset*weekMinutes) * time.Minute).UTC()
			if !at.After(now) && at.After(p.Boundary) {
				p.Boundary = at
			}
			if at.After(now) && (p.NextAt.IsZero() || at.Before(p.NextAt)) {
				p.NextAt, p.NextStatus = at, status
			}
		}
	}
	return p
}

func (c Config) Preview(now time.Time) Preview {
	p := c.Position(now)
	result := Preview{Allowed: p.Allowed, NextStatus: p.NextStatus, Windows: []WeeklyWindow{}}
	if !p.NextAt.IsZero() {
		result.NextAt = &p.NextAt
	}
	merged := c.weekIntervals()
	if len(merged) > 1 && merged[0].start == 0 && merged[len(merged)-1].end == weekMinutes {
		merged[len(merged)-1].end += merged[0].end
		merged = merged[1:]
	}
	for _, window := range merged {
		result.Windows = append(result.Windows, WeeklyWindow{Start: window.start, End: window.end})
	}
	return result
}

func (s *Schedule) Describe(now time.Time) {
	p := s.Config.Position(now)
	s.Allowed = p.Allowed
	s.NextStatus = p.NextStatus
	if !p.NextAt.IsZero() {
		s.NextAt = &p.NextAt
	} else {
		s.NextAt = nil
	}
}

func Terminal(status string) bool {
	return status == "succeeded" || status == "unchanged" || status == "skipped" || status == "failed" || status == "unknown" || status == "superseded"
}
