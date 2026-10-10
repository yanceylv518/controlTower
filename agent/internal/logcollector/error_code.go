package logcollector

import (
	"controltower/agent/internal/errorclass"
	"encoding/json"
	"regexp"
	"strconv"
	"strings"
)

var namedStatisticsCode = regexp.MustCompile(`(?i)\b(status[_ ]code|http[_ ]status(?:[_ ]code)?|statusCode|error_code|code)["']?\s*[=:\x{ff1a}]\s*["']?([A-Za-z0-9_.+-]{1,80})\b`)

var safeBusinessCode = regexp.MustCompile(`^[A-Za-z0-9_.-]{1,64}$`)

// ClassifyError caches the existing alert classification as well as the new
// display category. Calling it again does not reparse the record.
func (e *Event) ClassifyError() {
	if e.ErrorParsed || e.LogType != "error" {
		return
	}
	e.ErrorParsed = true
	legacy, explicitHTTP := errorclass.ExtractStatusCodes(e.ErrorSummary)
	e.HTTPStatus = legacy
	e.ErrorCode = "unknown"
	if explicitHTTP != 0 {
		e.ErrorCode = "http:" + strconv.Itoa(explicitHTTP)
	} else if legacy != 0 {
		e.ErrorCode = "business:" + strconv.Itoa(legacy)
	}
}

func classifyRowError(e *Event, content, other string) {
	e.ClassifyError()
	if e.LogType != "error" {
		return
	}
	// Only named scalar fields are retained. Error messages and credentials are
	// never copied into the statistics stream. Explicit HTTP fields win.
	var business string
	for _, raw := range []string{content, other} {
		if !strings.HasPrefix(strings.TrimSpace(raw), "{") {
			if len(raw) <= 64*1024 {
				for _, match := range namedStatisticsCode.FindAllStringSubmatch(raw, -1) {
					if !strings.EqualFold(match[1], "error_code") && !strings.EqualFold(match[1], "code") {
						if n, err := strconv.Atoi(match[2]); err == nil && n >= 100 && n <= 599 {
							e.ErrorCode = "http:" + strconv.Itoa(n)
							return
						}
					} else if business == "" && safeBusinessCode.MatchString(match[2]) {
						business = match[2]
					}
				}
			}
			continue
		}
		var m map[string]json.RawMessage
		if len(raw) > 64*1024 || json.Unmarshal([]byte(raw), &m) != nil {
			continue
		}
		objects := []map[string]json.RawMessage{m}
		for depth := 0; depth < 8; depth++ {
			var nested map[string]json.RawMessage
			if json.Unmarshal(objects[len(objects)-1]["error"], &nested) != nil || nested == nil {
				break
			}
			objects = append(objects, nested)
		}
		for _, obj := range objects {
			for _, key := range []string{"status_code", "http_status_code", "statusCode"} {
				value := strings.TrimSpace(string(obj[key]))
				if strings.HasPrefix(value, `"`) {
					_ = json.Unmarshal(obj[key], &value)
				}
				n, _ := strconv.Atoi(value)
				if n >= 100 && n <= 599 {
					e.ErrorCode = "http:" + strconv.Itoa(n)
					return
				}
			}
			for _, key := range []string{"error_code", "code"} {
				if value := scalarCode(obj[key]); value != "" && business == "" {
					business = value
				}
			}
		}
	}
	if business != "" && !strings.HasPrefix(e.ErrorCode, "http:") {
		e.ErrorCode = "business:" + business
	}
}
func scalarCode(raw json.RawMessage) string {
	value := strings.TrimSpace(string(raw))
	if strings.HasPrefix(value, `"`) {
		if json.Unmarshal(raw, &value) != nil {
			return ""
		}
	}
	if !safeBusinessCode.MatchString(value) || value == "null" || value == "true" || value == "false" {
		return ""
	}
	return value
}
