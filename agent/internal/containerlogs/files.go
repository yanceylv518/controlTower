package containerlogs

import (
	"bufio"
	"compress/gzip"
	"context"
	cl "controltower/internal/containerlog"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"os"
	"regexp"
	"sort"
	"strings"
	"time"
)

const fileScanLimit int64 = 64 * 1024 * 1024
const maxLogFiles = 256

var logName = regexp.MustCompile(`(?i)^[a-z0-9_.-]+\.log(?:[.-][a-z0-9_.-]+)?$`)
var stamp = regexp.MustCompile(`\d{4}[-/]\d{2}[-/]\d{2}(?:T|[ ]+(?:-[ ]*)?)\d{2}:\d{2}:\d{2}(?:\.\d+)?(?:Z|[+-]\d{2}:\d{2})?`)

func lineTime(line string, loc *time.Location) (time.Time, bool) {
	line = strings.TrimSpace(line)
	parse := func(v string) (time.Time, bool) {
		if t, e := time.Parse(time.RFC3339Nano, v); e == nil {
			return t, true
		}
		for _, layout := range []string{"2006/01/02 - 15:04:05", "2006/01/02 15:04:05", "2006-01-02 15:04:05", "2006-01-02T15:04:05"} {
			if t, e := time.ParseInLocation(layout, v, loc); e == nil {
				return t, true
			}
		}
		return time.Time{}, false
	}
	if strings.HasPrefix(line, "{") {
		var obj map[string]json.RawMessage
		if json.Unmarshal([]byte(line), &obj) == nil {
			for _, key := range []string{"time", "timestamp", "ts", "created_at"} {
				var value string
				if b, ok := obj[key]; ok && json.Unmarshal(b, &value) == nil {
					if t, ok := parse(value); ok {
						return t, true
					}
				}
			}
		}
		return time.Time{}, false
	}
	at := stamp.FindStringIndex(line)
	if at == nil || at[0] > 32 {
		return time.Time{}, false
	}
	prefix := strings.TrimSpace(line[:at[0]])
	if prefix != "" && !(strings.HasPrefix(prefix, "[") && strings.HasSuffix(prefix, "]")) {
		return time.Time{}, false
	}
	return parse(line[at[0]:at[1]])
}

type logFile struct {
	name     string
	modified time.Time
}

