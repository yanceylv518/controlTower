package containerlogs

import (
	cl "controltower/internal/containerlog"
	"os"
	"time"
)

const timeBlockBytes int64 = 1 << 20
const maxTimeIndexBlocks = 8192

// Blocks end at record boundaries. Min/max timestamps tolerate out-of-order logs.
// No raw log data, request IDs or keywords are retained in the index.
type timeBlock struct {
	start, end int64
	min, max   time.Time
	unknown    bool
}
type fileTimeIndex struct {
	key    string
	snap   fileSnapshot
	blocks []timeBlock
}
type indexBuild struct {
	blocks   []timeBlock
	next     int64
	disabled bool
}

func (b *indexBuild) observe(r logRecord) {
	if b == nil || b.disabled || r.start < b.next {
		return
	}
	if len(b.blocks) == 0 || b.blocks[len(b.blocks)-1].end-b.blocks[len(b.blocks)-1].start >= timeBlockBytes {
		if len(b.blocks) >= maxTimeIndexBlocks {
			b.disabled = true
			return
		}
		b.blocks = append(b.blocks, timeBlock{start: r.start})
	}
	v := &b.blocks[len(b.blocks)-1]
	v.end = r.end
	if r.stamp.IsZero() || r.oversized {
		v.unknown = true
	}
	if !r.stamp.IsZero() {
		if v.min.IsZero() || r.stamp.Before(v.min) {
			v.min = r.stamp
		}
		if v.max.IsZero() || r.stamp.After(v.max) {
			v.max = r.stamp
		}
	}
}
func timeIndexKey(s *searchSession, loc *time.Location) string {
	return s.dir + "\x00" + s.files[s.filePos].name + "\x00" + s.source.Kind + "\x00" + s.source.LogFormat + "\x00" + loc.String()
}
func overlaps(b timeBlock, q cl.Query) bool {
	return b.unknown || (!b.max.Before(q.From) && b.min.Before(q.To))
}
func (e *StreamEngine) prepareTimeIndex(s *searchSession, f *os.File, loc *time.Location) {
	snap := s.files[s.filePos]
	s.indexKey = timeIndexKey(s, loc)
	s.build = &indexBuild{}
	var cached *fileTimeIndex
	for n := len(e.indexes) - 1; n >= 0; n-- {
		if e.indexes[n].key == s.indexKey {
			item := e.indexes[n]
			e.indexes = append(e.indexes[:n], e.indexes[n+1:]...)
			if snapshotMatches(f, item.snap) {
				cached = &item
				e.indexes = append(e.indexes, item)
			}
			break
		}
	}
	if cached == nil {
		s.ranges = []timeBlock{{end: snap.info.Size()}}
		return
	}
	blocks := cached.blocks
	same := snap.info.Size() == cached.snap.info.Size()
	if same {
		s.build = nil
	} else if len(blocks) > 0 {
		// The final record may have been a partial line at EOF. Rescan the last block.
		blocks = blocks[:len(blocks)-1]
		s.build.blocks = append([]timeBlock(nil), blocks...)
		if len(blocks) > 0 {
			s.build.next = blocks[len(blocks)-1].end
		}
	}
	for _, b := range blocks {
		if overlaps(b, s.query) {
			s.ranges = append(s.ranges, b)
		}
	}
	if !same {
		s.ranges = append(s.ranges, timeBlock{start: s.build.next, end: snap.info.Size()})
	}
}
func (e *StreamEngine) finishTimeIndex(s *searchSession) {
	b := s.build
	if b == nil || b.disabled {
		return
	}
	for n := len(e.indexes) - 1; n >= 0; n-- {
		if e.indexes[n].key == s.indexKey {
			e.indexes = append(e.indexes[:n], e.indexes[n+1:]...)
		}
	}
	total := len(b.blocks)
	for _, v := range e.indexes {
		total += len(v.blocks)
	}
	for len(e.indexes) > 0 && (total > maxTimeIndexBlocks || len(e.indexes) >= 64) {
		total -= len(e.indexes[0].blocks)
		e.indexes = e.indexes[1:]
	}
	e.indexes = append(e.indexes, fileTimeIndex{key: s.indexKey, snap: s.files[s.filePos], blocks: b.blocks})
}
