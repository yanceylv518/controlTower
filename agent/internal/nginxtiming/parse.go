package nginxtiming

import (
	"regexp"
	"strconv"
	"strings"
	"time"
)

var requestPattern = regexp.MustCompile(`"([A-Z]+)\s+([^\s]+)\s+HTTP/[^"]+"`)
var timePattern = regexp.MustCompile(`\[([^\]]+)\]`)
var fieldPattern = regexp.MustCompile(`(?:^|\s)(status|request_id|rt|uct|uht|urt|bytes)=([^\s,]+(?:,\s*[^\s,]+)*)`)

const maxRequestIDLength = 255

type Entry struct {
	OccurredAt  time.Time
	Method      string
	Path        string
	Status      int
	RT          float64
	UCT         float64
	UHT         float64
	URT         float64
	Bytes       int64
	RequestID   string
	HasUpstream bool
}

func ParseLine(line string) (Entry, bool) {
	values := make(map[string]string)
	for _, match := range fieldPattern.FindAllStringSubmatch(line, -1) {
		values[match[1]] = strings.Trim(match[2], `"`)
	}
	status, err := strconv.Atoi(values["status"])
	if err != nil || values["rt"] == "" {
		return Entry{}, false
	}
	rt, ok := sumTiming(values["rt"])
	if !ok {
		return Entry{}, false
	}
	entry := Entry{Status: status, RT: rt}
	entry.RequestID = normalizeRequestID(values["request_id"])
	entry.UCT, _ = sumTiming(values["uct"])
	uht, hasUHT := sumTiming(values["uht"])
	urt, hasURT := sumTiming(values["urt"])
	entry.UHT, entry.URT, entry.HasUpstream = uht, urt, hasUHT && hasURT
	entry.Bytes, _ = strconv.ParseInt(values["bytes"], 10, 64)
	if match := requestPattern.FindStringSubmatch(line); len(match) == 3 {
		entry.Method = match[1]
		entry.Path = strings.SplitN(match[2], "?", 2)[0]
	}
	if match := timePattern.FindStringSubmatch(line); len(match) == 2 {
		entry.OccurredAt, _ = time.Parse("02/Jan/2006:15:04:05 -0700", match[1])
	}
	if entry.OccurredAt.IsZero() {
		entry.OccurredAt = time.Now().UTC()
	}
	return entry, true
}

func normalizeRequestID(value string) string {
	value = strings.TrimSpace(strings.Trim(value, `"`))
	if value == "" || value == "-" || len(value) > maxRequestIDLength {
		return ""
	}
	return value
}

func sumTiming(value string) (float64, bool) {
	if value == "" || value == "-" {
		return 0, false
	}
	var total float64
	seen := false
	for _, part := range strings.Split(value, ",") {
		part = strings.TrimSpace(part)
		if part == "" || part == "-" {
			continue
		}
		parsed, err := strconv.ParseFloat(part, 64)
		if err != nil {
			return 0, false
		}
		total += parsed
		seen = true
	}
	return total, seen
}