// All opens remain beneath one discovered root, including across symlink races.
func ReadFiles(ctx context.Context, dir string, q cl.Query, loc *time.Location) cl.Result {
	result := cl.Result{Status: "succeeded", Lines: []string{}}
	root, err := os.OpenRoot(dir)
	if err != nil {
		result.Status = "failed"
		result.Error = "日志目录不存在或不可读"
		return result
	}
	defer root.Close()
	files := []logFile{}
	entries := 0
	err = fs.WalkDir(root.FS(), ".", func(name string, d fs.DirEntry, e error) error {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		entries++
		if entries > 4096 {
			result.Truncated = true
			return fs.SkipAll
		}
		if e != nil {
			return e
		}
		if d.Type()&os.ModeSymlink != 0 {
			return nil
		}
		if d.IsDir() {
			if strings.Count(name, "/") >= 3 {
				result.Truncated = true
				return fs.SkipDir
			}
			return nil
		}
		if !d.Type().IsRegular() || !logName.MatchString(d.Name()) {
			return nil
		}
		info, e := d.Info()
		if e != nil {
			return e
		}
		files = append(files, logFile{name, info.ModTime()})
		return nil
	})
	if err != nil {
		result.Status = "failed"
		result.Error = "日志目录扫描失败"
		return result
	}
	sort.Slice(files, func(i, j int) bool {
		if files[i].modified.Equal(files[j].modified) {
			return files[i].name < files[j].name
		}
		return files[i].modified.After(files[j].modified)
	})
	if len(files) > maxLogFiles {
		files = files[:maxLogFiles]
		result.Truncated = true
	}
	unknown, unreadable, total := 0, 0, 0
	matches := newMatcher(q)
	for _, entry := range files {
		if ctx.Err() != nil {
			result.Status = "timed_out"
			result.Error = "查询超过执行时限"
			result.Truncated = true
			break
		}
		if result.ScannedBytes >= fileScanLimit {
			result.Truncated = true
			break
		}
		file, e := openLogFile(root, entry.name)
		if e != nil {
			unreadable++
			continue
		}
		info, e := file.Stat()
		if e != nil || !info.Mode().IsRegular() {
			file.Close()
			unreadable++
			continue
		}
		var reader io.Reader = file
		var gz *gzip.Reader
		if strings.HasSuffix(strings.ToLower(entry.name), ".gz") {
			gz, e = gzip.NewReader(file)
			if e != nil {
				file.Close()
				unreadable++
				continue
			}
			reader = gz
		}
		remaining := fileScanLimit - result.ScannedBytes
		limited := &io.LimitedReader{R: reader, N: remaining + 1}
		scan := bufio.NewScanner(limited)
		scan.Buffer(make([]byte, 4096), 256*1024)
		record := []string{}
		recordBytes, lineNumber, startLine := 0, 0, 0
		inRange := false
		recordValid := false
		flush := func() bool {
			if !inRange || !recordValid || len(record) == 0 {
				return true
			}
			if !matches(strings.Join(record, "\n")) {
				return true
			}
			for i, line := range record {
				out := fmt.Sprintf("%s:%d %s", entry.name, startLine+i, Redact(line))
				if len(result.Lines) >= cl.MaxLines || total+len(out) > cl.MaxResultBytes {
					result.Truncated = true
					return false
				}
				result.Lines = append(result.Lines, out)
				total += len(out)
			}
			return true
		}
		stop := false
		for scan.Scan() {
			if ctx.Err() != nil {
				result.Truncated = true
				stop = true
				break
			}
			lineNumber++
			line := strings.TrimSuffix(scan.Text(), "\r")
			if t, ok := lineTime(line, loc); ok {
				if !flush() {
					stop = true
					break
				}
				record = nil
				recordBytes = 0
				inRange = !t.Before(q.From) && t.Before(q.To)
				recordValid = true
				startLine = lineNumber
			} else if strings.HasPrefix(strings.TrimSpace(line), "{") || (stamp.MatchString(line) && strings.HasPrefix(line, "[")) {
				if !flush() {
					stop = true
					break
				}
				record = nil
				recordBytes = 0
				recordValid = false
				inRange = false
				unknown++
				continue
			} else if len(record) == 0 && !recordValid {
				unknown++
				continue
			}
			if recordBytes+len(line) > 256*1024 {
				result.Truncated = true
				recordValid = false
				record = nil
				continue
			}
			if inRange && recordValid {
				record = append(record, line)
				recordBytes += len(line)
			}
		}
		read := remaining + 1 - limited.N
		result.ScannedBytes += min(read, remaining)
		result.FilesScanned++
		if scan.Err() != nil {
			result.Truncated = true
			unreadable++
		}
		if read > remaining {
			result.Truncated = true
			recordValid = false
			stop = true
		}
		if !stop && !flush() {
			stop = true
		}
		if gz != nil {
			gz.Close()
		}
		file.Close()
		if stop {
			break
		}
	}
	if ctx.Err() != nil {
		result.Status = "timed_out"
		result.Error = "查询超过执行时限"
		result.Truncated = true
	}
	if len(files) == 0 {
		result.Note = "发现日志目录，但没有 .log、轮转或 .log.gz 文件"
	}
	if unknown > 0 {
		result.Note += fmt.Sprintf(" %d 行没有可识别的时间且无上文，已跳过。", unknown)
	}
	if unreadable > 0 {
		result.Truncated = true
		result.Note += fmt.Sprintf(" %d 个文件或片段无法完整读取。", unreadable)
	}
	if unreadable > 0 && result.FilesScanned == 0 {
		result.Status = "failed"
		result.Error = "日志文件无法读取或压缩格式不支持"
	}
	return result
}
func newMatcher(q cl.Query) func(string) bool {
	var request, code *regexp.Regexp
	if q.RequestID != "" {
		request = regexp.MustCompile(`(^|[^a-zA-Z0-9_.:-])` + regexp.QuoteMeta(q.RequestID) + `($|[^a-zA-Z0-9_.:-])`)
	}
	if q.ErrorCode != "" {
		code = regexp.MustCompile(`(?i)["']?\b(?:error_code|status_code|status code|status|code)["']?\s*[:=]\s*["']?` + regexp.QuoteMeta(q.ErrorCode) + `(?:["']|$|[^a-zA-Z0-9_.:-])`)
	}
	return func(record string) bool {
		return (q.Keyword == "" || strings.Contains(record, q.Keyword)) && (request == nil || request.MatchString(record)) && (code == nil || code.MatchString(record) || ginCode(record, q.ErrorCode))
	}
}
