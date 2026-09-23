package dashboard

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"math/big"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/go-sql-driver/mysql"
)

// A database regex budget is per match, not per HTTP request. Retrying the
// same regex, or increasing the HTTP timeout, cannot recover error 3699.
func readonlyRegexpLimit(err error) bool {
	var e *mysql.MySQLError
	return errors.As(err, &e) && e.Number == 3699
}

// ICU's POSIX classes are Unicode classes; Go's POSIX classes are ASCII.
// Build the corresponding RE2 classes explicitly instead of silently changing
// word boundaries or accepting 500 followed by a non-ASCII decimal digit.
var readonlyAlphabetic = func() string {
	var b strings.Builder
	b.WriteString(`\p{L}\p{Nl}\p{Nd}`)
	appendRange := func(lo, hi, stride uint32) {
		if stride == 1 && lo != hi {
			fmt.Fprintf(&b, `\x{%x}-\x{%x}`, lo, hi)
			return
		}
		for c := lo; c <= hi; c += stride {
			fmt.Fprintf(&b, `\x{%x}`, c)
		}
	}
	table := unicode.Properties["Other_Alphabetic"]
	for _, r := range table.R16 {
		appendRange(uint32(r.Lo), uint32(r.Hi), uint32(r.Stride))
	}
	for _, r := range table.R32 {
		appendRange(r.Lo, r.Hi, r.Stride)
	}
	return b.String()
}()

type readonlyStatusMatcher struct{ sensitive, folded *regexp.Regexp }

// Cache compiled patterns, never query results. Bound memory even when users
// cycle through all 500 valid codes; active walkers retain their own patterns.
var readonlyStatusMatchers = struct {
	sync.Mutex
	values map[int]readonlyStatusMatcher
}{values: make(map[int]readonlyStatusMatcher)}
var readonlyStatusLigatures = strings.NewReplacer("\ufb05", "st", "\ufb06", "st")

func newReadonlyStatusMatcher(code int) readonlyStatusMatcher {
	readonlyStatusMatchers.Lock()
	cached, ok := readonlyStatusMatchers.values[code]
	readonlyStatusMatchers.Unlock()
	if ok {
		return cached
	}
	pattern := "(?:" + strings.Join(statusCodeQueryPatterns(code), "|") + ")"
	pattern = strings.NewReplacer(
		"[:alnum:]", readonlyAlphabetic,
		"[:digit:]", `\p{Nd}`,
		"[:space:]", `\t-\r \x{85}\x{a0}\x{1680}\x{2000}-\x{200a}\x{2028}\x{2029}\x{202f}\x{205f}\x{3000}`,
	).Replace(pattern)
	compiled := readonlyStatusMatcher{regexp.MustCompile(pattern), regexp.MustCompile("(?i)" + pattern)}
	readonlyStatusMatchers.Lock()
	if len(readonlyStatusMatchers.values) >= 32 {
		readonlyStatusMatchers.values = make(map[int]readonlyStatusMatcher)
	}
	readonlyStatusMatchers.values[code] = compiled
	readonlyStatusMatchers.Unlock()
	return compiled
}

func (m readonlyStatusMatcher) match(text, collation string) bool {
	if collation == "binary" || strings.HasSuffix(collation, "_bin") || strings.Contains(collation, "_cs") {
		return m.sensitive.MatchString(text)
	}
	// ICU full case folding also expands these two ligatures to "st". These
	// are the only multi-rune folds that can occur within our fixed ASCII keys;
	// RE2 handles the remaining simple folds (including long s).
	text = readonlyStatusLigatures.Replace(text)
	return m.folded.MatchString(text)
}

func readonlyStatusCandidates(filters readonlyLogFilters) readonlyLogFilters {
	f := filters
	f.where = strings.Replace(f.where, f.statusClause, "", 1)
	f.args = append([]any(nil), filters.args[:f.statusArgOffset]...)
	f.args = append(f.args, filters.args[f.statusArgOffset+f.statusArgCount:]...)
	// A necessary condition only. Exact matching happens before counting,
	// skipping OFFSET matches, or choosing the next page's cursor.
	f.where += " AND (LOCATE(?,l.content)>0 OR LOCATE(?,l.other)>0)"
	f.args = append(f.args, strconv.Itoa(*f.statusCode), strconv.Itoa(*f.statusCode))
	return f
}

type readonlyRowScanner interface{ Scan(...any) error }

func scanReadonlyLog(row readonlyRowScanner, v *PassthroughLog, content *string, extra ...any) error {
	var created int64
	args := []any{&v.ID, &v.UserID, &created, &v.Type, &v.Username, &v.ModelName, &v.ChannelID, &v.TokenID, &v.TokenName, &v.PromptTokens, &v.CompletionTokens, &v.Quota, &v.UseTime, &v.RequestID, &v.UpstreamRequestID, content, &v.Group, &v.IP, &v.IsStream, &v.Other}
	err := row.Scan(append(args, extra...)...)
	v.CreatedAt = time.Unix(created, 0).UTC()
	return err
}

func projectReadonlyLog(v *PassthroughLog, content string, viewer bool) {
	v.ContentSummary = redactSummary(content)
	v.Content = v.ContentSummary
	v.Channel = v.ChannelID
	v.Fallback, v.FallbackChannels = readonlyLogFallbackInfo(v.Other)
	v.FallbackChecked = v.Fallback
	v.Other = projectReadonlyLogOther(v.Other, viewer)
	if viewer {
		v.ChannelName = ""
	}
}

