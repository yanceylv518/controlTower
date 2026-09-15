package containerlogs

import (
	cl "controltower/internal/containerlog"
	"strings"
	"testing"
	"time"
)

func TestNginxErrorSeverityFilter(t *testing.T) {
	for _, filter := range []string{"", "error_and_above", "error", "crit", "alert", "emerg"} {
		t.Run("filter="+filter, func(t *testing.T) {
			match := nginxMatcher(cl.Source{Kind: "nginx_error"}, cl.Query{Level: filter})
			for _, level := range []string{"debug", "info", "notice", "warn", "error", "crit", "alert", "emerg"} {
				want := level == filter
				if filter == "" || filter == "error_and_above" {
					want = level == "error" || level == "crit" || level == "alert" || level == "emerg"
				}
				line := "2026/09/11 16:35:05 [" + level + "] message mentioning [error]"
				if got := match(line); got != want {
					t.Errorf("level=%s got=%v want=%v", level, got, want)
				}
			}
		})
	}
}

func TestNginxErrorSeverityQueryAccepted(t *testing.T) {
	now := time.Now().UTC()
	q := cl.Query{Kind: "nginx_error", Level: "error_and_above", Container: "nginx-host", SourceID: strings.Repeat("a", 64), From: now.Add(-time.Minute), To: now}
	if err := q.Validate(now); err != nil {
		t.Fatal(err)
	}
}
