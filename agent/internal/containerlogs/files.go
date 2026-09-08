package containerlogs

import (
	cl "controltower/internal/containerlog"
	"encoding/json"
	"regexp"
	"strings"
	"time"
)

const fileScanLimit int64 = 64 * 1024 * 1024
const maxLogFiles = 256

var logName = regexp.MustCompile(`(?i)^[a-z0-9_.-]+\.log(?:[.-][a-z0-9_.-]+)?$`)
var stamp = regexp.MustCompile(`\d{4}[-/]\d{2}[-/]\d{2}(?:T|[ ]+(?:-[ ]*)?)\d{2}:\d{2}:\d{2}(?:\.\d+)?(?:Z|[+-]\d{2}:\d{2})?`)

func lineTime(line string, loc *time.Location) (time.Time, bool) {
	line = strings.TrimSpace(line)
	parse := func(v string) (time.Time, bool) {
		if t, e := time.Parse(time.RFC3339Nano, v); e == nil {
			return t, true
		}
		for _, layout := range []string{"2006/01/02 - 15:04:05", "2006/01/02 15:04:05", "2006-01-02 15:04:05", "2006-01-02T15:04:05"} {
			if t, e := time.ParseInLocation(layout, v, loc); e == nil {
				return t, true
			}
		}
		return time.Time{}, false
	}
	if strings.HasPrefix(line, "{") {
		var obj map[string]json.RawMessage
		if json.Unmarshal([]byte(line), &obj) == nil {
			for _, key := range []string{"time", "timestamp", "ts", "created_at"} {
				var value string
				if b, ok := obj[key]; ok && json.Unmarshal(b, &value) == nil {
					if t, ok := parse(value); ok {
						return t, true
					}
				}
			}
		}
		return time.Time{}, false
	}
	at := stamp.FindStringIndex(line)
	if at == nil || at[0] > 32 {
		return time.Time{}, false
	}
	prefix := strings.TrimSpace(line[:at[0]])
	if prefix != "" && !(strings.HasPrefix(prefix, "[") && strings.HasSuffix(prefix, "]")) {
		return time.Time{}, false
	}
	return parse(line[at[0]:at[1]])
}

func newMatcher(q cl.Query) func(string) bool {
	var request, code *regexp.Regexp
	if q.RequestID != "" {
		request = regexp.MustCompile(`(^|[^a-zA-Z0-9_.:-])` + regexp.QuoteMeta(q.RequestID) + `($|[^a-zA-Z0-9_.:-])`)
	}
	if q.ErrorCode != "" {
		code = regexp.MustCompile(`(?i)["']?\b(?:error_code|status_code|status code|status|code)["']?\s*[:=]\s*["']?` + regexp.QuoteMeta(q.ErrorCode) + `(?:["']|$|[^a-zA-Z0-9_.:-])`)
	}
	return func(record string) bool {
		return (q.Keyword == "" || strings.Contains(record, q.Keyword)) && (request == nil || request.MatchString(record)) && (code == nil || code.MatchString(record) || ginCode(record, q.ErrorCode))
	}
}
