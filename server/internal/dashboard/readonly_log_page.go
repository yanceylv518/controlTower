package dashboard

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
)

type readonlyPageCursor struct {
	Time     int64    `json:"t"`
	ID       int64    `json:"i"`
	Previous bool     `json:"p,omitempty"`
	Scope    [32]byte `json:"s"`
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
