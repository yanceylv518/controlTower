package errorclass

import (
	"regexp"
	"strconv"
)

var statusCodePatterns = []*regexp.Regexp{
	regexp.MustCompile(`(?i)status code\s+(\d{3})`),
	regexp.MustCompile(`(?i)status_code[=: ]+(\d{3})`),
	regexp.MustCompile(`(?i)statusCode[=: ]+(\d{3})`),
	regexp.MustCompile(`(?i)"code"\s*:\s*(\d{3})`),
	regexp.MustCompile(`(?i)HTTP\s+(\d{3})`),
}

// ExtractStatusCode returns the first status code found by the documented
// pattern precedence. Values outside the HTTP status-code range are ignored.
func ExtractStatusCode(summary string) (int, bool) {
	for _, pattern := range statusCodePatterns {
		match := pattern.FindStringSubmatch(summary)
		if len(match) != 2 {
			continue
		}
		code, err := strconv.Atoi(match[1])
		if err == nil && code >= 100 && code <= 599 {
			return code, true
		}
	}
	return 0, false
}

// IsUserError deliberately treats unknown/unparseable errors as channel-side
// so new upstream failure shapes remain visible to dispatch and alerting.
func IsUserError(summary string, userCodes map[int]bool) bool {
	code, ok := ExtractStatusCode(summary)
	return ok && userCodes[code]
}
