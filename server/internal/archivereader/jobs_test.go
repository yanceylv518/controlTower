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
		if permittedJobGrant(grant+" WITH GRANT OPTION", "a") {
			t.Fatal(grant)
		}
	}
	for _, grant := range []string{"GRANT SELECT, INSERT ON `a`.`logs_202607` TO `r`@`%`"} {
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

func TestJobDatabaseSelect(t *testing.T) {
	good := "GRANT SELECT ON `archive`.* TO `r`@`%`"
	if !permittedJobGrant(good, "archive") || permittedGrant(good, "archive") {
		t.Fatal("database SELECT compatibility")
	}
	for _, g := range []string{good + " WITH GRANT OPTION", "GRANT SELECT, INSERT ON `archive`.* TO `r`@`%`", "GRANT ALL PRIVILEGES ON `archive`.* TO `r`@`%`"} {
		if permittedJobGrant(g, "archive") {
			t.Fatal(g)
		}
	}
}

func TestManagedReadonlyCapabilities(t *testing.T) {
	grants := []string{
		"GRANT PROCESS, REPLICATION SLAVE, REPLICATION CLIENT ON *.* TO `logs_readonly`@`%`",
		"GRANT XA_RECOVER_ADMIN ON *.* TO `logs_readonly`@`%`",
		"GRANT SELECT, LOCK TABLES, SHOW VIEW ON `pinducloud\\_logs\\_archive`.* TO `logs_readonly`@`%`",
		"GRANT SELECT ON `mysql`.`general_log` TO `logs_readonly`@`%`",
		"GRANT SELECT ON `mysql`.`help_topic` TO `logs_readonly`@`%`",
		"GRANT SELECT ON `another_db`.* TO `logs_readonly`@`%`",
		"grant select, show view on `archive`.* to `r`@`%`",
		"GRANT SELECT ON *.* TO `r`@`%`",
	}
	for _, g := range grants {
		if !permittedJobGrant(g, "pinducloud_logs_archive") {
			t.Fatal(g)
		}
	}
	for _, priv := range []string{"INSERT", "UPDATE", "DELETE", "CREATE", "DROP", "ALTER", "INDEX", "TRIGGER", "EVENT", "EXECUTE", "CREATE ROUTINE", "ALTER ROUTINE", "CREATE TEMPORARY TABLES", "FILE", "SUPER", "CREATE USER", "ROLE_ADMIN", "SYSTEM_VARIABLES_ADMIN", "ALL PRIVILEGES", "UNKNOWN_ADMIN"} {
		for _, scope := range []string{"*.*", "`archive`.*", "`other`.`users`"} {
			g := "GRANT SELECT, " + priv + " ON " + scope + " TO `r`@`%`"
			if permittedJobGrant(g, "archive") {
				t.Fatal(g)
			}
		}
	}
	for _, g := range []string{"GRANT `role`@`%` TO `r`@`%`", "GRANT PROXY ON ``@`` TO `r`@`%`", grants[0] + " WITH GRANT OPTION", "invalid"} {
		if permittedJobGrant(g, "archive") {
			t.Fatal(g)
		}
	}
}

func TestArchiveChannelSchemaVariants(t *testing.T) {
	for _, tc := range []struct {
		id, legacy bool
		column     string
	}{{true, false, "`channel_id`"}, {false, true, "`channel`"}, {true, true, "COALESCE(`channel_id`,`channel`)"}, {false, false, "NULL"}} {
		column := archiveChannelColumn(tc.id, tc.legacy)
		if column != tc.column {
			t.Fatal(column)
		}
		for _, kind := range []string{"logs", "anomalies"} {
			q := JobQuery{Kind: kind, Date: "2026-07-07", Limit: 100, ChannelID: "8", channelColumn: column}
			query, args := jobSQL(q, "")
			if !strings.Contains(query, " AND "+column+"=?") {
				t.Fatal(query)
			}
			if kind == "logs" && !strings.Contains(query, column+" AS channel") {
				t.Fatal(query)
			}
			found := false
			for _, a := range args {
				if a == "8" {
					found = true
				}
			}
			if !found {
				t.Fatal(args)
			}
		}
	}
}
