package containerlogs

import (
	cl "controltower/internal/containerlog"
	"math"
	"net"
	"regexp"
	"strconv"
	"strings"
	"time"
)

const combinedFormat = `$remote_addr - $remote_user [$time_local] "$request" $status $body_bytes_sent "$http_referer" "$http_user_agent"`

var nginxVariable = regexp.MustCompile(`\$\{?([a-zA-Z0-9_]+)\}?`)
var nginxRotation = regexp.MustCompile(`^(?:[.-][0-9][0-9_.-]*)?(?:\.gz)?$`)
var nginxQuerySecret = regexp.MustCompile(`(?i)([?&](?:token|key|api_key|access_token|refresh_token|password|secret|authorization)=)[^&\s"']*`)

func redactNginx(line string) string {
	return nginxQuerySecret.ReplaceAllString(Redact(line), "${1}[REDACTED]")
}

func nginxLogFile(name, base string) bool {
	return name == base || (strings.HasPrefix(name, base) && nginxRotation.MatchString(strings.TrimPrefix(name, base)))
}

type nginxFormat struct {
	re    *regexp.Regexp
	names []string
}

func (f *nginxFormat) redact(line string) string {
	if f == nil {
		return line
	}
	indices := f.re.FindStringSubmatchIndex(line)
	if indices == nil {
		return line
	}
	for i := len(f.names) - 1; i >= 0; i-- {
		name := strings.ToLower(f.names[i])
		sensitive := name == "request_body" || name == "args"
		for _, word := range []string{"authorization", "cookie", "api_key", "access_token", "refresh_token", "arg_token", "password", "secret"} {
			if strings.Contains(name, word) {
				sensitive = true
			}
		}
		if sensitive {
			start, end := indices[2*(i+1)], indices[2*(i+1)+1]
			line = line[:start] + "[REDACTED]" + line[end:]
		}
	}
	return line
}

func compileNginxFormat(format string) *nginxFormat {
	if len(format) > 8192 {
		return nil
	}
	var pattern strings.Builder
	pattern.WriteString("^")
	last := 0
	names := []string{}
	for _, m := range nginxVariable.FindAllStringSubmatchIndex(format, -1) {
		pattern.WriteString(regexp.QuoteMeta(format[last:m[0]]))
		pattern.WriteString(`(.*?)`)
		names = append(names, format[m[2]:m[3]])
		last = m[1]
	}
	pattern.WriteString(regexp.QuoteMeta(format[last:]))
	pattern.WriteString("$")
	re, err := regexp.Compile(pattern.String())
	if err != nil {
		return nil
	}
	return &nginxFormat{re, names}
}
func (f *nginxFormat) values(line string) map[string]string {
	if f == nil {
		return nil
	}
	m := f.re.FindStringSubmatch(line)
	if m == nil {
		return nil
	}
	v := map[string]string{}
	for i, n := range f.names {
		v[n] = m[i+1]
	}
	return v
}
func nginxTime(v map[string]string) (time.Time, bool) {
	for _, entry := range []struct{ key, layout string }{{"time_iso8601", time.RFC3339Nano}, {"time_local", "02/Jan/2006:15:04:05 -0700"}} {
		if t, e := time.Parse(entry.layout, v[entry.key]); e == nil {
			return t, true
		}
	}
	if n, e := strconv.ParseFloat(v["msec"], 64); e == nil && n > 0 && !math.IsInf(n, 0) && !math.IsNaN(n) && n < 1e12 {
		return time.UnixMilli(int64(n * 1000)), true
	}
	return time.Time{}, false
}
func nginxFields(format, kind string) []string {
	if kind == "nginx_error" {
		return []string{"level"}
	}
	f := compileNginxFormat(format)
	if f == nil {
		return nil
	}
	has := map[string]bool{}
	for _, n := range f.names {
		has[n] = true
	}
	fields := []string{}
	if has["time_local"] || has["time_iso8601"] || has["msec"] {
		fields = append(fields, "time")
	}
	if has["status"] {
		fields = append(fields, "status")
	}
	if has["request_id"] || has["http_x_request_id"] {
		fields = append(fields, "request_id")
	}
	if has["request"] || has["request_uri"] || has["uri"] {
		fields = append(fields, "path")
	}
	if has["request_time"] {
		fields = append(fields, "duration")
	}
	if has["host"] || has["http_host"] || has["server_name"] {
		fields = append(fields, "host")
	}
	return fields
}
func hasField(s cl.Source, field string) bool {
	for _, f := range s.Fields {
		if f == field {
			return true
		}
	}
	return false
}
func normalizedHost(s string) string {
	if h, _, e := net.SplitHostPort(s); e == nil {
		s = h
	}
	return strings.TrimSuffix(strings.ToLower(s), ".")
}

var nginxErrorLevel = regexp.MustCompile(`^\d{4}/\d{2}/\d{2} \d{2}:\d{2}:\d{2} \[([a-z]+)\]`)
var nginxErrorHost = regexp.MustCompile(`(?:^|, )server: ([^,\s]+)`)

func nginxMatcher(source cl.Source, q cl.Query) func(string) bool {
	f := compileNginxFormat(source.LogFormat)
	return func(line string) bool {
		if q.Keyword != "" && !strings.Contains(line, q.Keyword) {
			return false
		}
		if source.Kind == "nginx_error" {
			m := nginxErrorLevel.FindStringSubmatch(line)
			if len(m) < 2 || q.Level != "" && m[1] != q.Level {
				return false
			}
			if source.Shared && q.Host != "" {
				h := nginxErrorHost.FindStringSubmatch(line)
				return len(h) > 1 && normalizedHost(h[1]) == normalizedHost(q.Host)
			}
			return true
		}
		v := f.values(line)
		if v == nil {
			return false
		}
		if q.Host != "" && source.Shared {
			host := v["host"]
			if host == "" {
				host = v["http_host"]
			}
			if host == "" {
				host = v["server_name"]
			}
			if normalizedHost(host) != normalizedHost(q.Host) {
				return false
			}
		}
		if q.ErrorCode != "" && v["status"] != q.ErrorCode {
			return false
		}
		if q.RequestID != "" && v["request_id"] != q.RequestID && v["http_x_request_id"] != q.RequestID {
			return false
		}
		uri := v["request_uri"]
		if uri == "" {
			uri = v["uri"]
		}
		if uri == "" {
			parts := strings.Fields(v["request"])
			if len(parts) > 1 {
				uri = parts[1]
			}
		}
		if q.Path != "" && !strings.Contains(uri, q.Path) {
			return false
		}
		if q.MinDurationMS > 0 {
			seconds, e := strconv.ParseFloat(v["request_time"], 64)
			if e != nil || math.IsNaN(seconds) || math.IsInf(seconds, 0) || seconds*1000 < float64(q.MinDurationMS) {
				return false
			}
		}
		return true
	}
}
