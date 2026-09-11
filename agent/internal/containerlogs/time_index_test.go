package containerlogs

import (
	"context"
	cl "controltower/internal/containerlog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestTimeIndexOutOfOrderAndReplacement(t *testing.T) {
	dir := t.TempDir()
	now := time.Now().UTC().Truncate(time.Second)
	q := cl.Query{From: now.Add(-time.Minute), To: now, Keyword: "needle"}
	line := func(at time.Time, body string) string { return at.Format(time.RFC3339) + " " + body + "\n" }
	old := line(now.Add(-time.Hour), strings.Repeat("x", 8192))
	// A matching record followed by old timestamps must not be pruned.
	body := strings.Repeat(old, 180) + line(now.Add(-time.Second), "needle-out-of-order") + strings.Repeat(old, 180)
	p := filepath.Join(dir, "app.log")
	if err := os.WriteFile(p, []byte(body), 0600); err != nil {
		t.Fatal(err)
	}
	e := NewStreamEngine()
	cold, coldBytes, _ := finishStream(t, e, dir, q)
	warm, warmBytes, _ := finishStream(t, e, dir, q)
	t.Logf("application log bytes: cold=%d warm=%d", coldBytes, warmBytes)
	if strings.Join(cold, "\n") != strings.Join(warm, "\n") || len(warm) != 1 || warmBytes >= coldBytes {
		t.Fatalf("cold=%d warm=%d lines=%v", coldBytes, warmBytes, warm)
	}
	if err := os.Remove(p); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(line(now.Add(-time.Second), "needle-replacement")), 0600); err != nil {
		t.Fatal(err)
	}
	got := e.Query(context.Background(), dir, q, time.UTC)
	if len(got.Lines) != 1 || !strings.Contains(got.Lines[0], "replacement") {
		t.Fatal(got)
	}
}

func TestNginxTimeIndexReusesBlocks(t *testing.T) {
	dir := t.TempDir()
	now := time.Now().UTC().Truncate(time.Second)
	line := func(at time.Time, path string) string { return at.Format(time.RFC3339) + " GET " + path + " 200\n" }
	old := line(now.Add(-time.Hour), strings.Repeat("x", 8192))
	body := strings.Repeat(old, 400) + line(now.Add(-time.Second), "/needle")
	if err := os.WriteFile(filepath.Join(dir, "access.log"), []byte(body), 0600); err != nil {
		t.Fatal(err)
	}
	s := cl.Source{Kind: "nginx_access", HostDir: dir, FileName: "access.log", LogFormat: "$time_iso8601 $request_method $uri $status"}
	q := cl.Query{From: now.Add(-time.Minute), To: now, Keyword: "needle"}
	e := NewStreamEngine()
	cold := e.QuerySource(context.Background(), s, q, time.UTC)
	warm := e.QuerySource(context.Background(), s, q, time.UTC)
	t.Logf("nginx log bytes: cold=%d warm=%d", cold.ScannedBytes, warm.ScannedBytes)
	if !cold.Complete || !warm.Complete || len(warm.Lines) != 1 || warm.ScannedBytes >= cold.ScannedBytes {
		t.Fatalf("cold=%+v warm=%+v", cold, warm)
	}
}
