package containerlogs

import (
	"bytes"
	"strings"
	"time"
)

type logLine struct {
	text   string
	offset int64
}

type indexedRecord struct {
	start, end int64
	stamp      time.Time
	lines      []logLine
	oversized  bool
}

// Parser state survives page boundaries, including a split timestamp or record.
type recordParser struct {
	offset, lineStart int64
	fragment          []byte
	longLine          bool
	record            *indexedRecord
	recordBytes       int
	loc               *time.Location
}

func (p *recordParser) feed(data []byte, final bool, emit func(indexedRecord)) {
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

func (p *recordParser) finish(end int64, emit func(indexedRecord)) {
	if p.record != nil {
		p.record.end = end
		emit(*p.record)
	}
	p.record = nil
	p.recordBytes = 0
}

func (p *recordParser) line(emit func(indexedRecord)) {
	text := strings.TrimSuffix(strings.TrimSuffix(string(p.fragment), "\n"), "\r")
	if p.longLine {
		p.finish(p.lineStart, emit)
		emit(indexedRecord{start: p.lineStart, end: p.offset, oversized: true})
	} else if stampTime, ok := lineTime(text, p.loc); ok {
		p.finish(p.lineStart, emit)
		p.record = &indexedRecord{start: p.lineStart, stamp: stampTime}
	} else if strings.HasPrefix(strings.TrimSpace(text), "{") || (stamp.MatchString(text) && strings.HasPrefix(text, "[")) {
		p.finish(p.lineStart, emit)
	}
	if !p.longLine {
		if p.record == nil {
			p.record = &indexedRecord{start: p.lineStart}
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

type timeSpan struct {
	start, end int64
	min, max   time.Time
}
type sparseIndex struct {
	offset            int64
	spans             []timeSpan
	pending           *timeSpan
	parser            recordParser
	complete, skipped bool
	blockSize         int64
}

func newSparseIndex(start int64, loc *time.Location) *sparseIndex {
	return &sparseIndex{offset: start, parser: recordParser{offset: start, lineStart: start, loc: loc}, blockSize: 1024 * 1024}
}
func (i *sparseIndex) add(r indexedRecord) {
	if i.pending == nil {
		i.pending = &timeSpan{start: r.start}
	}
	i.pending.end = r.end
	if !r.stamp.IsZero() {
		if i.pending.min.IsZero() || r.stamp.Before(i.pending.min) {
			i.pending.min = r.stamp
		}
		if i.pending.max.IsZero() || r.stamp.After(i.pending.max) {
			i.pending.max = r.stamp
		}
	}
	if r.oversized || r.stamp.IsZero() {
		i.skipped = true
	}
	if i.pending.end-i.pending.start >= i.blockSize {
		i.flush()
	}
}
func (i *sparseIndex) flush() {
	if i.pending == nil {
		return
	}
	i.spans = append(i.spans, *i.pending)
	i.pending = nil
	// Keep a sparse, bounded index even for very large files. Coarser spans affect
	// speed, not correctness; min/max includes out-of-order records.
	if len(i.spans) > 4096 {
		compact := make([]timeSpan, 0, (len(i.spans)+1)/2)
		for n := 0; n < len(i.spans); n += 2 {
			s := i.spans[n]
			if n+1 < len(i.spans) {
				t := i.spans[n+1]
				s.end = t.end
				if s.min.IsZero() || (!t.min.IsZero() && t.min.Before(s.min)) {
					s.min = t.min
				}
				if t.max.After(s.max) {
					s.max = t.max
				}
			}
			compact = append(compact, s)
		}
		i.spans = compact
		i.blockSize *= 2
	}
}
