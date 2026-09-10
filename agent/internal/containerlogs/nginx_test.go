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

func nginxFixture(t *testing.T, config string, files map[string]string) string {
	t.Helper()
	root := t.TempDir()
	files["etc/nginx/nginx.conf"] = config
	for name, body := range files {
		p := filepath.Join(root, filepath.FromSlash(name))
		if e := os.MkdirAll(filepath.Dir(p), 0755); e != nil {
			t.Fatal(e)
		}
		if e := os.WriteFile(p, []byte(body), 0600); e != nil {
			t.Fatal(e)
		}
	}
	return root
}
func TestNginxDiscoveryAndFileQuery(t *testing.T) {
	config := `http { log_format timing '$host [$time_local] "$request" $status $request_time $request_id'; include conf.d/*.conf; }`
	root := nginxFixture(t, config, map[string]string{
		"etc/nginx/conf.d/site.conf":  `server { server_name example.com; access_log /var/log/nginx/access.log timing; error_log /var/log/nginx/error.log warn; location /off { access_log off; } }`,
		"var/log/nginx/access.log":    "example.com [09/Sep/2026:10:00:00 +0800] \"GET /v1/chat HTTP/1.1\" 502 1.2 req-1\nexample.com [09/Sep/2026:10:00:01 +0800] \"GET /other HTTP/1.1\" 200 0.1 req-2\n",
		"var/log/nginx/error.log":     "2026/09/09 10:00:00 [error] 1#1: upstream timed out\n",
		"var/log/nginx/unrelated.log": "SECRET unrelated log\n",
	})
	sources := discoverNginxConfig(context.Background(), root, "/etc/nginx/nginx.conf", "", "nginx-host", "host", "Asia/Shanghai")
	if len(sources) != 2 {
		t.Fatalf("sources: %+v", sources)
	}
	from := time.Date(2026, 9, 9, 2, 0, 0, 0, time.UTC)
	for _, s := range sources {
		if !s.Available {
			t.Fatalf("unavailable: %+v", s)
		}
		q := cl.Query{Kind: s.Kind, Host: "example.com", SourceID: s.ID, Container: s.Container, From: from, To: from.Add(time.Minute)}
		if s.Kind == "nginx_access" {
			q.RequestID = "req-1"
			q.ErrorCode = "502"
			q.Path = "/v1"
			q.MinDurationMS = 1000
		} else {
			q.Level = "error"
		}
		if e := cl.ValidateSourceQuery(s, q); e != nil {
			t.Fatal(e)
		}
		loc, _ := time.LoadLocation("Asia/Shanghai")
		got := NewStreamEngine().QuerySource(context.Background(), s, q, loc)
		if got.Status != "succeeded" || !got.Complete || len(got.Lines) != 1 || strings.Contains(strings.Join(got.Lines, ""), "SECRET") {
			t.Fatalf("query: %+v", got)
		}
	}
}

func TestNginxSharedLogDomainIsolation(t *testing.T) {
	config := `http { log_format domains '$host [$time_local] "$request" $status'; access_log /logs/access.log domains; error_log /logs/error.log; server {server_name one.example;} server {server_name two.example;} }`
	root := nginxFixture(t, config, map[string]string{"logs/access.log": "one.example [09/Sep/2026:10:00:00 +0800] \"GET / HTTP/1.1\" 200\ntwo.example [09/Sep/2026:10:00:00 +0800] \"GET / HTTP/1.1\" 500\n", "logs/error.log": ""})
	sources := discoverNginxConfig(context.Background(), root, "/etc/nginx/nginx.conf", "", "nginx-host", "host", "Asia/Shanghai")
	for _, s := range sources {
		if s.Kind == "nginx_error" {
			if !s.Available {
				t.Fatal("local error log unavailable")
			}
			continue
		}
		if !s.Available || !s.Shared {
			t.Fatalf("shared access: %+v", s)
		}
		q := cl.Query{Kind: s.Kind, Host: "one.example", From: time.Date(2026, 9, 9, 2, 0, 0, 0, time.UTC), To: time.Date(2026, 9, 9, 2, 1, 0, 0, time.UTC)}
		got := NewStreamEngine().QuerySource(context.Background(), s, q, time.UTC)
		if len(got.Lines) != 1 || strings.Contains(got.Lines[0], "two.example") {
			t.Fatalf("domain leak %+v", got)
		}
		q.Host = "other.example"
		if cl.ValidateSourceQuery(s, q) == nil {
			t.Fatal("foreign domain allowed")
		}
		q.Host = ""
		if err := cl.ValidateSourceQuery(s, q); err != nil {
			t.Fatal(err)
		}
		got = NewStreamEngine().QuerySource(context.Background(), s, q, time.UTC)
		if len(got.Lines) != 2 {
			t.Fatalf("local query should include both records: %+v", got)
		}
	}
}

