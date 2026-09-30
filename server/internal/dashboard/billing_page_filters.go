package dashboard

import (
	"controltower/server/internal/billing"
	"strings"
	"time"
)

func billingPageRangeArgs(start, end time.Time, cursor billing.LogCursor) []any {
	// Keep the same-second ID predicate: advancing to cursor + 1 second would
	// lose requests. This redundant lower bound only narrows the index range.
	lower := max(start.Unix(), cursor.CreatedUnix)
	return []any{lower, end.Unix(), cursor.CreatedUnix, cursor.CreatedUnix, cursor.ID}
}

func billingChannelsModelsPageQuery(ids []int64, models map[int64][]string) (string, []any) {
	args := make([]any, 0, len(ids))
	for _, id := range ids {
		args = append(args, id)
	}
	if len(ids) == 0 {
		// Never turn an empty binding into a site-wide scan.
		return billingPageFromFilter(" AND 1=0"), args
	}
	modelCount := 0
	for _, id := range ids {
		modelCount += len(models[id])
	}
	// Large legacy bindings retain the existing Go filter rather than growing
	// an unbounded SQL expression. Falling back affects cost, never inclusion.
	if modelCount == 0 || modelCount+len(ids) > 1000 {
		return billingChannelsLogsPageQuery(len(ids)), args
	}
	terms := make([]string, 0, len(ids))
	for _, id := range ids {
		args = append(args, id)
		if len(models[id]) == 0 {
			terms = append(terms, "l.channel_id=?")
			continue
		}
		marks := strings.TrimSuffix(strings.Repeat("?,", len(models[id])), ",")
		// Go compares the normalized model name exactly, including case and
		// trailing spaces. Use UTF-8 bytes instead of the source's collation;
		// NULL becomes "" just as in billingLogColumns. Values stay parameters.
		terms = append(terms, "(l.channel_id=? AND CAST(CONVERT(COALESCE(l.model_name,'') USING utf8mb4) AS BINARY) IN ("+marks+"))")
		for _, model := range models[id] {
			args = append(args, []byte(model))
		}
	}
	channelMarks := strings.TrimSuffix(strings.Repeat("?,", len(ids)), ",")
	return billingPageFromFilter(" AND l.channel_id IN (" + channelMarks + ") AND (" + strings.Join(terms, " OR ") + ")"), args
}
