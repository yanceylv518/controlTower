package dashboard

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"time"
)

type readonlyPageCursor struct {
	Time     int64    `json:"t"`
	ID       int64    `json:"i"`
	Previous bool     `json:"p,omitempty"`
	Scope    [32]byte `json:"s"`
}

// These strings are part of the existing cursor wire identity. Preserve even
// whitespace: previously issued cursors hash the pre-optimization WHERE/args.
// They are never executed as SQL. Validation and all base filters still use the
// common parser, so site, role, user scope, time and filter isolation are intact.
const readonlyCursorFinalPredicate = ` AND (l.request_id IS NULL OR l.request_id = '' OR NOT EXISTS (
			SELECT 1 FROM logs AS newer_logs
			WHERE newer_logs.request_id = l.request_id
			  AND newer_logs.user_id = l.user_id
			  AND (newer_logs.created_at > l.created_at OR
				(newer_logs.created_at = l.created_at AND newer_logs.id > l.id))
		))`

func appendReadonlyCursorStatusCode(filters *readonlyLogFilters, code int) {
	value := strconv.Itoa(code)
	boundary := "([^[:digit:]]|$)"
	prefix := "(^|[^[:alnum:]_])"
	patterns := []string{
		prefix + "status_code[[:space:]]*[:=][[:space:]]*[\"']?" + value + boundary,
		prefix + "statusCode[[:space:]]*[:=][[:space:]]*[\"']?" + value + boundary,
		prefix + "status[[:space:]]+code[[:space:]]*[:=][[:space:]]*[\"']?" + value + boundary,
		prefix + "error_code[[:space:]]*[:=][[:space:]]*[\"']?" + value + boundary,
		prefix + "[\"']code[\"'][[:space:]]*[:=][[:space:]]*[\"']?" + value + boundary,
		prefix + "HTTP[[:space:]]+" + value + boundary,
	}
	conditions := make([]string, 0, len(patterns))
	for _, pattern := range patterns {
		conditions = append(conditions, "(l.content REGEXP ? OR l.other REGEXP ?)")
		filters.args = append(filters.args, pattern, pattern)
	}
	filters.where += " AND (" + strings.Join(conditions, " OR ") + ")"
}

func readonlyLogCursorScope(site string, viewer bool, values url.Values, ids []int64, start, end time.Time) ([32]byte, error) {
	filters, err := parseReadonlyLogFiltersMode(values, ids, viewer, true)
	if err != nil {
		return [32]byte{}, err
	}
	return readonlyPageScope(site, viewer, filters.where, append([]any{start.Unix(), end.Unix()}, filters.args...)), nil
}

func readonlyPageScope(site string, viewer bool, where string, args []any) [32]byte {
	b, _ := json.Marshal([]any{site, viewer, where, args})
	return sha256.Sum256(b)
}
func parseReadonlyPageCursor(raw string, scope [32]byte) (*readonlyPageCursor, error) {
	if raw == "" {
		return nil, nil
	}
	if len(raw) > 512 {
		return nil, fmt.Errorf("invalid_cursor")
	}
	b, err := base64.RawURLEncoding.DecodeString(raw)
	var c readonlyPageCursor
	if err != nil || json.Unmarshal(b, &c) != nil || c.ID <= 0 || c.Scope != scope {
		return nil, fmt.Errorf("invalid_cursor")
	}
	return &c, nil
}
func readonlyPageToken(item PassthroughLog, previous bool, scope [32]byte) string {
	b, _ := json.Marshal(readonlyPageCursor{Time: item.CreatedAt.Unix(), ID: item.ID, Previous: previous, Scope: scope})
	return base64.RawURLEncoding.EncodeToString(b)
}
func readonlyPageSQL(where string, args []any, limit, offset int, cursor *readonlyPageCursor) (string, []any) {
	args = append([]any(nil), args...)
	order := readonlyLogsListOrder
	if cursor != nil {
		op := "<"
		if cursor.Previous {
			op = ">"
			order = " ORDER BY l.created_at ASC,l.id ASC LIMIT ? OFFSET ?"
		}
		where += " AND (l.created_at " + op + " ? OR (l.created_at = ? AND l.id " + op + " ?))"
		args = append(args, cursor.Time, cursor.Time, cursor.ID)
		offset = 0
	}
	return readonlyLogsListQuery + where + order, append(args, limit+1, offset)
}
