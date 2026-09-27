package archivereader

import (
	"strings"
	"testing"
	"time"
)

func TestJobQueryBoundsAndSQL(t *testing.T) {
	base := JobQuery{Site: "site", Kind: "logs", Date: "2026-07-04", Limit: 100}
	for _, mutate := range []func(*JobQuery){
		func(q *JobQuery) { q.Date = "2026-07-04` UNION SELECT" },
		func(q *JobQuery) { q.Date = "2026-02-30" },
		func(q *JobQuery) { q.Limit = 201 },
		func(q *JobQuery) { q.AfterID = -1 },
		func(q *JobQuery) { q.AfterTime = 1 },
		func(q *JobQuery) { q.UserID = "1 OR 1=1" },
		func(q *JobQuery) { q.Category = "failed" },
		func(q *JobQuery) { q.Kind = "stats"; q.AfterHash = strings.Repeat("a", 64) },
	} {
		q := base
		mutate(&q)
		if q.Validate() == nil {
			t.Fatalf("accepted %+v", q)
		}
	}
	base.Model = "' OR 1=1 --"
	base.Category = "empty_output"
	if err := base.Validate(); err != nil {
		t.Fatal(err)
	}
	query, args := jobSQL(base, "")
	if strings.Contains(query, base.Model) || !strings.Contains(query, "completion_tokens=0") || !strings.Contains(query, "`logs_202607`") {
		t.Fatal(query)
	}
	if args[0] != time.Date(2026, 7, 3, 16, 0, 0, 0, time.UTC).Unix() || args[len(args)-1] != 101 {
		t.Fatal(args)
	}
	base.Category = "missing_output"
	query, _ = jobSQL(base, "")
	if !strings.Contains(query, "completion_tokens IS NULL") {
		t.Fatal(query)
	}
	base.Category = "error"
	query, _ = jobSQL(base, "")
	if !strings.Contains(query, "type=5") {
		t.Fatal(query)
	}
}

func TestJobGrantsDoNotBroadenLegacyReader(t *testing.T) {
	for _, table := range []string{"log_archive_meta", "log_archive_days", "log_archive_day_versions", "log_archive_daily_stats", "logs_202607"} {
		grant := "GRANT SELECT ON `a`.`" + table + "` TO `r`@`%`"
		if !permittedJobGrant(grant, "a") || permittedGrant(grant, "a") {
			t.Fatal(grant)
		}
		if permittedJobGrant(grant, "b") || permittedJobGrant(grant+" WITH GRANT OPTION", "a") {
			t.Fatal(grant)
		}
	}
	for _, grant := range []string{"GRANT SELECT ON `a`.* TO `r`@`%`", "GRANT SELECT, INSERT ON `a`.`logs_202607` TO `r`@`%`", "GRANT SELECT ON `a`.`logs` TO `r`@`%`", "GRANT SELECT ON `a`.`users` TO `r`@`%`"} {
		if permittedJobGrant(grant, "a") {
			t.Fatal(grant)
		}
	}
}

func TestAnomalyQueryIsBoundedAndFiltered(t *testing.T) {
	q := JobQuery{Site: "s", Kind: "anomalies", Date: "2026-07-04", Limit: 1, UserID: "7", ChannelID: "8", Model: "' OR 1=1 --"}
	if err := q.Validate(); err != nil {
		t.Fatal(err)
	}
	query, args := jobSQL(q, "")
	if strings.Contains(query, q.Model) || !strings.Contains(query, "GROUP BY hour ORDER BY hour LIMIT 24") || !strings.Contains(query, "MAX_EXECUTION_TIME(3000)") || !strings.Contains(query, "completion_tokens IS NULL") || !strings.Contains(query, "AND quota>0") {
		t.Fatal(query)
	}
	start := time.Date(2026, 7, 3, 16, 0, 0, 0, time.UTC).Unix()
	if len(args) != 6 || args[0] != start || args[1] != start || args[2] != start+86400 || args[3] != "7" || args[4] != q.Model || args[5] != "8" {
		t.Fatal(args)
	}
	for _, mutate := range []func(*JobQuery){
		func(q *JobQuery) { q.Category = "error" },
		func(q *JobQuery) { q.AfterTime = start },
		func(q *JobQuery) { q.Version = strings.Repeat("a", 32) },
		func(q *JobQuery) { q.Date = "2026-07" },
	} {
		invalid := q
		mutate(&invalid)
		if invalid.Validate() == nil {
			t.Fatalf("accepted %+v", invalid)
		}
	}
}
