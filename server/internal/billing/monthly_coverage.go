package billing

import (
	"fmt"
	"strings"
	"time"
)

// Date ranges are inclusive business dates, not request query boundaries.
type CoverageRange struct {
	From string `json:"from"`
	To   string `json:"to"`
}

type MonthlyCoverage struct {
	Ranges      []CoverageRange `json:"ranges"`
	Missing     []CoverageRange `json:"missing"`
	CoveredDays int             `json:"covered_days"`
	EmptyDays   int             `json:"empty_days"`
	TotalDays   int             `json:"total_days"`
	Complete    bool            `json:"complete"`
}

// Only explicitly saved daily bills or verified empty dates count as covered.
// No amount, request count, or absence of an aggregate implies an empty day.
func NewMonthlyCoverage(from, to time.Time, days map[string]bool) *MonthlyCoverage {
	c := &MonthlyCoverage{Ranges: []CoverageRange{}, Missing: []CoverageRange{}}
	previous := ""
	for day := from.In(BusinessLocation); day.Before(to); day = day.AddDate(0, 0, 1) {
		key := day.Format("2006-01-02")
		empty, known := days[key]
		c.TotalDays++
		ranges := &c.Missing
		if known {
			c.CoveredDays++
			if empty {
				c.EmptyDays++
			}
			ranges = &c.Ranges
		}
		if len(*ranges) > 0 && (*ranges)[len(*ranges)-1].To == previous {
			(*ranges)[len(*ranges)-1].To = key
		} else {
			*ranges = append(*ranges, CoverageRange{From: key, To: key})
		}
		previous = key
	}
	c.Complete = c.TotalDays > 0 && c.CoveredDays == c.TotalDays
	return c
}

func (c *MonthlyCoverage) RangeLabel() string {
	if c == nil || len(c.Ranges) == 0 {
		return "覆盖日期未确认"
	}
	parts := []string{}
	for _, r := range c.Ranges {
		label := r.From
		if r.From != r.To {
			label += " 至 " + r.To
		}
		parts = append(parts, label)
	}
	return strings.Join(parts, "、")
}

func (c *MonthlyCoverage) Description() string {
	label := c.RangeLabel()
	if c == nil {
		return label
	}
	if !c.Complete {
		label += fmt.Sprintf("（部分月账单，已覆盖 %d/%d 天）", c.CoveredDays, c.TotalDays)
	}
	return label
}

// Older monthly snapshots can prove only the dates actually saved in them.
func MonthlyCoverageFromRows(job Job, rows []StatementAggregateRow) *MonthlyCoverage {
	if job.MonthlyCoverage != nil {
		return job.MonthlyCoverage
	}
	days := map[string]bool{}
	for _, row := range rows {
		days[row.Day.In(BusinessLocation).Format("2006-01-02")] = false
	}
	return NewMonthlyCoverage(job.From, job.To, days)
}
