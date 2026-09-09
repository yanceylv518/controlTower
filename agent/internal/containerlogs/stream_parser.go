package containerlogs

import (
	"bytes"
	cl "controltower/internal/containerlog"
	"strings"
	"time"
)

type logLine struct {
	text   string
	offset int64
}

type logRecord struct {
	start, end int64
	stamp      time.Time
	lines      []logLine
	oversized  bool
}

// Parser state survives page boundaries, including a split timestamp or record.
type recordParser struct {
	parseTime         func(string) (time.Time, bool)
	singleLine        bool
	offset, lineStart int64
	fragment          []byte
	longLine          bool
	record            *logRecord
	recordBytes       int
	loc               *time.Location
}

func (p *recordParser) feed(data []byte, final bool, emit func(logRecord)) {
	for len(data) > 0 {
		i := bytes.IndexByte(data, '\n')
		n := len(data)
		if i >= 0 {
			n = i + 1
		}
		part := data[:n]
		data = data[n:]
		p.offset += int64(n)
		if !p.longLine {
			if len(p.fragment)+len(part) > 256*1024 {
				p.fragment = nil
				p.longLine = true
			} else {
				p.fragment = append(p.fragment, part...)
			}
		}
		if i >= 0 {
			p.line(emit)
		}
	}
	if final {
		if len(p.fragment) > 0 || p.longLine {
			p.line(emit)
		}
		p.finish(p.offset, emit)
	}
}

func (p *recordParser) finish(end int64, emit func(logRecord)) {
	if p.record != nil {
		p.record.end = end
		emit(*p.record)
	}
	p.record = nil
	p.recordBytes = 0
}

func (p *recordParser) line(emit func(logRecord)) {
	text := strings.TrimSuffix(strings.TrimSuffix(string(p.fragment), "\n"), "\r")
	parseTime := p.parseTime
	if parseTime == nil {
		parseTime = func(s string) (time.Time, bool) { return lineTime(s, p.loc) }
	}
	if p.singleLine {
		p.finish(p.lineStart, emit)
	}
	if p.longLine {
		p.finish(p.lineStart, emit)
		emit(logRecord{start: p.lineStart, end: p.offset, oversized: true})
	} else if stampTime, ok := parseTime(text); ok {
		p.finish(p.lineStart, emit)
		p.record = &logRecord{start: p.lineStart, stamp: stampTime}
	} else if strings.HasPrefix(strings.TrimSpace(text), "{") || (stamp.MatchString(text) && strings.HasPrefix(text, "[")) {
		p.finish(p.lineStart, emit)
	}
	if !p.longLine {
		if p.record == nil {
			p.record = &logRecord{start: p.lineStart}
		}
		p.recordBytes += len(text) + 1
		if p.recordBytes > 256*1024 || len(p.record.lines) >= 2000 {
			p.record.oversized = true
			p.record.lines = nil
		}
		if !p.record.oversized {
			p.record.lines = append(p.record.lines, logLine{text, p.lineStart})
		}
	}
	p.fragment = nil
	p.longLine = false
	p.lineStart = p.offset
}

func parserForSource(source cl.Source, loc *time.Location) *recordParser {
	p := &recordParser{loc: loc}
	if source.Kind == "nginx_access" {
		f := compileNginxFormat(source.LogFormat)
		p.singleLine = true
		p.parseTime = func(line string) (time.Time, bool) { return nginxTime(f.values(line)) }
	}
	if source.Kind == "nginx_error" {
		p.singleLine = true
	}
	return p
}
