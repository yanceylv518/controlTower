package archivereader

import (
	"time"
)

// One bounded day and one consistent read: totals do not depend on log pagination.
// Empty output, missing output and type-5 errors remain separate categories.
func anomalySQL(q JobQuery) (string, []any) {
	d, _ := time.ParseInLocation("2006-01-02", q.Date, time.FixedZone("Beijing", 28800))
	query := `SELECT /*+ MAX_EXECUTION_TIME(3000) */ FLOOR((created_at-?)/3600) AS hour,
COUNT(*) AS log_rows,
SUM(CASE WHEN type=2 THEN 1 ELSE 0 END) AS consumption,
SUM(CASE WHEN type=2 AND completion_tokens=0 THEN 1 ELSE 0 END) AS empty_output,
SUM(CASE WHEN type=2 AND completion_tokens IS NULL THEN 1 ELSE 0 END) AS missing_output,
SUM(CASE WHEN type=5 THEN 1 ELSE 0 END) AS error,
SUM(CASE WHEN type=2 AND completion_tokens=0 AND quota>0 THEN 1 ELSE 0 END) AS charged_empty_output
FROM ` + "`logs_" + d.Format("200601") + "` WHERE created_at>=? AND created_at<?"
	args := []any{d.Unix(), d.Unix(), d.AddDate(0, 0, 1).Unix()}
	for _, filter := range []struct{ column, value string }{{"user_id", q.UserID}, {"model_name", q.Model}, {"channel", q.ChannelID}} {
		if filter.value != "" {
			query += " AND " + filter.column + "=?"
			args = append(args, filter.value)
		}
	}
	return query + " GROUP BY hour ORDER BY hour LIMIT 24", args
}