func readReadonlyLogPage(ctx context.Context, tx *sql.Tx, query string, args []any, viewer bool) ([]PassthroughLog, error) {
	rows, err := tx.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []PassthroughLog{}
	for rows.Next() {
		var v PassthroughLog
		var content string
		if err := scanReadonlyLog(rows, &v, &content); err != nil {
			return nil, err
		}
		projectReadonlyLog(&v, content, viewer)
		items = append(items, v)
	}
	if err := rows.Err(); err != nil {
		return nil, err // Never expose the partial page preceding error 3699.
	}
	return items, rows.Close()
}

// Stream keyset batches on the caller's read-only transaction. No source DDL,
// parameter changes, text truncation, or arbitrary candidate-count cap. The
// existing request deadline still applies; an incomplete scan is an error.
func walkReadonlyStatus(ctx context.Context, tx *sql.Tx, start, end time.Time, filters readonlyLogFilters, cursor *readonlyPageCursor, mode string, visit func(PassthroughLog, string) bool) error {
	const batch = 64
	f := readonlyStatusCandidates(filters)
	matcher := newReadonlyStatusMatcher(*f.statusCode)
	args := append([]any{start.Unix(), end.Unix()}, f.args...)
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		query, pageArgs := readonlyPageSQL(f.where, args, batch-1, 0, cursor)
		if mode != "list" {
			// Count must not depend on decoding quota or unrelated display
			// columns. Rate and quota likewise read only their own numeric facts.
			quota, prompt, completion := "0", "0", "0"
			if mode == "quota" {
				quota = "COALESCE(l.quota,0)"
			}
			if mode == "rate" {
				prompt, completion = "COALESCE(l.prompt_tokens,0)", "COALESCE(l.completion_tokens,0)"
			}
			selectSQL := "SELECT l.id,l.created_at," + quota + "," + prompt + "," + completion + ",COALESCE(l.content,''),COALESCE(l.other,'') FROM logs l WHERE l.created_at>=? AND l.created_at<?"
			query = strings.Replace(query, readonlyLogsListQuery, selectSQL, 1)
		}
		query = strings.Replace(query, " FROM logs l", ",COLLATION(l.content),COLLATION(l.other) FROM logs l", 1)
		rows, err := tx.QueryContext(ctx, query, pageArgs...)
		if err != nil {
			return err
		}
		read, stopped := 0, false
		var last PassthroughLog
		for rows.Next() {
			var v PassthroughLog
			var content, contentCollation, otherCollation string
			var scanErr error
			if mode == "list" {
				scanErr = scanReadonlyLog(rows, &v, &content, &contentCollation, &otherCollation)
			} else {
				var created int64
				scanErr = rows.Scan(&v.ID, &created, &v.Quota, &v.PromptTokens, &v.CompletionTokens, &content, &v.Other, &contentCollation, &otherCollation)
				v.CreatedAt = time.Unix(created, 0).UTC()
			}
			if scanErr != nil {
				rows.Close()
				return scanErr
			}
			read++
			last = v
			if matcher.match(content, contentCollation) || matcher.match(v.Other, otherCollation) {
				if !visit(v, content) {
					stopped = true
					break
				}
			}
		}
		rowErr, closeErr := rows.Err(), rows.Close()
		if rowErr != nil {
			return rowErr
		}
		if closeErr != nil {
			return closeErr
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if stopped || read < batch {
			return nil
		}
		cursor = &readonlyPageCursor{Time: last.CreatedAt.Unix(), ID: last.ID, Previous: cursor != nil && cursor.Previous}
	}
}

func fallbackReadonlyLogPage(ctx context.Context, tx *sql.Tx, start, end time.Time, filters readonlyLogFilters, limit, offset int, cursor *readonlyPageCursor, viewer bool) ([]PassthroughLog, error) {
	if cursor != nil {
		offset = 0
	}
	items := []PassthroughLog{}
	err := walkReadonlyStatus(ctx, tx, start, end, filters, cursor, "list", func(v PassthroughLog, content string) bool {
		if offset > 0 {
			offset--
			return true
		}
		projectReadonlyLog(&v, content, viewer)
		items = append(items, v)
		return len(items) < limit+1
	})
	return items, err
}

func fallbackReadonlyStatusSummary(ctx context.Context, db *sql.DB, start, end time.Time, filters readonlyLogFilters, mode string) (readonlyRawSummary, int64, error) {
	tx, err := db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return readonlyRawSummary{}, 0, err
	}
	defer tx.Rollback()
	var result readonlyRawSummary
	var quota, tokens, term big.Int
	err = walkReadonlyStatus(ctx, tx, start, end, filters, nil, mode, func(v PassthroughLog, _ string) bool {
		result.Count++
		if mode == "quota" {
			quota.Add(&quota, term.SetInt64(v.Quota))
		}
		if mode == "rate" {
			tokens.Add(&tokens, term.SetInt64(v.PromptTokens))
			tokens.Add(&tokens, term.SetInt64(v.CompletionTokens))
		}
		return true
	})
	if err != nil {
		return readonlyRawSummary{}, 0, err
	}
	if !quota.IsInt64() || !tokens.IsInt64() {
		return readonlyRawSummary{}, 0, fmt.Errorf("readonly status summary overflow")
	}
	result.Quota = quota.Int64()
	return result, tokens.Int64(), nil
}
