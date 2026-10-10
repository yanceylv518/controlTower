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
	legacy, _ := ExtractStatusCodes(summary)
	return legacy, legacy != 0
}

// ExtractStatusCodes preserves the legacy alert precedence while distinguishing
// an explicit HTTP status from a generic numeric business "code" for display.
// Both are extracted in the same pass through the patterns.
func ExtractStatusCodes(summary string) (legacy, explicitHTTP int) {
	for index, pattern := range statusCodePatterns {
		match := pattern.FindStringSubmatch(summary)
		if len(match) != 2 {
			continue
		}
		code, err := strconv.Atoi(match[1])
		if err == nil && code >= 100 && code <= 599 {
			if legacy == 0 {
				legacy = code
			}
			if index != 3 {
				return legacy, code
			}
		}
	}
	return legacy, 0
}

// IsUserError deliberately treats unknown/unparseable errors as channel-side
// so new upstream failure shapes remain visible to dispatch and alerting.
func IsUserError(summary string, userCodes map[int]bool) bool {
	code, ok := ExtractStatusCode(summary)
	return ok && userCodes[code]
}
