package containerlogs

import (
	"compress/gzip"
	"context"
	cl "controltower/internal/containerlog"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestFilesHistoryTimeAndMultiline(t *testing.T) {
	dir := t.TempDir()
	loc, _ := time.LoadLocation("Asia/Shanghai")
	from := time.Date(2026, 9, 8, 12, 0, 0, 0, loc)
	q := cl.Query{From: from, To: from.Add(time.Hour), RequestID: "req-123", ErrorCode: "500"}
	data := "[SYS] 2026/09/08 - 11:59:59 | req-123 status code: 500 outside\n" +
		"[ERR] 2026/09/08 - 12:00:00 | req-123 error\nstatus code: 500 api_key=sk-abcdefghijk\n" +
		"[GIN] 2026/09/08 - 12:01:00 | 200 | req-123 prompt_tokens=500\n" +
		"[GIN] 2026/09/08 - 12:02:00 | 500 | req-1234 wrong id\n" +
		"[GIN] 2026/09/08 - 12:03:00 | 500 | req-123 matching\n" +
		"[GIN] 2026/09/08 - 13:00:00 | 500 | req-123 outside\n"
	f, e := os.Create(filepath.Join(dir, "app.log.1.gz"))
	if e != nil {
		t.Fatal(e)
	}
	z := gzip.NewWriter(f)
	_, _ = z.Write([]byte(data))
	_ = z.Close()
	_ = f.Close()
	if e = os.WriteFile(filepath.Join(dir, "current.log"), []byte("{\"time\":\"2026-09-08T04:04:00Z\",\"request_id\":\"req-123\",\"code\":500}\n"), 0600); e != nil {
		t.Fatal(e)
	}
	if e = os.WriteFile(filepath.Join(dir, "secret.env"), []byte(data), 0600); e != nil {
		t.Fatal(e)
	}
	got := ReadFiles(context.Background(), dir, q, loc)
	if got.Status != "succeeded" || got.Truncated || len(got.Lines) != 4 || got.FilesScanned != 2 {
		t.Fatalf("unexpected result: %#v", got)
	}
	all := strings.Join(got.Lines, "\n")
	for _, bad := range []string{"outside", "prompt_tokens", "req-1234", "sk-abcdefghijk", "secret.env"} {
		if strings.Contains(all, bad) {
			t.Fatalf("unexpected %q: %s", bad, all)
		}
	}
	if !strings.Contains(all, "app.log.1.gz:2") || !strings.Contains(all, "[REDACTED]") {
		t.Fatal(all)
	}
}

func TestFilesSkippedContentAndLimits(t *testing.T) {
	dir := t.TempDir()
	now := time.Now().UTC()
	q := cl.Query{From: now.Add(-time.Minute), To: now.Add(time.Minute)}
	data := "unparseable timestamp\n" + strings.Repeat(now.Format(time.RFC3339)+" log\n", cl.MaxLines+1)
	if e := os.WriteFile(filepath.Join(dir, "app.log"), []byte(data), 0600); e != nil {
		t.Fatal(e)
	}
	got := ReadFiles(context.Background(), dir, q, time.UTC)
	if !got.Truncated || len(got.Lines) != cl.MaxLines || got.Note == "" {
		t.Fatalf("missing cap or skipped notice: %#v", got)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if r := ReadFiles(ctx, dir, q, time.UTC); r.Status == "succeeded" {
		t.Fatal("canceled query reported complete")
	}
}

func TestDiscoveryMetadataAndMounts(t *testing.T) {
	dir := t.TempDir()
	id := strings.Repeat("a", 64)
	m := dockerMeta{ID: id, Name: "/new-api-prod", Image: "example/new-api:latest", Path: "/new-api", Args: []string{"--log-dir", "/app/logs"}, Mounts: []dockerMount{{Type: "volume", Source: dir, Destination: "/app/logs"}}}
	calls := 0
	run := func(_ context.Context, args []string) ([]byte, error) {
		calls++
		if args[0] == "ps" {
			return []byte(id), nil
		}
		if len(args) != 4 || args[0] != "inspect" || args[2] != inspectFormat || args[3] != id || strings.Contains(args[2], ".Env") {
			t.Fatalf("unexpected discovery command: %v", args)
		}
		return json.Marshal(m)
	}
	inv := Discover(context.Background(), run, nil, "Asia/Shanghai")
	if calls != 2 || len(inv.Sources) != 1 || !inv.Sources[0].Available || inv.Sources[0].HostDir != dir || !cl.ValidSourceID(inv.Sources[0].ID) {
		t.Fatalf("bad discovery: %#v", inv)
	}
	b, _ := json.Marshal(inv)
	if strings.Contains(string(b), "HostDir") || strings.Contains(string(b), "host_dir") {
		t.Fatal("host directory exposed")
	}
	original := inv.Sources[0].ID
	m.ID = strings.Repeat("b", 64)
	source, _ := sourceFromMeta(m, nil, "Asia/Shanghai")
	if original == source.ID {
		t.Fatal("container recreation not detected")
	}
	m.Mounts = append(m.Mounts, dockerMount{Type: "tmpfs", Destination: "/app/logs/sub"})
	m.Args = []string{"--log-dir=/app/logs/sub"}
	source, _ = sourceFromMeta(m, nil, "Asia/Shanghai")
	if source.Available {
		t.Fatal("read through shadowing mount")
	}
	m.Path = "/bin/sh"
	m.Args = []string{"-c", "new-api --log-dir /app/logs"}
	source, found := sourceFromMeta(m, nil, "Asia/Shanghai")
	if !found || source.Available || source.Reason == "" {
		t.Fatal("name-only guess accepted")
	}
	m.Name = "/postgres"
	m.Image = "postgres"
	if _, found = sourceFromMeta(m, nil, "Asia/Shanghai"); found {
		t.Fatal("unrelated container discovered")
	}
}