func TestNginxDiscoveryUnsupportedAndOff(t *testing.T) {
	for _, cfg := range []string{
		`http {server {server_name one.example;access_log /logs/$host.log;}}`,
		`http {server {server_name one.example;access_log /dev/stdout;}}`,
	} {
		root := nginxFixture(t, cfg, map[string]string{"logs/access.log": ""})
		for _, s := range discoverNginxConfig(context.Background(), root, "/etc/nginx/nginx.conf", "", "nginx", "host", "UTC") {
			if s.Kind == "nginx_access" && s.Available {
				t.Fatalf("unsupported source available %+v", s)
			}
		}
	}
	root := nginxFixture(t, `http {server {server_name example.com;access_log off;}}`, map[string]string{})
	for _, s := range discoverNginxConfig(context.Background(), root, "/etc/nginx/nginx.conf", "", "nginx", "host", "UTC") {
		if s.Kind == "nginx_access" {
			t.Fatal("off log discovered")
		}
	}
	for _, name := range []string{"access.log", "access.log.1", "access.log.2.gz", "access.log-20260909.gz"} {
		if !nginxLogFile(name, "access.log") {
			t.Fatal(name)
		}
	}
	for _, name := range []string{"error.log", "access.log.secret", "access.log/secret"} {
		if nginxLogFile(name, "access.log") {
			t.Fatal(name)
		}
	}
}

func TestNginxLocalLogsWithoutDomain(t *testing.T) {
	root := nginxFixture(t, `http { server { listen 80; server_name _; access_log /logs/access.log combined; error_log /logs/error.log; } }`, map[string]string{
		"logs/access.log": "127.0.0.1 - - [09/Sep/2026:10:00:00 +0800] \"GET / HTTP/1.1\" 200 12 \"-\" \"agent\"\n",
		"logs/error.log":  "2026/09/09 10:00:00 [error] 1#1: local failure\n",
	})
	sources := discoverNginxConfig(context.Background(), root, "/etc/nginx/nginx.conf", "", "nginx-host", "host", "Asia/Shanghai")
	if len(sources) != 2 {
		t.Fatalf("sources: %+v", sources)
	}
	zone := time.FixedZone("CST", 8*3600)
	for _, source := range sources {
		if !source.Available {
			t.Fatalf("local log unavailable: %+v", source)
		}
		q := cl.Query{Kind: source.Kind, From: time.Date(2026, 9, 9, 2, 0, 0, 0, time.UTC), To: time.Date(2026, 9, 9, 2, 1, 0, 0, time.UTC)}
		if err := cl.ValidateSourceQuery(source, q); err != nil {
			t.Fatal(err)
		}
		got := NewStreamEngine().QuerySource(context.Background(), source, q, zone)
		if len(got.Lines) != 1 {
			t.Fatalf("local query: %+v", got)
		}
	}
}

func TestNginxJSONFormatAndSensitiveFields(t *testing.T) {
	f := compileNginxFormat(`{"time":"$time_iso8601","status":$status,"request_id":"$request_id","auth":"$http_authorization","body":"$request_body"}`)
	line := `{"time":"2026-09-09T10:00:00+08:00","status":502,"request_id":"req-1","auth":"Bearer private-token","body":"private payload"}`
	if _, ok := nginxTime(f.values(line)); !ok {
		t.Fatal("JSON time not recognized")
	}
	redacted := f.redact(line)
	if strings.Contains(redacted, "private") || !strings.Contains(redacted, "req-1") {
		t.Fatal(redacted)
	}
	if got := redactNginx(`GET /?token=private&next=ok HTTP/1.1`); strings.Contains(got, "private") || !strings.Contains(got, "next=ok") {
		t.Fatal(got)
	}
	source := cl.Source{Kind: "nginx_access", LogFormat: `$status $request_time`, Fields: []string{"status", "duration"}, Domains: []string{"example.com"}}
	q := cl.Query{Kind: "nginx_access", Host: "example.com", ErrorCode: "502", MinDurationMS: 1000}
	if nginxMatcher(source, q)("502 NaN") || nginxMatcher(source, q)("502 0.5") || !nginxMatcher(source, q)("502 2.0") {
		t.Fatal("duration filter")
	}
	q.RequestID = "unsupported"
	if cl.ValidateSourceQuery(source, q) == nil {
		t.Fatal("unsupported field accepted")
	}
}
